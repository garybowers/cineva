package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/store"
)

// browser is a cookie jar against the API: what a TV's browser keeps.
type browser struct {
	t       *testing.T
	e       *echo.Echo
	cookies map[string]string
}

func (b *browser) do(method, path, body string) *httptest.ResponseRecorder {
	b.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range b.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rec := httptest.NewRecorder()
	b.e.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(b.cookies, ck.Name)
		} else {
			b.cookies[ck.Name] = ck.Value
		}
	}
	return rec
}

func (b *browser) signIn(user, password string) {
	b.t.Helper()
	if rec := b.do(http.MethodPost, "/api/v1/auth/login", `{"username":"`+user+`","password":"`+password+`"}`); rec.Code != http.StatusOK {
		b.t.Fatalf("sign in as %s: %d %s", user, rec.Code, rec.Body)
	}
}

// The screen is the device: signing out, another profile signing in and a
// password change all leave it the same device, with its name and room.
func TestDeviceOutlivesSessions(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"dad", "kid"} {
		hash, err := auth.HashPassword(u + "-password-1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateUser(ctx, u, "", hash, store.RoleUser); err != nil {
			t.Fatal(err)
		}
	}
	s := &server{store: st, events: events.NewBus(), presence: newPresence(), throttle: auth.NewThrottle(), sessionTTL: 30 * 24 * time.Hour}
	e := echo.New()
	s.apiRoutes(e)
	tv := &browser{t: t, e: e, cookies: map[string]string{}}

	tv.signIn("dad", "dad-password-1")
	if tv.cookies[deviceCookie] == "" {
		t.Fatal("signing in gave the browser no device cookie")
	}
	if rec := tv.do(http.MethodPatch, "/api/v1/auth/sessions/current", `{"name":"Living-room TV"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("naming: %d %s", rec.Code, rec.Body)
	}
	first, err := st.DeviceBySecret(ctx, auth.HashToken(tv.cookies[deviceCookie]))
	if err != nil {
		t.Fatal(err)
	}
	same := func(when string) {
		t.Helper()
		d, err := st.DeviceBySecret(ctx, auth.HashToken(tv.cookies[deviceCookie]))
		if err != nil || d.ID != first.ID || d.Name != "Living-room TV" {
			t.Fatalf("%s: device %+v (%v), want %s named Living-room TV", when, d, err, first.ID)
		}
	}

	// Signing out and back in.
	if rec := tv.do(http.MethodPost, "/api/v1/auth/logout", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("sign out: %d", rec.Code)
	}
	same("after signing out")
	tv.signIn("dad", "dad-password-1")
	same("after signing back in")

	// Another profile on the same screen replaces Dad's session there.
	dadToken := tv.cookies[sessionCookie]
	tv.signIn("kid", "kid-password-1")
	same("after the kid signs in")
	if _, _, err := st.SessionByHash(ctx, auth.HashToken(dadToken)); err == nil {
		t.Fatal("Dad is still signed in on the TV after the kid chose themselves")
	}

	// A password change signs everything out and this screen back in.
	if rec := tv.do(http.MethodPost, "/api/v1/auth/password", `{"current":"kid-password-1","new":"kid-password-2"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("password change: %d %s", rec.Code, rec.Body)
	}
	same("after a password change")
	if rec := tv.do(http.MethodGet, "/api/v1/auth/me", ""); rec.Code != http.StatusOK {
		t.Fatalf("still signed in after the password change? %d", rec.Code)
	}

	// A browser signed in before devices existed has no device cookie; its
	// next request gets one for the device its session is on.
	delete(tv.cookies, deviceCookie)
	if rec := tv.do(http.MethodGet, "/api/v1/auth/me", ""); rec.Code != http.StatusOK {
		t.Fatalf("me: %d", rec.Code)
	}
	same("after its device cookie was lost")

	// Forgetting it signs it out and removes it.
	if rec := tv.do(http.MethodDelete, "/api/v1/auth/sessions/"+first.ID+"?forget=true", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("forget: %d %s", rec.Code, rec.Body)
	}
	if _, err := st.Device(ctx, first.ID); err == nil {
		t.Fatal("the device was not forgotten")
	}
}

// A device an admin sets to sign in by itself becomes that user whenever
// nobody is signed in on it, and anyone can still switch to themselves.
func TestDeviceSignsInByItself(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, u := range []struct{ name, role string }{{"gary", store.RoleAdmin}, {"family", store.RoleUser}, {"dad", store.RoleUser}} {
		hash, err := auth.HashPassword(u.name + "-password-1")
		if err != nil {
			t.Fatal(err)
		}
		created, err := st.CreateUser(ctx, u.name, "", hash, u.role)
		if err != nil {
			t.Fatal(err)
		}
		ids[u.name] = created.ID
	}
	s := &server{store: st, events: events.NewBus(), presence: newPresence(), throttle: auth.NewThrottle(), sessionTTL: 30 * 24 * time.Hour}
	e := echo.New()
	s.apiRoutes(e)
	admin := &browser{t: t, e: e, cookies: map[string]string{}}
	admin.signIn("gary", "gary-password-1")
	tv := &browser{t: t, e: e, cookies: map[string]string{}}
	tv.signIn("family", "family-password-1")
	d, err := st.DeviceBySecret(ctx, auth.HashToken(tv.cookies[deviceCookie]))
	if err != nil {
		t.Fatal(err)
	}
	set := func(user string) int {
		return admin.do(http.MethodPatch, "/api/v1/devices/"+d.ID, `{"autoUserId":"`+ids[user]+`"}`).Code
	}
	if code := set("gary"); code != http.StatusBadRequest {
		t.Fatalf("an admin as the TV's user: %d, want 400", code)
	}
	if code := set("family"); code != http.StatusOK {
		t.Fatalf("setting the TV's user: %d", code)
	}
	whoOpens := func() (string, string) {
		t.Helper()
		rec := tv.do(http.MethodGet, "/api/v1/auth/status", "")
		var st struct {
			User     *struct{ Username string } `json:"user"`
			AutoUser *struct{ ID string }       `json:"autoUser"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		who, auto := "", ""
		if st.User != nil {
			who = st.User.Username
		}
		if st.AutoUser != nil {
			auto = st.AutoUser.ID
		}
		return who, auto
	}

	// Signed out, the TV signs itself straight back in as the family.
	tv.do(http.MethodPost, "/api/v1/auth/logout", "")
	if who, auto := whoOpens(); who != "family" || auto != ids["family"] {
		t.Fatalf("after signing out, the TV opened as %q (auto %q), want the family", who, auto)
	}
	if rec := tv.do(http.MethodGet, "/api/v1/auth/me", ""); rec.Code != http.StatusOK {
		t.Fatalf("the automatic sign-in gave no working session: %d", rec.Code)
	}

	// Dad switches to himself; while he is on it, it stays his.
	tv.do(http.MethodPost, "/api/v1/auth/logout", "")
	tv.signIn("dad", "dad-password-1")
	if who, _ := whoOpens(); who != "dad" {
		t.Fatalf("with Dad signed in, the TV opened as %q", who)
	}

	// Turned off, a signed-out TV shows the sign-in screen again.
	if code := admin.do(http.MethodPatch, "/api/v1/devices/"+d.ID, `{"autoUserId":""}`).Code; code != http.StatusOK {
		t.Fatalf("turning it off: %d", code)
	}
	tv.do(http.MethodPost, "/api/v1/auth/logout", "")
	if who, auto := whoOpens(); who != "" || auto != "" {
		t.Fatalf("with no automatic user the TV opened as %q (auto %q)", who, auto)
	}

	// A browser that is not that device never gets the family's account.
	stranger := &browser{t: t, e: e, cookies: map[string]string{}}
	set("family")
	rec := stranger.do(http.MethodGet, "/api/v1/auth/status", "")
	if strings.Contains(rec.Body.String(), `"user"`) {
		t.Fatalf("a browser with no device cookie was signed in: %s", rec.Body)
	}
}
