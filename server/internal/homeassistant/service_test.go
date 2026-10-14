package homeassistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/garybowers/cineva/server/internal/store"
)

const goodToken = "good-token"

// fakeHA speaks enough of Home Assistant's websocket and HTTP APIs to
// test against: authentication, states, registries, service calls, events,
// camera snapshots and an HLS stream whose first token has expired.
type fakeHA struct {
	t     *testing.T
	srv   *httptest.Server
	admin bool

	mu       sync.Mutex
	calls    []map[string]any // call_service and fire_event commands
	sendLive func(v any)      // pushes a message down the open websocket
	streams  int              // camera/stream requests answered
}

func newFakeHA(t *testing.T, admin bool) *fakeHA {
	f := &fakeHA{t: t, admin: admin}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/websocket", f.websocket)
	mux.HandleFunc("/api/camera_proxy/camera.door", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+goodToken {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		io.WriteString(w, "jpeg")
	})
	// The first stream token has expired by the time it is used.
	mux.HandleFunc("/api/hls/tok2/master_playlist.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		io.WriteString(w, "#EXTM3U\nplaylist.m3u8\n")
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

var fakeStates = []map[string]any{
	{"entity_id": "light.lounge", "state": "on", "attributes": map[string]any{"friendly_name": "Lounge", "brightness": 255.0, "supported_color_modes": []any{"brightness"}}},
	{"entity_id": "media_player.avr", "state": "on", "attributes": map[string]any{"source_list": []any{"HDMI 1", "Tuner"}}},
	{"entity_id": "camera.door", "state": "idle", "attributes": map[string]any{"supported_features": 2.0}},
	{"entity_id": "sensor.temp", "state": "19", "attributes": map[string]any{}},
}

func (f *fakeHA) websocket(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ctx := r.Context()
	var wmu sync.Mutex
	send := func(v any) {
		data, _ := json.Marshal(v)
		wmu.Lock()
		defer wmu.Unlock()
		ws.Write(ctx, websocket.MessageText, data)
	}
	send(map[string]any{"type": "auth_required", "ha_version": "2026.10.1"})
	_, data, err := ws.Read(ctx)
	if err != nil {
		return
	}
	var auth map[string]any
	json.Unmarshal(data, &auth)
	if auth["access_token"] != goodToken {
		send(map[string]any{"type": "auth_invalid", "message": "Invalid access token or password"})
		return
	}
	send(map[string]any{"type": "auth_ok", "ha_version": "2026.10.1"})
	f.mu.Lock()
	f.sendLive = send
	f.mu.Unlock()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var cmd map[string]any
		json.Unmarshal(data, &cmd)
		id := cmd["id"]
		ok := func(result any) { send(map[string]any{"id": id, "type": "result", "success": true, "result": result}) }
		switch cmd["type"] {
		case "auth/current_user":
			ok(map[string]any{"id": "u", "is_admin": f.admin})
		case "subscribe_events":
			ok(nil)
			if cmd["event_type"] == "state_changed" {
				f.mu.Lock()
				f.sendLive = func(v any) {
					ev := v.(map[string]any)
					ev["id"] = id
					send(ev)
				}
				f.mu.Unlock()
			}
		case "get_states":
			ok(fakeStates)
		case "config/area_registry/list":
			ok([]any{map[string]any{"area_id": "living_room", "name": "Living room"}})
		case "config/device_registry/list":
			ok([]any{map[string]any{"id": "dev1", "area_id": "living_room"}})
		case "config/entity_registry/list":
			ok([]any{
				map[string]any{"entity_id": "light.lounge", "area_id": "living_room"},
				map[string]any{"entity_id": "media_player.avr", "area_id": nil, "device_id": "dev1"},
			})
		case "call_service", "fire_event":
			f.mu.Lock()
			f.calls = append(f.calls, cmd)
			f.mu.Unlock()
			ok(nil)
		case "camera/stream":
			f.mu.Lock()
			f.streams++
			n := f.streams
			f.mu.Unlock()
			if n == 1 {
				ok(map[string]any{"url": "/api/hls/tok1/master_playlist.m3u8"})
			} else {
				ok(map[string]any{"url": "/api/hls/tok2/master_playlist.m3u8"})
			}
		default:
			send(map[string]any{"id": id, "type": "result", "success": false, "error": map[string]any{"code": "unknown_command", "message": "Unknown command."}})
		}
	}
}

func (f *fakeHA) lastCall() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

// stateChanged pushes a state_changed event as Home Assistant would.
func (f *fakeHA) stateChanged(entityID, state string, attrs map[string]any) {
	f.mu.Lock()
	send := f.sendLive
	f.mu.Unlock()
	send(map[string]any{"type": "event", "event": map[string]any{
		"event_type": "state_changed",
		"data":       map[string]any{"entity_id": entityID, "new_state": map[string]any{"entity_id": entityID, "state": state, "attributes": attrs}},
	}})
}

// memStore is the service's database, in memory.
type memStore struct {
	cfg      store.HAConnection
	exposure map[string]store.LibraryAccess
}

func (m *memStore) HAConnection(context.Context) (store.HAConnection, error) {
	if m.cfg.URL == "" {
		return store.HAConnection{}, store.ErrNotFound
	}
	return m.cfg, nil
}
func (m *memStore) SetHAVersion(context.Context, string) error { return nil }
func (m *memStore) HAExposures(context.Context) (map[string]store.LibraryAccess, error) {
	return m.exposure, nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCheck(t *testing.T) {
	f := newFakeHA(t, true)
	res, err := Check(context.Background(), f.srv.URL, goodToken)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "2026.10.1" || !res.IsAdmin {
		t.Fatalf("got %+v", res)
	}
	if _, err := Check(context.Background(), f.srv.URL, "wrong"); !errors.Is(err, ErrAuth) {
		t.Fatalf("wrong token: err = %v, want ErrAuth", err)
	}
	if _, err := Check(context.Background(), "ftp://nowhere", goodToken); err == nil {
		t.Fatal("ftp URL accepted")
	}
}

// startService connects a service to a fake Home Assistant with the
// lounge light and the camera exposed, the camera to admins only.
func startService(t *testing.T, f *fakeHA, events bool) (*Service, chan []Entity) {
	t.Helper()
	st := &memStore{
		cfg: store.HAConnection{Name: "Home", URL: f.srv.URL, Token: goodToken, PlaybackEvents: events},
		exposure: map[string]store.LibraryAccess{
			"light.lounge": {},
			"camera.door":  {Restricted: true},
		},
	}
	s := New(st)
	changes := make(chan []Entity, 8)
	s.OnStates = func(es []Entity) { changes <- es }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { s.Apply(nil); cancel() })
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connection", func() bool { return s.Status().Connected })
	return s, changes
}

func TestServiceStateAndControls(t *testing.T) {
	f := newFakeHA(t, false)
	s, changes := startService(t, f, false)

	// Supported domains only, and an entity with no area of its own takes
	// its device's.
	if n := len(s.Entities()); n != 3 {
		t.Fatalf("entities = %d, want 3 (the sensor is not supported)", n)
	}
	avr, _ := s.Entity("media_player.avr")
	if avr.AreaID != "living_room" {
		t.Fatalf("amplifier area = %q, want its device's", avr.AreaID)
	}
	if areas := s.Areas(); len(areas) != 1 || areas[0].Name != "Living room" {
		t.Fatalf("areas = %+v", areas)
	}

	// Access: the light is open, the camera admins' only, the amplifier
	// not exposed at all.
	kid := Principal{ID: "kid"}
	if !s.CanSee(kid, "light.lounge") || s.CanSee(kid, "camera.door") || s.CanSee(kid, "media_player.avr") {
		t.Fatal("access wrong for a user")
	}
	if !s.CanSee(Principal{Admin: true}, "camera.door") {
		t.Fatal("an admin cannot see the camera")
	}

	// A control becomes a service call with Home Assistant's field names.
	if err := s.Do(context.Background(), "light.lounge", Action{Service: "turn_on", BrightnessPct: ptr(30)}); err != nil {
		t.Fatal(err)
	}
	call := f.lastCall()
	if call["domain"] != "light" || call["service"] != "turn_on" ||
		call["service_data"].(map[string]any)["brightness_pct"] != 30.0 ||
		call["target"].(map[string]any)["entity_id"] != "light.lounge" {
		t.Fatalf("call = %v", call)
	}
	if err := s.Do(context.Background(), "media_player.avr", Action{Service: "select_source", Source: "Aux"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown input: err = %v", err)
	}

	// Changes to exposed entities are passed on; others are not.
	f.stateChanged("media_player.avr", "off", map[string]any{})
	f.stateChanged("light.lounge", "off", map[string]any{"friendly_name": "Lounge"})
	select {
	case es := <-changes:
		if len(es) != 1 || es[0].EntityID != "light.lounge" || *es[0].On {
			t.Fatalf("change = %+v", es)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no change reported")
	}
	select {
	case es := <-changes:
		t.Fatalf("unexpected change %+v", es)
	default:
	}

	// Playback events are off: the user is not an admin there.
	if s.PlaybackEventsOn() {
		t.Fatal("playback events on for a non-admin user")
	}
}

func TestServiceCameras(t *testing.T) {
	f := newFakeHA(t, true)
	s, _ := startService(t, f, true)
	ctx := context.Background()

	res, err := s.Snapshot(ctx, "camera.door")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != "jpeg" {
		t.Fatalf("snapshot = %d %q", res.StatusCode, body)
	}

	// The first stream answers 404 (its token expired): a new stream is
	// asked for, once, and its playlist served.
	res, err = s.HLS(ctx, "camera.door", "master_playlist.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "#EXTM3U") {
		t.Fatalf("playlist = %d %q", res.StatusCode, body)
	}
	for _, bad := range []string{"../../api/states", "segment/../x.m4s", "x.html", "nested/dir/x.m4s"} {
		if _, err := s.HLS(ctx, "camera.door", bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%q: err = %v, want ErrInvalid", bad, err)
		}
	}
	if _, err := s.Snapshot(ctx, "light.lounge"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("snapshot of a light: err = %v", err)
	}
}

func TestPlaybackEventsAndModes(t *testing.T) {
	f := newFakeHA(t, true)
	s, _ := startService(t, f, true)
	if !s.PlaybackEventsOn() {
		t.Fatal("playback events off for an admin user with them switched on")
	}
	s.FirePlayback(PlaybackPaused, map[string]any{"area_id": "living_room"})
	waitFor(t, "fire_event", func() bool {
		c := f.lastCall()
		return c != nil && c["type"] == "fire_event" && c["event_type"] == PlaybackPaused
	})

	// A mode runs its steps in order and plays last; a failing step stops it.
	mode := store.HAMode{ID: "m", Name: "Movie mode", Steps: []store.HAModeStep{
		{Type: "action", EntityID: "light.lounge", Service: "turn_on", BrightnessPct: ptr(10)},
		{Type: "wait", Seconds: 1},
		{Type: "play", DeviceID: "tv"},
	}}
	if err := s.Preflight(mode); err != nil {
		t.Fatal(err)
	}
	var played string
	var reports []Progress
	s.Run(context.Background(), "r1", mode, "film", func(_ context.Context, st store.HAModeStep, item string) (string, string, error) {
		played = st.DeviceID + "/" + item
		return st.DeviceID, "Living-room TV", nil
	}, func(p Progress) { reports = append(reports, p) })
	if played != "tv/film" {
		t.Fatalf("played %q", played)
	}
	if last := reports[len(reports)-1]; last.Status != "done" || len(reports) != 4 || last.PlayedOnName != "Living-room TV" {
		t.Fatalf("reports = %+v", reports)
	}

	failing := store.HAMode{ID: "f", Name: "Broken", Steps: []store.HAModeStep{
		{Type: "play", DeviceID: "tv"},
		{Type: "action", EntityID: "light.lounge", Service: "turn_off"},
	}}
	reports = nil
	s.Run(context.Background(), "r2", failing, "film", func(context.Context, store.HAModeStep, string) (string, string, error) {
		return "", "", errors.New("Living-room TV is not connected")
	}, func(p Progress) { reports = append(reports, p) })
	if last := reports[len(reports)-1]; last.Status != "failed" || last.Step != 1 || last.Error != "Living-room TV is not connected" {
		t.Fatalf("reports = %+v", reports)
	}

	missing := store.HAMode{Steps: []store.HAModeStep{{Type: "action", EntityID: "light.gone", Service: "turn_on"}}}
	if err := s.Preflight(missing); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing entity: err = %v", err)
	}
}
