package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/homeassistant"
	"github.com/garybowers/cineva/server/internal/notify"
	"github.com/garybowers/cineva/server/internal/store"
)

func TestNotificationDestinations(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct{ name, role string }{{"gary", store.RoleAdmin}, {"kid", store.RoleUser}} {
		hash, _ := auth.HashPassword(u.name + "-password-1")
		if _, err := st.CreateUser(ctx, u.name, "", hash, u.role); err != nil {
			t.Fatal(err)
		}
	}
	s := &server{store: st, events: events.NewBus(), presence: newPresence(), throttle: auth.NewThrottle(),
		sessionTTL: 30 * 24 * time.Hour, notify: notify.New(st), ha: homeassistant.New(st)}
	e := echo.New()
	s.apiRoutes(e)
	admin := &browser{t: t, e: e, cookies: map[string]string{}}
	admin.signIn("gary", "gary-password-1")
	kid := &browser{t: t, e: e, cookies: map[string]string{}}
	kid.signIn("kid", "kid-password-1")

	// The admin's household Telegram: the token comes back masked, and a
	// masked token sent back with an edit keeps the real one.
	rec := admin.do(http.MethodPost, "/api/v1/notifications/destinations",
		`{"kind":"telegram","name":"Family","config":{"botToken":"123:SECRETTOKEN","chatId":"-100"},"events":["library.new"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var made struct {
		ID     string            `json:"id"`
		Config map[string]string `json:"config"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &made)
	if strings.Contains(rec.Body.String(), "SECRETTOKEN") || made.Config["botToken"] != "••••OKEN" {
		t.Fatalf("token not masked: %s", rec.Body)
	}
	if rec := admin.do(http.MethodPatch, "/api/v1/notifications/destinations/"+made.ID,
		`{"config":{"botToken":"••••OKEN","chatId":"-200"}}`); rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body)
	}
	if d, _ := st.NotifyDestination(ctx, made.ID); d.Config["botToken"] != "123:SECRETTOKEN" || d.Config["chatId"] != "-200" {
		t.Fatalf("saved config = %v", d.Config)
	}

	// The kid may not reach the household's, nor point the server at the
	// network, nor use ntfy until the admin chooses a server.
	if rec := kid.do(http.MethodDelete, "/api/v1/auth/me/notifications/"+made.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("kid deleting the household's: %d", rec.Code)
	}
	if rec := kid.do(http.MethodGet, "/api/v1/notifications", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("kid reading household destinations: %d", rec.Code)
	}
	for _, body := range []string{
		`{"kind":"webhook","name":"Probe","config":{"url":"http://router.lan/"}}`,
		`{"kind":"ntfy","name":"Phone","config":{"topic":"kid-cineva"}}`,
		`{"kind":"discord","name":"Mine","config":{"webhookUrl":"https://discord.com/api/webhooks/1/a"},"events":["storage.down"]}`,
	} {
		if rec := kid.do(http.MethodPost, "/api/v1/auth/me/notifications", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("kid %s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if rec := admin.do(http.MethodPut, "/api/v1/notifications/settings", `{"publicUrl":"https://cineva.example/","ntfyServer":"https://ntfy.sh"}`); rec.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	rec = kid.do(http.MethodPost, "/api/v1/auth/me/notifications", `{"kind":"ntfy","name":"Phone","config":{"server":"http://10.0.0.1","topic":"kid-cineva"},"events":["episode.new"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("kid's ntfy: %d %s", rec.Code, rec.Body)
	}
	var mine struct {
		Config map[string]string `json:"config"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &mine)
	if mine.Config["server"] != "https://ntfy.sh" {
		t.Fatalf("kid's ntfy server = %q, want the admin's", mine.Config["server"])
	}
	rec = kid.do(http.MethodGet, "/api/v1/auth/me/notifications", "")
	if !strings.Contains(rec.Body.String(), `"kid-cineva"`) || strings.Contains(rec.Body.String(), "SECRETTOKEN") {
		t.Fatalf("kid's list: %s", rec.Body)
	}
}

func TestEpisodeCodes(t *testing.T) {
	ep := func(s, e int) store.Item { return store.Item{SeasonNum: s, EpisodeNum: e} }
	for _, tc := range []struct {
		eps  []store.Item
		want string
	}{
		{[]store.Item{ep(6, 5)}, "S06E05"},
		{[]store.Item{ep(6, 7), ep(6, 5), ep(6, 6)}, "S06E05-E07"},
		{[]store.Item{ep(6, 5), ep(6, 9)}, "2 new episodes"},
		{[]store.Item{ep(1, 10), ep(2, 1)}, "2 new episodes"},
	} {
		if got := episodeCodes(tc.eps); got != tc.want {
			t.Errorf("episodeCodes = %q, want %q", got, tc.want)
		}
	}
}
