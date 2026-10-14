package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/prefs"
	"github.com/garybowers/cineva/server/internal/store"
)

// prefsFixture is a server with an admin, a child in the Kids group and a
// grown-up, each signed in on one browser, behind the real routes and
// middleware so permissions are tested as they are served.
type prefsFixture struct {
	t       *testing.T
	e       *echo.Echo
	st      *store.Store
	kids    store.UserGroup
	cookies map[string]string // username -> session token
	devices map[string]string // username -> session public id
}

func newPrefsFixture(t *testing.T) *prefsFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f := &prefsFixture{t: t, st: st, cookies: map[string]string{}, devices: map[string]string{}}
	ids := map[string]string{}
	for _, u := range []struct{ name, role string }{{"admin", store.RoleAdmin}, {"kid", store.RoleUser}, {"mum", store.RoleUser}} {
		user, err := st.CreateUser(ctx, u.name, "", "h", u.role)
		if err != nil {
			t.Fatal(err)
		}
		ids[u.name] = user.ID
		token, hash, err := auth.NewToken()
		if err != nil {
			t.Fatal(err)
		}
		sess, err := st.CreateSession(ctx, hash, user.ID, "2099-01-01 00:00:00", "Firefox", "::1", "")
		if err != nil {
			t.Fatal(err)
		}
		f.cookies[u.name], f.devices[u.name] = token, sess.ID
	}
	if f.kids, err = st.CreateGroup(ctx, "Kids", []string{ids["kid"]}); err != nil {
		t.Fatal(err)
	}
	s := &server{store: st, events: events.NewBus(), prefs: prefs.NewService(st)}
	f.e = echo.New()
	s.apiRoutes(f.e)
	return f
}

// do sends a request as a user and decodes a JSON answer into out.
func (f *prefsFixture) do(user, method, path, body string, out any) int {
	f.t.Helper()
	req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.cookies[user]})
	rec := httptest.NewRecorder()
	f.e.ServeHTTP(rec, req)
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			f.t.Fatalf("%s %s: decoding %s: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code
}

func TestPrefsPermissions(t *testing.T) {
	f := newPrefsFixture(t)
	for _, tc := range []struct {
		name, user, method, path, body string
		want                           int
	}{
		{"a user cannot read the defaults", "kid", http.MethodGet, "/prefs/defaults", "", http.StatusForbidden},
		{"a user cannot set the defaults", "kid", http.MethodPut, "/prefs/defaults", `{"autoplay":{"value":"off"}}`, http.StatusForbidden},
		{"a user cannot set a group's defaults", "kid", http.MethodPut, "/groups/" + f.kids.ID + "/prefs", `{"autoplay":{"value":"on"}}`, http.StatusForbidden},
		{"a user cannot order groups", "kid", http.MethodPut, "/groups/order", `{"groupIds":[]}`, http.StatusForbidden},
		{"an admin can read the defaults", "admin", http.MethodGet, "/prefs/defaults", "", http.StatusOK},
		{"an unknown group", "admin", http.MethodPut, "/groups/nope/prefs", `{"autoplay":{"value":"on"}}`, http.StatusNotFound},
		{"a default for an unknown key", "admin", http.MethodPut, "/prefs/defaults", `{"whatever":{"value":"x"}}`, http.StatusBadRequest},
		{"a default of the wrong type", "admin", http.MethodPut, "/prefs/defaults", `{"autoplay":{"value":"maybe"}}`, http.StatusBadRequest},
		{"an order missing a group", "admin", http.MethodPut, "/groups/order", `{"groupIds":[]}`, http.StatusBadRequest},
		{"another user's device", "kid", http.MethodPut, "/auth/sessions/" + f.devices["mum"] + "/prefs", `{"quality":"480p"}`, http.StatusNotFound},
		{"reading another user's device", "kid", http.MethodGet, "/auth/me/prefs?device=" + f.devices["mum"], "", http.StatusNotFound},
		{"a person key on a device", "kid", http.MethodPut, "/auth/sessions/current/prefs", `{"sub-mode":"always"}`, http.StatusBadRequest},
		{"a device key on this device", "kid", http.MethodPut, "/auth/sessions/current/prefs", `{"quality":"480p"}`, http.StatusOK},
		{"an invalid value of your own", "kid", http.MethodPut, "/auth/me/prefs", `{"skip-intro":"sometimes"}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := f.do(tc.user, tc.method, tc.path, tc.body, nil); got != tc.want {
				t.Errorf("status %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPrefsLocksAndResolution(t *testing.T) {
	f := newPrefsFixture(t)

	// The admin sets English audio for everyone, and locks subtitles on for
	// Kids.
	if code := f.do("admin", http.MethodPut, "/prefs/defaults", `{"audio-lang":{"value":"en"}}`, nil); code != http.StatusOK {
		t.Fatalf("server defaults: %d", code)
	}
	var group map[string]store.PrefSetting
	if code := f.do("admin", http.MethodPut, "/groups/"+f.kids.ID+"/prefs", `{"sub-mode":{"value":"always","locked":true}}`, &group); code != http.StatusOK {
		t.Fatalf("group defaults: %d", code)
	}
	if !group["sub-mode"].Locked {
		t.Errorf("group defaults = %+v", group)
	}

	// The kid cannot turn subtitles off, and is told why.
	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/me/prefs", strings.NewReader(`{"sub-mode":"off"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.cookies["kid"]})
	rec := httptest.NewRecorder()
	f.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("setting a locked key: %d %s", rec.Code, rec.Body.String())
	}
	var lock struct {
		Message, Key, Source, GroupName string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lock); err != nil {
		t.Fatal(err)
	}
	if lock.Key != "sub-mode" || lock.Source != "group" || lock.GroupName != "Kids" || !strings.Contains(lock.Message, "administrator") {
		t.Errorf("409 body = %+v", lock)
	}

	// Mum, not in Kids, can.
	if code := f.do("mum", http.MethodPut, "/auth/me/prefs", `{"sub-mode":"off","audio-lang":""}`, nil); code != http.StatusOK {
		t.Fatalf("mum: %d", code)
	}

	// The legacy route still writes the raw layer, and the lock still wins.
	if code := f.do("kid", http.MethodPut, "/auth/prefs", `{"sub-mode":"off"}`, nil); code != http.StatusOK {
		t.Fatalf("legacy put: %d", code)
	}

	resolved := func(user string) myPrefs {
		var out myPrefs
		if code := f.do(user, http.MethodGet, "/auth/me/prefs", "", &out); code != http.StatusOK {
			t.Fatalf("%s: get: %d", user, code)
		}
		return out
	}
	for _, tc := range []struct {
		name, user, key, want string
		source                prefs.Source
		locked                bool
	}{
		{"kid gets the server's audio", "kid", "audio-lang", "en", prefs.SourceServer, false},
		{"kid's subtitles are locked on", "kid", "sub-mode", "always", prefs.SourceGroup, true},
		{"mum chose the file's own audio", "mum", "audio-lang", "", prefs.SourceUser, false},
		{"mum turned subtitles off", "mum", "sub-mode", "off", prefs.SourceUser, false},
		{"built-in elsewhere", "mum", "skip-intro", "button", prefs.SourceBuiltin, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := resolved(tc.user)
			got := out.Prefs[tc.key]
			if got.Value != tc.want || got.Source != tc.source || got.Locked != tc.locked {
				t.Errorf("%s = %+v, want %q from %s (locked %v)", tc.key, got, tc.want, tc.source, tc.locked)
			}
			if out.Device != f.devices[tc.user] || len(out.Registry) == 0 || len(out.Sections) == 0 {
				t.Errorf("device %q, %d registry entries, %d sections", out.Device, len(out.Registry), len(out.Sections))
			}
		})
	}

	// Playback reads the same resolved values.
	c := f.e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
	kid, err := f.st.UserByUsername(context.Background(), "kid")
	if err != nil {
		t.Fatal(err)
	}
	c.Set(ctxUser, kid)
	c.Set(ctxSessionID, f.devices["kid"])
	s := &server{store: f.st, prefs: prefs.NewService(f.st)}
	values, err := s.resolvedPrefs(c)
	if err != nil {
		t.Fatal(err)
	}
	if values[prefAudioLang] != "en" || values[prefSubMode] != "always" {
		t.Errorf("playback prefs = %v", values)
	}

	// The admin's table shows both layers.
	var table prefDefaults
	if code := f.do("admin", http.MethodGet, "/prefs/defaults", "", &table); code != http.StatusOK {
		t.Fatalf("defaults: %d", code)
	}
	if table.Server["audio-lang"].Value != "en" || len(table.Groups) != 1 || !table.Groups[0].Defaults["sub-mode"].Locked {
		t.Errorf("defaults table = %+v", table.Defaults)
	}
}
