package api

import (
	"log"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/playback"
	"github.com/garybowers/cineva/server/internal/store"
)

// audit records an admin/user action from a request. Failures are logged,
// never surfaced — the trail must not break the action it describes.
func (s *server) audit(c echo.Context, action, targetKind, targetID, targetTitle, detail string) {
	browser, osName := playback.ParseUserAgent(c.Request().UserAgent())
	client := browser
	if osName != "" {
		client += " · " + osName
	}
	e := store.AuditEntry{
		UserID: userID(c), Action: action,
		TargetKind: targetKind, TargetID: targetID, TargetTitle: targetTitle, Detail: detail,
		ClientIP: c.RealIP(), Client: client,
	}
	if err := s.store.RecordAudit(c.Request().Context(), e); err != nil {
		log.Printf("audit %s: %v", action, err)
		return
	}
	s.events.Publish(events.Event{Type: "logs"})
}

// GET /logs?limit=100&offset=0&action=scan. — the audit trail, newest first.
func (s *server) auditLog(c echo.Context) error {
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	offset, _ := strconv.Atoi(c.QueryParam("offset"))
	out, err := s.store.AuditLog(c.Request().Context(), limit, offset, c.QueryParam("action"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}
