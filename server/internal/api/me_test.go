package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/store"
)

// Renaming yourself changes the name shown and nothing else.
func TestUpdateMe(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(ctx, "gary", "Gary", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{store: st, events: events.NewBus()}

	for _, tc := range []struct {
		name, body string
		code       int
		want       string // the stored name afterwards
	}{
		{"renamed, trimmed", `{"displayName":"  Dad  "}`, http.StatusOK, "Dad"},
		{"empty", `{"displayName":"   "}`, http.StatusBadRequest, "Dad"},
		{"too long", `{"displayName":"` + strings.Repeat("é", maxDisplayName+1) + `"}`, http.StatusBadRequest, "Dad"},
		{"at the limit", `{"displayName":"` + strings.Repeat("é", maxDisplayName) + `"}`, http.StatusOK, strings.Repeat("é", maxDisplayName)},
		{"a role in the body is ignored", `{"displayName":"Gary","role":"admin"}`, http.StatusOK, "Gary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			c := echo.New().NewContext(req, httptest.NewRecorder())
			c.Set(ctxUser, u)
			code := http.StatusOK
			if err := s.updateMe(c); err != nil {
				var he *echo.HTTPError
				if !errors.As(err, &he) {
					t.Fatalf("unexpected error: %v", err)
				}
				code = he.Code
			}
			if code != tc.code {
				t.Fatalf("status %d, want %d", code, tc.code)
			}
			got, err := st.UserByID(ctx, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.DisplayName != tc.want || got.Role != store.RoleUser {
				t.Fatalf("user = %q (%s), want %q (user)", got.DisplayName, got.Role, tc.want)
			}
		})
	}
}
