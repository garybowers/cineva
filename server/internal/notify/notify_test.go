package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/store"
)

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scope  Scope
		kind   string
		config map[string]string
		events []string
		ntfy   string
		ok     bool
	}{
		{"household ntfy", Household, "ntfy", map[string]string{"server": "https://ntfy.example/", "topic": "cineva"}, []string{"storage.down"}, "", true},
		{"bad topic", Household, "ntfy", map[string]string{"server": "https://ntfy.example", "topic": "a b"}, nil, "", false},
		{"personal ntfy, none allowed", Personal, "ntfy", map[string]string{"topic": "mine"}, nil, "", false},
		{"personal ntfy takes the admin's server", Personal, "ntfy", map[string]string{"server": "http://10.0.0.1", "topic": "mine"}, []string{"episode.new"}, "https://ntfy.sh", true},
		{"personal webhook refused", Personal, "webhook", map[string]string{"url": "http://router.lan/"}, nil, "", false},
		{"personal gotify refused", Personal, "gotify", map[string]string{"server": "http://gotify.lan", "token": "x"}, nil, "", false},
		{"discord", Personal, "discord", map[string]string{"webhookUrl": "https://discord.com/api/webhooks/1/abc"}, []string{"request.available"}, "", true},
		{"discord elsewhere", Personal, "discord", map[string]string{"webhookUrl": "http://evil.lan/api/webhooks/1"}, nil, "", false},
		{"telegram", Personal, "telegram", map[string]string{"botToken": "123:ABC-def", "chatId": "-1001"}, nil, "", true},
		{"telegram bad token", Personal, "telegram", map[string]string{"botToken": "nope", "chatId": "1"}, nil, "", false},
		{"missing required", Household, "gotify", map[string]string{"server": "http://g"}, nil, "", false},
		{"household event on personal", Personal, "discord", map[string]string{"webhookUrl": "https://discord.com/api/webhooks/1/a"}, []string{"storage.down"}, "", false},
		{"personal event on household", Household, "webhook", map[string]string{"url": "http://hook"}, []string{"episode.new"}, "", false},
		{"unknown kind", Household, "pager", map[string]string{}, nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.scope, tc.kind, tc.config, tc.events, tc.ntfy)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok {
				var inv ErrInvalid
				if !errors.As(err, &inv) {
					t.Fatalf("err = %v, want ErrInvalid", err)
				}
			}
		})
	}
	cfg := map[string]string{"server": "http://10.0.0.1", "topic": "mine", "extra": "x"}
	if err := Validate(Personal, "ntfy", cfg, nil, "https://ntfy.sh"); err != nil || cfg["server"] != "https://ntfy.sh" || cfg["extra"] != "" {
		t.Fatalf("personal ntfy config = %v, %v", cfg, err)
	}
}

func TestMaskAndKeepSecrets(t *testing.T) {
	saved := map[string]string{"botToken": "123:SECRETTOKEN", "chatId": "42"}
	masked := Mask("telegram", saved)
	if masked["botToken"] != "••••OKEN" || masked["chatId"] != "42" {
		t.Fatalf("masked = %v", masked)
	}
	update := map[string]string{"botToken": masked["botToken"], "chatId": "43"}
	KeepSecrets("telegram", update, saved)
	if update["botToken"] != "123:SECRETTOKEN" || update["chatId"] != "43" {
		t.Fatalf("kept = %v", update)
	}
	update = map[string]string{"botToken": "", "chatId": "43"}
	KeepSecrets("telegram", update, saved)
	if update["botToken"] != "123:SECRETTOKEN" {
		t.Fatalf("blank did not keep the secret: %v", update)
	}
}

// received is one request a fake service got.
type received struct {
	path    string
	headers http.Header
	body    string
}

func capture(t *testing.T, status int) (*httptest.Server, func() []received) {
	var mu sync.Mutex
	var got []received
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, received{r.URL.Path + "?" + r.URL.RawQuery, r.Header.Clone(), string(b)})
		mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []received {
		mu.Lock()
		defer mu.Unlock()
		return append([]received(nil), got...)
	}
}

func TestSendShapes(t *testing.T) {
	srv, got := capture(t, http.StatusOK)
	telegramAPI = srv.URL
	t.Cleanup(func() { telegramAPI = "https://api.telegram.org" })
	m := Message{Event: "storage.down", Title: "Films storage unreachable", Body: "Check the NAS.", URL: "https://cineva.example/admin", High: true}
	ctx := context.Background()
	client := srv.Client()
	for _, tc := range []struct {
		kind   string
		config map[string]string
		check  func(r received) bool
	}{
		{"ntfy", map[string]string{"server": srv.URL, "topic": "cineva", "token": "tk"}, func(r received) bool {
			return r.path == "/cineva?" && r.headers.Get("Title") == m.Title && r.headers.Get("Priority") == "high" &&
				r.headers.Get("Click") == m.URL && r.headers.Get("Authorization") == "Bearer tk" && r.body == m.Body
		}},
		{"gotify", map[string]string{"server": srv.URL, "token": "gt"}, func(r received) bool {
			var b map[string]any
			_ = json.Unmarshal([]byte(r.body), &b)
			return r.path == "/message?" && r.headers.Get("X-Gotify-Key") == "gt" && b["priority"] == float64(8) && b["title"] == m.Title
		}},
		{"discord", map[string]string{"webhookUrl": srv.URL + "/api/webhooks/1/x"}, func(r received) bool {
			var b struct {
				Embeds []map[string]any `json:"embeds"`
			}
			_ = json.Unmarshal([]byte(r.body), &b)
			return len(b.Embeds) == 1 && b.Embeds[0]["title"] == m.Title && b.Embeds[0]["url"] == m.URL
		}},
		{"telegram", map[string]string{"botToken": "1:A", "chatId": "42"}, func(r received) bool {
			var b map[string]any
			_ = json.Unmarshal([]byte(r.body), &b)
			return r.path == "/bot1:A/sendMessage?" && b["chat_id"] == "42" && b["text"] == m.Title+"\n"+m.Body+"\n"+m.URL
		}},
		{"webhook", map[string]string{"url": srv.URL + "/hook", "secret": "s"}, func(r received) bool {
			var b map[string]any
			_ = json.Unmarshal([]byte(r.body), &b)
			return r.headers.Get("X-Cineva-Secret") == "s" && b["event"] == "storage.down" && b["priority"] == "high"
		}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			before := len(got())
			if err := send(ctx, client, tc.kind, tc.config, m); err != nil {
				t.Fatal(err)
			}
			all := got()
			if len(all) != before+1 || !tc.check(all[len(all)-1]) {
				t.Fatalf("%s sent %+v", tc.kind, all[len(all)-1])
			}
		})
	}

	// A refusal is not retried; an unreachable service is.
	bad, _ := capture(t, http.StatusUnauthorized)
	err := send(ctx, bad.Client(), "webhook", map[string]string{"url": bad.URL}, m)
	var se sendError
	if !errors.As(err, &se) || se.retry {
		t.Fatalf("401: %v, want a final error", err)
	}
	err = send(ctx, http.DefaultClient, "webhook", map[string]string{"url": "http://127.0.0.1:1"}, m)
	if !errors.As(err, &se) || !se.retry {
		t.Fatalf("unreachable: %v, want a retryable error", err)
	}
}

// memStore is the service's database, in memory.
type memStore struct {
	mu      sync.Mutex
	dests   []store.NotifyDestination
	libs    map[string]store.Library
	results map[string]string
	public  string
}

func (m *memStore) EnabledNotifyDestinations(context.Context) ([]store.NotifyDestination, error) {
	return m.dests, nil
}
func (m *memStore) RecordNotifyResult(_ context.Context, id, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[id] = errMsg
	return nil
}
func (m *memStore) Library(_ context.Context, id string) (store.Library, error) {
	return m.libs[id], nil
}
func (m *memStore) Setting(_ context.Context, key, def string) (string, error) {
	if key == SettingPublicURL {
		return m.public, nil
	}
	return def, nil
}

// Household notices honour library choice, personal ones reach only their
// owner, and links use the public address.
func TestRouting(t *testing.T) {
	srv, got := capture(t, http.StatusOK)
	hook := func(id, owner string, events []string, libs []string) store.NotifyDestination {
		return store.NotifyDestination{ID: id, OwnerID: owner, Kind: "webhook", Name: id, Enabled: true,
			Config: map[string]string{"url": srv.URL + "/" + id}, Events: events, LibraryIDs: libs}
	}
	st := &memStore{
		results: map[string]string{}, public: "https://cineva.example",
		libs: map[string]store.Library{"films": {ID: "films"}, "grown": {ID: "grown", Restricted: true}},
		dests: []store.NotifyDestination{
			hook("family", "", []string{"library.new"}, nil),
			hook("grownups", "", []string{"library.new"}, []string{"grown"}),
			hook("kid", "kid", []string{"episode.new"}, nil),
			hook("dad", "dad", []string{"episode.new"}, nil),
		},
	}
	s := New(st)
	s.http = srv.Client()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	s.Household(Notice{Event: "library.new", Title: "New in Films", LibraryID: "films", Path: "/items/1"})
	s.Household(Notice{Event: "library.new", Title: "New in Grown-ups", LibraryID: "grown"})
	s.Personal("kid", Notice{Event: "episode.new", Title: "New episode: Bluey"})
	deadline := time.Now().Add(5 * time.Second)
	for len(got()) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	paths := map[string]string{}
	for _, r := range got() {
		var b map[string]any
		_ = json.Unmarshal([]byte(r.body), &b)
		paths[r.path] = b["title"].(string)
		if r.path == "/family?" && b["url"] != "https://cineva.example/items/1" {
			t.Errorf("link = %v", b["url"])
		}
	}
	want := map[string]string{"/family?": "New in Films", "/grownups?": "New in Grown-ups", "/kid?": "New episode: Bluey"}
	if len(paths) != len(want) {
		t.Fatalf("delivered %v, want %v", paths, want)
	}
	for p, title := range want {
		if paths[p] != title {
			t.Fatalf("delivered %v, want %v", paths, want)
		}
	}
}
