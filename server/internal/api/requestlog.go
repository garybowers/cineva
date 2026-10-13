package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/logging"
)

var httpLog = logging.For("http")

// slowRequest is how long an ordinary request may take before it is worth
// a line at info level.
const slowRequest = time.Second

// requestLog logs what is worth knowing about requests: failures, and
// anything slow. The rest — a browser fetches dozens of things a minute —
// only at debug level.
//
// Only the path is logged, never the query string: media URLs can carry an
// API key there (?api_key=), and a log is the last place it should land.
func requestLog(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		start := time.Now()
		if err := next(c); err != nil {
			c.Error(err) // writes the response, so the status below is the real one
		}
		took := time.Since(start)
		status := c.Response().Status
		req := c.Request()

		level := slog.LevelDebug
		msg := "request"
		switch {
		case status >= 500:
			level, msg = slog.LevelError, "request failed"
		case status >= 400 && !routineMiss(status):
			level, msg = slog.LevelWarn, "request refused"
		case took >= slowRequest && !longLived(c.Path()):
			level, msg = slog.LevelInfo, "slow request"
		}
		if !httpLog.Enabled(req.Context(), level) {
			return nil
		}
		attrs := []any{"method", req.Method, "path", req.URL.Path, "status", status, "took", took}
		if he, ok := c.Get(errKey).(string); ok && he != "" {
			attrs = append(attrs, "err", he)
		}
		httpLog.Log(req.Context(), level, msg, attrs...)
		return nil
	}
}

// routineMiss is a client error that happens in normal use and says nothing
// about the server: signed out (401), a deep link or probe for something
// absent (404), a heartbeat after an admin stopped a playback (409), a
// range past the end of a file (416).
func routineMiss(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusRequestedRangeNotSatisfiable:
		return true
	}
	return false
}

// longLived routes are slow by nature — a stream, a download, the event
// connection — and their duration is not a symptom.
func longLived(route string) bool {
	for _, suffix := range []string{"/events", "/stream", "/download"} {
		if strings.HasSuffix(route, suffix) {
			return true
		}
	}
	return strings.HasPrefix(route, "/api/v1/play/")
}

// errKey carries a handler's error message from the error handler to the
// request log.
const errKey = "cineva.err"
