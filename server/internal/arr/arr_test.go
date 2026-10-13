package arr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/garybowers/cineva/server/internal/store"
)

// fakeArr is enough of Sonarr's API to register a webhook against.
type fakeArr struct {
	mu      sync.Mutex
	app     string
	saved   map[int]map[string]any
	nextID  int
	refuses string // a validation failure to answer saves with
}

func (f *fakeArr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-Api-Key") != "key" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/api/v3/system/status":
		json.NewEncoder(w).Encode(map[string]any{"appName": f.app, "version": "4.0.15"})
	case r.URL.Path == "/api/v3/notification/schema":
		json.NewEncoder(w).Encode([]map[string]any{
			{"implementation": "Slack", "fields": []any{}},
			{
				"implementation": "Webhook", "name": "",
				"onGrab": false, "supportsOnGrab": true,
				"onDownload": false, "supportsOnDownload": true,
				"onRename": false, "supportsOnRename": true,
				"onHealthIssue": false, "supportsOnHealthIssue": true,
				"onMovieDelete": false, "supportsOnMovieDelete": false,
				"fields": []any{
					map[string]any{"name": "url", "value": ""},
					map[string]any{"name": "method", "value": 2},
					map[string]any{"name": "username", "value": ""},
					map[string]any{"name": "password", "value": ""},
					map[string]any{"name": "headers", "value": []any{}},
				},
			},
		})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v3/notification",
		r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v3/notification/"):
		if f.refuses != "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode([]map[string]any{{"propertyName": "Url", "errorMessage": f.refuses}})
			return
		}
		var def map[string]any
		json.NewDecoder(r.Body).Decode(&def)
		id := f.nextID + 1
		if r.Method == http.MethodPut {
			id = idFrom(r.URL.Path)
		} else {
			f.nextID = id
		}
		def["id"] = id
		f.saved[id] = def
		json.NewEncoder(w).Encode(def)
	case strings.HasPrefix(r.URL.Path, "/api/v3/notification/"):
		id := idFrom(r.URL.Path)
		def, ok := f.saved[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodDelete {
			delete(f.saved, id)
			return
		}
		json.NewEncoder(w).Encode(def)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func idFrom(path string) int {
	var id int
	for _, ch := range strings.TrimPrefix(path, "/api/v3/notification/") {
		id = id*10 + int(ch-'0')
	}
	return id
}

func field(def map[string]any, name string) any {
	for _, f := range def["fields"].([]any) {
		if m := f.(map[string]any); m["name"] == name {
			return m["value"]
		}
	}
	return nil
}

func TestRegisterWebhook(t *testing.T) {
	ctx := context.Background()
	fake := &fakeArr{app: "Sonarr", saved: map[int]map[string]any{}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	c := New("sonarr", srv.URL+"/", "key")
	hook := Webhook{Name: "Cineva", URL: "http://cineva:4747/api/v1/integrations/x/webhook", Username: "cineva", Password: "s3cret"}

	if st, err := c.Status(ctx); err != nil || st.Version != "4.0.15" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	id, err := c.RegisterWebhook(ctx, 0, hook)
	if err != nil {
		t.Fatal(err)
	}
	def := fake.saved[id]
	for key, want := range map[string]bool{
		"onDownload": true, "onRename": true,
		"onGrab": false, "onHealthIssue": false,
		"onMovieDelete": false, // wanted, but this app does not support it
	} {
		if def[key] != want {
			t.Errorf("%s = %v, want %v", key, def[key], want)
		}
	}
	if field(def, "url") != hook.URL || field(def, "password") != "s3cret" || field(def, "method") != float64(1) {
		t.Errorf("fields = %v", def["fields"])
	}

	// Registering again updates the same webhook...
	if again, err := c.RegisterWebhook(ctx, id, hook); err != nil || again != id {
		t.Fatalf("re-register = %d, %v; want %d", again, err, id)
	}
	// ...and recreates one deleted on the app's side.
	if err := c.DeleteWebhook(ctx, id); err != nil {
		t.Fatal(err)
	}
	if fresh, err := c.RegisterWebhook(ctx, id, hook); err != nil || fresh == id || fake.saved[fresh] == nil {
		t.Fatalf("register after delete = %d, %v", fresh, err)
	}
	if err := c.DeleteWebhook(ctx, 999); err != nil {
		t.Fatalf("deleting a webhook already gone: %v", err)
	}
}

func TestClientErrors(t *testing.T) {
	ctx := context.Background()
	fake := &fakeArr{app: "Radarr", saved: map[int]map[string]any{}, refuses: "Unable to send test message"}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"wrong key", func() error { _, err := New("radarr", srv.URL, "nope").Status(ctx); return err }, "rejected the API key"},
		{"wrong app", func() error { _, err := New("sonarr", srv.URL, "key").Status(ctx); return err }, "is Radarr, not Sonarr"},
		{"test fails", func() error { _, err := New("radarr", srv.URL, "key").RegisterWebhook(ctx, 0, Webhook{}); return err }, "Radarr: Unable to send test message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParseEvent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		changes bool
		paths   []string
	}{
		{"sonarr test", `{"eventType":"Test","series":{"path":"C:\\testpath"}}`, false, []string{`C:\testpath`}},
		{"sonarr download", `{"eventType":"Download","series":{"path":"/tv/Show"},"episodeFile":{"path":"/tv/Show/Season 02/e3.mkv"}}`,
			true, []string{"/tv/Show/Season 02/e3.mkv", "/tv/Show"}},
		{"sonarr season pack", `{"eventType":"ImportComplete","series":{"path":"/tv/Show"},"episodeFiles":[{"path":"/tv/Show/a.mkv"},{"path":"/tv/Show/b.mkv"}]}`,
			true, []string{"/tv/Show/a.mkv", "/tv/Show/b.mkv", "/tv/Show"}},
		{"radarr download", `{"eventType":"Download","movie":{"folderPath":"/movies/Film (2020)"},"movieFile":{"path":"/movies/Film (2020)/f.mkv"}}`,
			true, []string{"/movies/Film (2020)/f.mkv", "/movies/Film (2020)"}},
		{"grab", `{"eventType":"Grab","series":{"path":"/tv/Show"}}`, false, []string{"/tv/Show"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := ParseEvent([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if ev.ChangesFiles() != tc.changes || strings.Join(ev.Paths, "|") != strings.Join(tc.paths, "|") {
				t.Fatalf("event = %+v (changes %v), want changes %v paths %v", ev, ev.ChangesFiles(), tc.changes, tc.paths)
			}
		})
	}
	if _, err := ParseEvent([]byte("not json")); err == nil {
		t.Fatal("parsed a body that is not JSON")
	}
}

func TestLibraries(t *testing.T) {
	libs := []store.Library{
		{ID: "tv", MediaType: store.MediaTV, Source: "local", Paths: []string{"/mnt/media/TV"}},
		{ID: "kids", MediaType: store.MediaTV, Source: "local", Paths: []string{"/mnt/media/Kids TV"}},
		{ID: "remote", MediaType: store.MediaTV, Source: "federated", Paths: []string{"/mnt/media/TV"}},
		{ID: "films", MediaType: store.MediaMovies, Source: "local", Paths: []string{"/mnt/media/Movies"}},
	}
	for _, tc := range []struct {
		name  string
		kind  string
		paths []string
		want  string
	}{
		{"matching folder", "sonarr", []string{"/mnt/media/TV/Show/s1/e1.mkv"}, "tv"},
		{"prefix is not containment", "sonarr", []string{"/mnt/media/TV2/Show"}, "tv,kids"},
		{"container paths fall back to the type", "sonarr", []string{"/tv/Show"}, "tv,kids"},
		{"radarr", "radarr", []string{"/movies/Film"}, "films"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ids []string
			for _, l := range Libraries(libs, tc.kind, tc.paths) {
				ids = append(ids, l.ID)
			}
			if got := strings.Join(ids, ","); got != tc.want {
				t.Fatalf("libraries = %s, want %s", got, tc.want)
			}
		})
	}
}
