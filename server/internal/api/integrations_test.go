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

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/store"
)

// The webhook is public, so its own check is all that stands between the
// network and a rescan.
func TestIntegrationWebhookAuth(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	secret, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := st.CreateArrConnection(ctx, store.ArrConnection{
		Kind: "sonarr", Name: "Sonarr", URL: "http://sonarr:8989", APIKey: "k",
		CallbackURL: "http://cineva:4747", SecretHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{store: st, manager: library.NewManager(st, nil, t.TempDir())}

	for _, tc := range []struct {
		name       string
		id         string
		user, pass string
		body       string
		want       int
	}{
		{"test event", conn.ID, webhookUser, secret, `{"eventType":"Test"}`, http.StatusOK},
		{"download, no TV library", conn.ID, webhookUser, secret, `{"eventType":"Download","series":{"path":"/tv/x"}}`, http.StatusAccepted},
		{"wrong secret", conn.ID, webhookUser, "guess", `{"eventType":"Test"}`, http.StatusUnauthorized},
		{"wrong user", conn.ID, "admin", secret, `{"eventType":"Test"}`, http.StatusUnauthorized},
		{"no credentials", conn.ID, "", "", `{"eventType":"Test"}`, http.StatusUnauthorized},
		{"unknown connection", "nope", webhookUser, secret, `{"eventType":"Test"}`, http.StatusNotFound},
		{"not json", conn.ID, webhookUser, secret, `<xml/>`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			if tc.user != "" {
				req.SetBasicAuth(tc.user, tc.pass)
			}
			rec := httptest.NewRecorder()
			c := echo.New().NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(tc.id)
			var code int
			if err := s.integrationWebhook(c); err != nil {
				var he *echo.HTTPError
				if !errors.As(err, &he) {
					t.Fatalf("unexpected error: %v", err)
				}
				code = he.Code
			} else {
				code = rec.Code
			}
			if code != tc.want {
				t.Fatalf("status = %d, want %d", code, tc.want)
			}
		})
	}
}
