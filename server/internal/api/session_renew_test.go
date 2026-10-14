package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/store"
)

// Sessions slide: one near its end is renewed, cookie and all, and one
// renewed recently is left alone so requests do not each rewrite it.
func TestSessionRenewal(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(ctx, "tv", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	ttl := 30 * 24 * time.Hour
	s := &server{store: st, sessionTTL: ttl}

	for _, tc := range []struct {
		name    string
		left    time.Duration
		renewed bool
	}{
		{"two days left", 48 * time.Hour, true},
		{"used yesterday", ttl - 2*time.Hour, false},
		{"a day and a bit used", ttl - 25*time.Hour, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, hash, err := auth.NewToken()
			if err != nil {
				t.Fatal(err)
			}
			expires := time.Now().UTC().Add(tc.left).Format(sessionTimeLayout)
			if _, err := st.CreateSession(ctx, hash, u.ID, expires, "TV", "::1", ""); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			rec := httptest.NewRecorder()
			if err := s.authenticate(func(echo.Context) error { return nil })(echo.New().NewContext(req, rec)); err != nil {
				t.Fatal(err)
			}
			sess, _, err := st.SessionByHash(ctx, hash)
			if err != nil {
				t.Fatal(err)
			}
			moved := sess.ExpiresAt != expires
			cookie := false
			for _, ck := range rec.Result().Cookies() {
				cookie = cookie || ck.Name == sessionCookie
			}
			if moved != tc.renewed || cookie != tc.renewed {
				t.Fatalf("expiry moved %v, cookie set %v; want %v", moved, cookie, tc.renewed)
			}
			if tc.renewed {
				exp, _ := time.Parse(sessionTimeLayout, sess.ExpiresAt)
				if d := time.Until(exp); d < ttl-time.Minute || d > ttl+time.Minute {
					t.Fatalf("renewed to %v from now, want %v", d, ttl)
				}
			}
		})
	}
}
