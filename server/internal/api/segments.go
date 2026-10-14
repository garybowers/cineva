package api

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
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
// An administrator or a trusted editor (store.User.SegmentEditor) can set an
// episode's intro or credits, or a film's credits, say there are none, or
// hand them back to detection; and send any episode, season, series, film
// or a selection of them back to detection. A hand-set value is locked:
// re-detection leaves it alone until cleared, unless asked to replace it.

// requireSegmentEditor lets administrators and trusted editors through.
// Handlers behind it still check the caller can see the title concerned.
func requireSegmentEditor(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !currentUser(c).CanEditSegments() {
			return echo.NewHTTPError(http.StatusForbidden, "only administrators and trusted editors can change intro and credits times")
		}
		return next(c)
	}
}

// itemSegmentsResponse is an item's segments as an editor sees them.
type itemSegmentsResponse struct {
	// Detection is false when intro detection is turned off
	// (libraries.intro_detection), so there is nothing to re-detect with.
	Detection   bool                 `json:"detection"`
	RuntimeSecs int                  `json:"runtimeSecs"`
	Segments    []store.SegmentState `json:"segments"`
}

// segmentKindsFor lists the kinds of segment an item can have: a film's
// opening is a studio logo, not worth skipping, so it has credits alone.
func segmentKindsFor(item store.Item) []string {
	if item.Kind == store.KindMovie {
		return []string{store.SegmentCredits}
	}
	return store.SegmentKinds
}

// segmentItem loads the episode or film a segment request is about, which
// the caller must be able to see.
func (s *server) segmentItem(c echo.Context) (store.Item, error) {
	item, err := s.store.Item(c.Request().Context(), c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), item.LibraryID) {
		return store.Item{}, echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	if item.Kind != store.KindEpisode && item.Kind != store.KindMovie {
		return store.Item{}, echo.NewHTTPError(http.StatusBadRequest, "intro and credits belong to episodes and films")
	}
	return item, nil
}

// segmentKind reads the :kind parameter, which must be one the item has.
func segmentKind(c echo.Context, item store.Item) (string, error) {
	kind := c.Param("kind")
	if !slices.Contains(store.SegmentKinds, kind) {
		return "", echo.NewHTTPError(http.StatusNotFound, "no such segment kind")
	}
	if !slices.Contains(segmentKindsFor(item), kind) {
		return "", echo.NewHTTPError(http.StatusBadRequest, "a film has end credits only")
	}
	return kind, nil
}

func (s *server) itemSegmentsReply(c echo.Context, item store.Item) error {
	states, err := s.store.SegmentStates(c.Request().Context(), item.ID)
	if err != nil {
		return err
	}
	kinds := segmentKindsFor(item)
	states = slices.DeleteFunc(states, func(st store.SegmentState) bool { return !slices.Contains(kinds, st.Kind) })
	return c.JSON(http.StatusOK, itemSegmentsResponse{
		Detection: s.segments != nil, RuntimeSecs: item.RuntimeSecs, Segments: states})
}

// GET /items/:id/segments — an episode's intro and credits, or a film's
// credits, each with whether detection or a person set it.
func (s *server) itemSegments(c echo.Context) error {
	item, err := s.segmentItem(c)
	if err != nil {
		return err
	}
	return s.itemSegmentsReply(c, item)
}

type putSegmentRequest struct {
	// None says the item has no segment of this kind; StartSecs and
	// EndSecs are ignored.
	None      bool    `json:"none"`
	StartSecs float64 `json:"startSecs"`
	EndSecs   float64 `json:"endSecs"`
}

// validSegment checks hand-set bounds against an item's running time (when
// known): a second of slack for a running time rounded down.
func validSegment(req putSegmentRequest, runtimeSecs int) error {
	switch {
	case req.StartSecs < 0:
		return fmt.Errorf("the start cannot be before the beginning")
	case req.EndSecs-req.StartSecs < 1:
		return fmt.Errorf("the end must be at least a second after the start")
	case runtimeSecs > 0 && req.EndSecs > float64(runtimeSecs)+1:
		return fmt.Errorf("the end is after the item finishes")
	}
	return nil
}

// PUT /items/:id/segments/:kind — set an item's intro or credits by hand,
// or say it has none. Locked against re-detection until cleared.
func (s *server) putItemSegment(c echo.Context) error {
	item, err := s.segmentItem(c)
	if err != nil {
		return err
	}
	kind, err := segmentKind(c, item)
	if err != nil {
		return err
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
// detection, which looks at the item again on its next pass.
func (s *server) clearItemSegment(c echo.Context) error {
	item, err := s.segmentItem(c)
	if err != nil {
		return err
	}
	kind, err := segmentKind(c, item)
	if err != nil {
		return err
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

// redetectRequest is the body of a re-detection; every field is optional
// for one item or a library.
type redetectRequest struct {
	// Items is the selection, for POST /segments/redetect.
	Items []string `json:"items"`
	// Kinds limits it to intro or credits; also ?kinds=intro,credits.
	Kinds []string `json:"kinds"`
	// IncludeLocked replaces times set by hand as well; also
	// ?includeLocked=true.
	IncludeLocked bool `json:"includeLocked"`
}

// redetectOptions reads what a re-detection covers from its body and query.
func redetectOptions(c echo.Context) (redetectRequest, store.RedetectOptions, error) {
	var req redetectRequest
	if err := c.Bind(&req); err != nil {
		return req, store.RedetectOptions{}, echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if q := c.QueryParam("kinds"); q != "" {
		req.Kinds = strings.Split(q, ",")
	}
	if c.QueryParam("includeLocked") == "true" {
		req.IncludeLocked = true
	}
	for _, k := range req.Kinds {
		if !slices.Contains(store.SegmentKinds, k) {
			return req, store.RedetectOptions{}, echo.NewHTTPError(http.StatusBadRequest, "kinds must be intro, credits or both")
		}
	}
	return req, store.RedetectOptions{Kinds: req.Kinds, IncludeLocked: req.IncludeLocked}, nil
}

// redetectable is whether an item can be sent back to detection: an
// episode, season, series or film.
func redetectable(item store.Item) bool {
	switch item.Kind {
	case store.KindEpisode, store.KindSeason, store.KindSeries, store.KindMovie:
		return true
	}
	return false
}

// redetectDetail describes a re-detection for the audit log.
func redetectDetail(res store.RedetectResult, opts store.RedetectOptions) string {
	d := fmt.Sprintf("%d items · %d reopened", res.Items, res.Reopened)
	if len(opts.Kinds) > 0 {
		d += " · " + strings.Join(opts.Kinds, ", ")
	}
	if opts.IncludeLocked {
		d += " · hand-set times replaced"
	} else if res.Locked > 0 {
		d += fmt.Sprintf(" · %d set by hand kept", res.Locked)
	}
	return d
}

// startTargeted reopens items' analyses and puts their seasons and films at
// the front of detection's queue, ahead of every library and whatever the
// schedule says; it still gives way to playback, scans and a manual pause.
// What was found before is served until it is replaced.
func (s *server) startTargeted(c echo.Context, ids []string, opts store.RedetectOptions) (store.RedetectResult, error) {
	ctx := c.Request().Context()
	res, err := s.store.RedetectItemSegments(ctx, ids, opts)
	if err != nil {
		return res, err
	}
	s.segments.Prioritise(ctx, res.Seasons, res.Films)
	return res, nil
}

func (s *server) detectionOn() error {
	if s.segments == nil {
		return echo.NewHTTPError(http.StatusConflict, "intro detection is turned off on this server")
	}
	return nil
}

// POST /items/:id/segments/redetect — detect an episode, season, series or
// film again. An episode is analysed alone, still compared with the rest
// of its season; a film's credits are read from its chapters again.
func (s *server) redetectSegments(c echo.Context) error {
	if err := s.detectionOn(); err != nil {
		return err
	}
	item, err := s.store.Item(c.Request().Context(), c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), item.LibraryID) {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	if !redetectable(item) {
		return echo.NewHTTPError(http.StatusBadRequest, "intro and credits are detected for episodes, seasons, series and films")
	}
	_, opts, err := redetectOptions(c)
	if err != nil {
		return err
	}
	res, err := s.startTargeted(c, []string{item.ID}, opts)
	if err != nil {
		return err
	}
	s.audit(c, "segments.redetect", "item", item.ID, item.Title, redetectDetail(res, opts))
	return c.JSON(http.StatusAccepted, res)
}

// POST /segments/redetect — detect a selection again: { items: [...] }, any
// mix of episodes, seasons, series and films.
func (s *server) redetectSelection(c echo.Context) error {
	if err := s.detectionOn(); err != nil {
		return err
	}
	req, opts, err := redetectOptions(c)
	if err != nil {
		return err
	}
	if len(req.Items) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "items is required")
	}
	if len(req.Items) > store.MaxRedetectItems {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("at most %d items at once", store.MaxRedetectItems))
	}
	ctx := c.Request().Context()
	me := currentUser(c)
	for _, id := range req.Items {
		item, err := s.store.Item(ctx, id)
		if err != nil || !canSeeLibrary(me, item.LibraryID) {
			return echo.NewHTTPError(http.StatusNotFound, "item not found: "+id)
		}
		if !redetectable(item) {
			return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("%q is not an episode, season, series or film", item.Title))
		}
	}
	res, err := s.startTargeted(c, req.Items, opts)
	if err != nil {
		return err
	}
	s.audit(c, "segments.redetect", "segments", "", fmt.Sprintf("%d selected", len(req.Items)), redetectDetail(res, opts))
	return c.JSON(http.StatusAccepted, res)
}

// POST /libraries/:id/segments/redetect — detect a whole library again
// (admin only). It goes through the library queue rather than ahead of
// it, starting straight away whatever the schedule says, as "Analyse now"
// does.
func (s *server) redetectLibrary(c echo.Context) error {
	if err := s.detectionOn(); err != nil {
		return err
	}
	ctx := c.Request().Context()
	lib, err := s.store.Library(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	if lib.MediaType != store.MediaTV && lib.MediaType != store.MediaMovies {
		return echo.NewHTTPError(http.StatusBadRequest, "intro and credits are detected in film and TV libraries")
	}
	_, opts, err := redetectOptions(c)
	if err != nil {
		return err
	}
	res, err := s.store.RedetectLibrarySegments(ctx, lib.ID, opts)
	if err != nil {
		return err
	}
	s.segments.Enqueue(lib.ID)
	s.segments.RunNow()
	s.audit(c, "segments.redetect", "library", lib.ID, lib.Name, redetectDetail(res, opts))
	return c.JSON(http.StatusAccepted, res)
}

// ---- Reports of wrong times ----

type segmentReportRequest struct {
	Kind         string  `json:"kind"`
	PositionSecs float64 `json:"positionSecs"`
	Note         string  `json:"note"`
}

// maxReportNote bounds a report's note, in characters.
const maxReportNote = 500

// POST /items/:id/segments/reports — anyone who can see an episode or film
// can say its intro or credits are wrong, from the player.
func (s *server) createSegmentReport(c echo.Context) error {
	item, err := s.segmentItem(c)
	if err != nil {
		return err
	}
	var req segmentReportRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if !slices.Contains(segmentKindsFor(item), req.Kind) {
		return echo.NewHTTPError(http.StatusBadRequest, "kind must be intro or credits (a film has credits only)")
	}
	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > maxReportNote {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("the note can be at most %d characters", maxReportNote))
	}
	r, err := s.store.CreateSegmentReport(c.Request().Context(), store.SegmentReport{
		ItemID: item.ID, UserID: userID(c), Kind: req.Kind, PositionSecs: max(0, req.PositionSecs), Note: note})
	if err != nil {
		return err
	}
	s.events.Publish(events.Event{Type: "segmentReports"})
	return c.JSON(http.StatusCreated, r)
}

// GET /segments/reports — reports of wrong times, newest first: the open
// ones, or with ?all=true the resolved ones too (admin).
func (s *server) listSegmentReports(c echo.Context) error {
	reports, err := s.store.SegmentReports(c.Request().Context(), c.QueryParam("all") != "true", 200)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, reports)
}

// POST /segments/reports/:id/resolve — close a report (admin).
func (s *server) resolveSegmentReport(c echo.Context) error {
	ctx := c.Request().Context()
	r, err := s.store.SegmentReport(ctx, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "report not found")
	}
	if err != nil {
		return err
	}
	if err := s.store.ResolveSegmentReport(ctx, r.ID, userID(c)); err != nil {
		return err
	}
	s.audit(c, "segments.report.resolve", "item", r.ItemID, r.Title, r.Kind+" reported by "+r.UserName)
	s.events.Publish(events.Event{Type: "segmentReports"})
	return c.NoContent(http.StatusNoContent)
}
