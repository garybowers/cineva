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

// POST /segments/pause and /segments/resume — hold detection, or let it carry
// on. Kept across restarts, so a pause means "until I say", not "until the
// server next starts".
func (s *server) pauseSegments(c echo.Context) error  { return s.setSegmentsPaused(c, true) }
func (s *server) resumeSegments(c echo.Context) error { return s.setSegmentsPaused(c, false) }

func (s *server) setSegmentsPaused(c echo.Context, paused bool) error {
	if s.segments == nil {
		return echo.NewHTTPError(http.StatusConflict, "intro detection is turned off (libraries.intro_detection)")
	}
	v := "false"
	if paused {
		v = "true"
	}
	if err := s.store.SetSetting(c.Request().Context(), settingDetectionPaused, v); err != nil {
		return err
	}
	s.segments.SetPaused(paused)
	action := "segments.resume"
	if paused {
		action = "segments.pause"
	}
	s.audit(c, action, "settings", settingDetectionPaused, "Intro & credits detection", "")
	return s.segmentStatus(c)
}

type segmentStatusResponse struct {
	Enabled bool `json:"enabled"`
	segments.Status
}
