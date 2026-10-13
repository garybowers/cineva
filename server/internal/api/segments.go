package api

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/segments"
	"github.com/garybowers/cineva/server/internal/store"
)

// GET /segments/status — what intro detection is doing, for the admin's
// activity page. Progress is also pushed over /events as "segments".
func (s *server) segmentStatus(c echo.Context) error {
	if s.segments == nil {
		return c.JSON(http.StatusOK, segmentStatusResponse{Enabled: false})
	}
	return c.JSON(http.StatusOK, segmentStatusResponse{Enabled: true, Status: s.segments.Status()})
}

// POST /segments/pause and /segments/resume — hold background work (intro
// detection and seek-bar previews), or let it carry on. Kept across
// restarts, so a pause means "until I say", not "until the server next
// starts".
func (s *server) pauseSegments(c echo.Context) error  { return s.setSegmentsPaused(c, true) }
func (s *server) resumeSegments(c echo.Context) error { return s.setSegmentsPaused(c, false) }

func (s *server) setSegmentsPaused(c echo.Context, paused bool) error {
	v := "false"
	if paused {
		v = "true"
	}
	if err := s.store.SetSetting(c.Request().Context(), settingDetectionPaused, v); err != nil {
		return err
	}
	s.background.SetPaused(paused)
	if s.segments != nil {
		s.segments.SetPaused(paused) // the shared policy, and detection's status
	}
	s.previews.Notify()
	action := "segments.resume"
	if paused {
		action = "segments.pause"
	}
	s.audit(c, action, "settings", settingDetectionPaused, "Intro & credits detection and seek-bar previews", "")
	return s.segmentStatus(c)
}

type segmentStatusResponse struct {
	Enabled bool `json:"enabled"`
	segments.Status
}

// POST /segments/run — "Analyse now": start background work — intro
// detection and seek-bar previews — straight away, whatever the schedule
// says, until everything waiting is done. A manual pause still holds it.
func (s *server) runSegments(c echo.Context) error {
	ctx := c.Request().Context()
	libs, err := s.store.Libraries(ctx)
	if err != nil {
		return err
	}
	// Queued before the go-ahead: a worker that wakes to an empty queue
	// hands itself straight back to the schedule.
	for _, lib := range libs {
		s.enqueueBackground(lib)
	}
	if s.segments != nil {
		s.segments.RunNow()
	}
	s.previews.RunNow()
	s.audit(c, "segments.run", "segments", "", "Intro detection and seek-bar previews", "started by hand")
	if s.segments == nil {
		return c.JSON(http.StatusAccepted, segmentStatusResponse{Enabled: false})
	}
	return c.JSON(http.StatusAccepted, segmentStatusResponse{Enabled: true, Status: s.segments.Status()})
}

// ---- Correcting detection by hand ----
//
// An administrator can set an episode's intro or credits, say it has none,
// or hand it back to detection; and send a whole season back to detection.
// A hand-set value is locked: re-detection leaves it alone until cleared.

// itemSegmentsResponse is an episode's segments as the admin edits them.
type itemSegmentsResponse struct {
	// Detection is false when intro detection is turned off
	// (libraries.intro_detection), so there is nothing to re-detect with.
	Detection   bool                 `json:"detection"`
	RuntimeSecs int                  `json:"runtimeSecs"`
	Segments    []store.SegmentState `json:"segments"`
}

// segmentEpisode loads the episode a segment request is about.
func (s *server) segmentEpisode(c echo.Context) (store.Item, error) {
	item, err := s.store.Item(c.Request().Context(), c.Param("id"))
	if err != nil {
		return store.Item{}, echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	if item.Kind != store.KindEpisode {
		return store.Item{}, echo.NewHTTPError(http.StatusBadRequest, "intro and credits belong to episodes")
	}
	return item, nil
}

func (s *server) itemSegmentsReply(c echo.Context, item store.Item) error {
	states, err := s.store.SegmentStates(c.Request().Context(), item.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, itemSegmentsResponse{
		Detection: s.segments != nil, RuntimeSecs: item.RuntimeSecs, Segments: states})
}

// GET /items/:id/segments — an episode's intro and credits, each with
// whether detection or a person set it.
func (s *server) itemSegments(c echo.Context) error {
	item, err := s.segmentEpisode(c)
	if err != nil {
		return err
	}
	return s.itemSegmentsReply(c, item)
}

type putSegmentRequest struct {
	// None says the episode has no segment of this kind; StartSecs and
	// EndSecs are ignored.
	None      bool    `json:"none"`
	StartSecs float64 `json:"startSecs"`
	EndSecs   float64 `json:"endSecs"`
}

// validSegment checks hand-set bounds against an episode's running time
// (when known): a second of slack for a running time rounded down.
func validSegment(req putSegmentRequest, runtimeSecs int) error {
	switch {
	case req.StartSecs < 0:
		return fmt.Errorf("the start cannot be before the beginning")
	case req.EndSecs-req.StartSecs < 1:
		return fmt.Errorf("the end must be at least a second after the start")
	case runtimeSecs > 0 && req.EndSecs > float64(runtimeSecs)+1:
		return fmt.Errorf("the end is after the episode finishes")
	}
	return nil
}

// PUT /items/:id/segments/:kind — set an episode's intro or credits by
// hand, or say it has none. Locked against re-detection until cleared.
func (s *server) putItemSegment(c echo.Context) error {
	item, err := s.segmentEpisode(c)
	if err != nil {
		return err
	}
	kind := c.Param("kind")
	if !slices.Contains(store.SegmentKinds, kind) {
		return echo.NewHTTPError(http.StatusNotFound, "no such segment kind")
	}
	var req putSegmentRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	var seg *store.Segment
	detail := "none"
	if !req.None {
		if err := validSegment(req, item.RuntimeSecs); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		seg = &store.Segment{Kind: kind, StartSecs: req.StartSecs, EndSecs: req.EndSecs}
		detail = fmt.Sprintf("%.0f-%.0f s", req.StartSecs, req.EndSecs)
	}
	if err := s.store.SetManualSegment(c.Request().Context(), item.ID, kind, seg); err != nil {
		return err
	}
	s.audit(c, "segments.set", "item", item.ID, item.Title, kind+": "+detail)
	return s.itemSegmentsReply(c, item)
}

// DELETE /items/:id/segments/:kind — hand a segment set by hand back to
// detection, which looks at the episode again on its next pass.
func (s *server) clearItemSegment(c echo.Context) error {
	item, err := s.segmentEpisode(c)
	if err != nil {
		return err
	}
	kind := c.Param("kind")
	if !slices.Contains(store.SegmentKinds, kind) {
		return echo.NewHTTPError(http.StatusNotFound, "no such segment kind")
	}
	if err := s.store.ClearManualSegment(c.Request().Context(), item.ID, kind); err != nil {
		return err
	}
	if s.segments != nil {
		s.segments.Enqueue(item.LibraryID)
	}
	s.audit(c, "segments.clear", "item", item.ID, item.Title, kind)
	return s.itemSegmentsReply(c, item)
}

type redetectResponse struct {
	// Reopened counts the analyses sent back to detection; episodes never
	// analysed, or set by hand, are not among them.
	Reopened int64 `json:"reopened"`
}

// POST /items/:id/segments/redetect — detect a season's intros and credits
// again, from the season or any of its episodes. Hand-set values are kept.
// Detection starts straight away, whatever its schedule says, but still
// gives way to playback, scans and a manual pause; what was found before
// is served until it is replaced.
func (s *server) redetectSegments(c echo.Context) error {
	ctx := c.Request().Context()
	if s.segments == nil {
		return echo.NewHTTPError(http.StatusConflict, "intro detection is turned off on this server")
	}
	item, err := s.store.Item(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	season := item
	if item.Kind == store.KindEpisode {
		if season, err = s.store.Item(ctx, item.ParentID); err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "season not found")
		}
	}
	if season.Kind != store.KindSeason {
		return echo.NewHTTPError(http.StatusBadRequest, "re-detection is by season: give a season or an episode")
	}
	n, err := s.store.RedetectSeasonSegments(ctx, season.ID)
	if err != nil {
		return err
	}
	s.segments.Enqueue(season.LibraryID)
	s.segments.RunNow()
	s.audit(c, "segments.redetect", "item", season.ID, season.Title, fmt.Sprintf("%d episodes", n))
	return c.JSON(http.StatusAccepted, redetectResponse{Reopened: n})
}
