package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/segments"
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
