package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/store"
)

// Sign-ins reach the console, with where they came from; a failed one is a
// warning.
func TestLogAudit(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(logging.NewPretty(&buf, slog.LevelInfo, false)))
	c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), httptest.NewRecorder())

	for _, tc := range []struct {
		entry store.AuditEntry
		actor string
		want  []string
	}{
		{store.AuditEntry{Action: "login", TargetKind: "user", TargetTitle: "gary", ClientIP: "10.0.0.5", Client: "Firefox · Linux"},
			"gary", []string{"INFO  auth", "signed in", "user=gary", "ip=10.0.0.5", `client="Firefox · Linux"`}},
		{store.AuditEntry{Action: "login.failed", TargetKind: "user", TargetTitle: "gary", Detail: "locked for 30s", ClientIP: "203.0.113.9"},
			"gary", []string{"WARN  auth", "sign-in failed", "user=gary", `detail="locked for 30s"`, "ip=203.0.113.9"}},
		{store.AuditEntry{Action: "settings.update", TargetKind: "settings", TargetTitle: "Detection schedule", Detail: "window 02:00-06:00"},
			"gary", []string{"INFO  audit", "settings.update", "user=gary", `settings="Detection schedule"`}},
	} {
		buf.Reset()
		logAudit(c, tc.entry, tc.actor)
		for _, w := range tc.want {
			if !strings.Contains(buf.String(), w) {
				t.Errorf("%s: line %q lacks %q", tc.entry.Action, buf.String(), w)
			}
		}
	}
}
