package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/playback"
	"github.com/garybowers/cineva/server/internal/store"
)

var (
	auditLog = logging.For("audit")
	authLog  = logging.For("auth")
)

// auditMessages are the console wording for audit actions worth reading at
// a glance; anything else is logged under its action name.
var auditMessages = map[string]string{
	"login":          "signed in",
	"login.failed":   "sign-in failed",
	"logout":         "signed out",
	"session.revoke": "device signed out",
	"user.password":  "password changed",
	"apikey.create":  "API key created",
	"apikey.delete":  "API key revoked",
	"user.create":    "user created",
	"user.update":    "user changed",
	"user.delete":    "user deleted",
	"scan.start":     "scan started",
	"scan.cancel":    "scan cancelled",

	"request.create":      "title requested",
	"request.approve":     "request approved",
	"request.failed":      "request could not be sent",
	"request.decline":     "request declined",
	"request.delete":      "request withdrawn",
	"request.search":      "searched for missing episodes",
	"integration.options": "integration options changed",
	"prefs.defaults":      "preference defaults changed",
	"group.order":         "group priority changed",
}

// logAudit puts an audit entry on the console as well as in the trail: who
// signed in, from where, and what an admin changed are the things an
// operator watching the server wants to see happen. A failed sign-in is a
// warning — a typo, or someone trying passwords.
func logAudit(c echo.Context, e store.AuditEntry, actor string) {
	logger := auditLog
	if strings.HasPrefix(e.Action, "login") || strings.HasPrefix(e.Action, "logout") ||
		strings.HasPrefix(e.Action, "session.") || strings.HasPrefix(e.Action, "apikey.") || e.Action == "user.password" {
		logger = authLog
	}
	msg, ok := auditMessages[e.Action]
	if !ok {
		msg = e.Action
	}
	level := slog.LevelInfo
	if e.Action == "login.failed" {
		level = slog.LevelWarn
	}
	attrs := []any{}
	if actor != "" {
		attrs = append(attrs, "user", actor)
	}
	if e.TargetTitle != "" && e.TargetTitle != actor {
		attrs = append(attrs, e.TargetKind, e.TargetTitle)
	}
	if e.Detail != "" {
		attrs = append(attrs, "detail", e.Detail)
	}
	attrs = append(attrs, "ip", e.ClientIP, "client", e.Client)
	logger.Log(c.Request().Context(), level, msg, attrs...)
}

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
	// The person acting; for a sign-in nobody is signed in yet, so it is the
	// account being signed in to.
	actor := currentUser(c).Username
	if actor == "" && e.TargetKind == "user" {
		actor = e.TargetTitle
	}
	logAudit(c, e, actor)
	if err := s.store.RecordAudit(c.Request().Context(), e); err != nil {
		auditLog.Error("recording audit entry failed", "action", action, "err", err)
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
