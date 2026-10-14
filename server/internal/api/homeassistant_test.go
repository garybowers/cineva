package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/homeassistant"
	"github.com/garybowers/cineva/server/internal/playback"
	"github.com/garybowers/cineva/server/internal/store"
)

// Home Assistant's rules as the handlers apply them: cameras start
// admins-only, controls and modes are refused to those not allowed, and a
// mode's play step reaches only an online device, with a title both the
// runner and the device's user may watch. None of this needs Home
// Assistant itself: a mode that only plays runs without it.
func TestHomeAssistantHandlers(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	films, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{"/films"})
	if err != nil {
		t.Fatal(err)
	}
	grown, err := st.CreateLibrary(ctx, "Grown-ups", store.MediaMovies, []string{"/grown"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryAccess(ctx, grown.ID, store.LibraryAccess{Restricted: true}); err != nil {
		t.Fatal(err)
	}
	paddington, err := st.UpsertItem(ctx, store.Item{LibraryID: films.ID, Kind: store.KindMovie, Title: "Paddington", Path: "/films/p.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	alien, err := st.UpsertItem(ctx, store.Item{LibraryID: grown.ID, Kind: store.KindMovie, Title: "Alien", Path: "/grown/a.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := st.CreateUser(ctx, "gary", "", "h", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	kid, err := st.CreateUser(ctx, "kid", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	kid.LibraryIDs = []string{films.ID}
	tv, err := st.CreateSession(ctx, "tv", kid.ID, "2099-01-01 00:00:00", "Chrome · Android", "::1", "Living-room TV")
	if err != nil {
		t.Fatal(err)
	}

	s := &server{store: st, events: events.NewBus(), presence: newPresence(), ha: homeassistant.New(st),
		playbacks: playback.NewRegistry(time.Minute)}
	evCtx, stop := context.WithCancel(ctx)
	defer stop()
	sub := s.events.Subscribe(evCtx)

	type call struct {
		as      store.User
		handler func(echo.Context) error
		params  map[string]string
		body    string
	}
	do := func(cl call) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(cl.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		var names, values []string
		for k, v := range cl.params {
			names, values = append(names, k), append(values, v)
		}
		c.SetParamNames(names...)
		c.SetParamValues(values...)
		c.Set(ctxUser, cl.as)
		if err := cl.handler(c); err != nil {
			var he *echo.HTTPError
			if !errors.As(err, &he) {
				t.Fatalf("unexpected error: %v", err)
			}
			return he.Code, fmt.Sprint(he.Message)
		}
		return rec.Code, rec.Body.String()
	}

	// Exposing: a camera starts admins-only, a light open to all.
	for _, tc := range []struct {
		entity     string
		restricted bool
	}{{"camera.door", true}, {"light.lounge", false}} {
		code, body := do(call{as: admin, handler: s.putHAEntity, params: map[string]string{"entityId": tc.entity}, body: `{"exposed":true}`})
		if code != http.StatusOK {
			t.Fatalf("expose %s: %d %s", tc.entity, code, body)
		}
		var out haEntityAdmin
		json.Unmarshal([]byte(body), &out)
		if !out.Exposed || out.Access.Restricted != tc.restricted {
			t.Fatalf("expose %s: %+v", tc.entity, out)
		}
	}
	if code, _ := do(call{as: admin, handler: s.putHAEntity, params: map[string]string{"entityId": "lock.front"}, body: `{"exposed":true}`}); code != http.StatusBadRequest {
		t.Fatalf("exposing a lock: %d, want 400", code)
	}

	// Controls: the camera is hidden from the kid; the light is theirs,
	// but Home Assistant is not connected.
	action := func(entity string) int {
		code, _ := do(call{as: kid, handler: s.haEntityAction, params: map[string]string{"entityId": entity}, body: `{"service":"turn_on"}`})
		return code
	}
	if got := action("camera.door"); got != http.StatusNotFound {
		t.Fatalf("kid on the camera: %d, want 404", got)
	}
	if got := action("light.lounge"); got != http.StatusBadGateway {
		t.Fatalf("kid on the light, no connection: %d, want 502", got)
	}
	if code, _ := do(call{as: kid, handler: s.haCameraSnapshot, params: map[string]string{"entityId": "camera.door"}}); code != http.StatusNotFound {
		t.Fatalf("kid's snapshot: %d, want 404", code)
	}

	// Building modes.
	create := func(body string) (int, string) {
		return do(call{as: admin, handler: s.createHAMode, body: body})
	}
	if code, msg := create(`{"name":"Bad","steps":[{"type":"wait","seconds":99}]}`); code != http.StatusBadRequest {
		t.Fatalf("long wait: %d %s", code, msg)
	}
	if code, msg := create(`{"name":"Nowhere","steps":[{"type":"play","deviceId":"gone"}]}`); code != http.StatusBadRequest {
		t.Fatalf("unknown device: %d %s", code, msg)
	}
	code, body := create(`{"name":"Movie mode","steps":[{"type":"play","deviceId":"` + tv.ID + `"}]}`)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var movie haModeOut
	json.Unmarshal([]byte(body), &movie)
	if !movie.NeedsTitle {
		t.Fatal("a play step without a title should need one")
	}
	if code, _ := create(`{"name":"movie mode","steps":[{"type":"wait","seconds":1}]}`); code != http.StatusConflict {
		t.Fatalf("duplicate name: %d, want 409", code)
	}
	code, body = create(`{"name":"Grown-ups only","steps":[{"type":"wait","seconds":1}],"access":{"restricted":true,"userIds":[],"groupIds":[]}}`)
	if code != http.StatusCreated {
		t.Fatalf("create restricted: %d %s", code, body)
	}
	var private haModeOut
	json.Unmarshal([]byte(body), &private)

	// Listing: the kid sees only what they may run, without access lists.
	code, body = do(call{as: kid, handler: s.listHAModes})
	var listed []haModeOut
	json.Unmarshal([]byte(body), &listed)
	if code != http.StatusOK || len(listed) != 1 || listed[0].Name != "Movie mode" || listed[0].Access != nil {
		t.Fatalf("kid's modes: %d %s", code, body)
	}

	// Running.
	run := func(id, item string) (int, string) {
		return do(call{as: kid, handler: s.runHAMode, params: map[string]string{"id": id}, body: `{"itemId":"` + item + `"}`})
	}
	if code, _ := run(private.ID, ""); code != http.StatusNotFound {
		t.Fatalf("restricted mode: %d, want 404", code)
	}
	if code, _ := run(movie.ID, ""); code != http.StatusBadRequest {
		t.Fatalf("no title: %d, want 400", code)
	}
	if code, _ := run(movie.ID, paddington.ID); code != http.StatusConflict {
		t.Fatalf("TV offline: %d, want 409", code)
	}
	s.presence.connect(tv.ID)
	if code, _ := run(movie.ID, alien.ID); code != http.StatusBadRequest {
		t.Fatalf("a title the kid cannot see: %d, want 400", code)
	}
	if code, body := run(movie.ID, paddington.ID); code != http.StatusAccepted {
		t.Fatalf("run: %d %s", code, body)
	}
	// The TV is handed the film, and the runner told the mode finished.
	var handed, finished bool
	deadline := time.After(5 * time.Second)
	for !handed || !finished {
		select {
		case ev := <-sub:
			switch ev.Type {
			case "handoff":
				h := ev.Data.(handoff)
				handed = ev.Session == tv.ID && h.From == "Movie mode" && len(h.Items) == 1 && h.Items[0].ID == paddington.ID
			case "mode-run":
				p := ev.Data.(homeassistant.Progress)
				if p.Status == "failed" {
					t.Fatalf("run failed: %+v", p)
				}
				finished = finished || (p.Status == "done" && ev.User == kid.ID && p.PlayedOn == tv.ID && p.PlayedOnName == "Living-room TV")
			}
		case <-deadline:
			t.Fatalf("handed %v, finished %v", handed, finished)
		}
	}

	// Run from the player on a TV already playing the film, the mode sets
	// the room around it rather than starting it again.
	s.playbacks.Start(playback.Playback{UserID: kid.ID, ItemID: paddington.ID, DeviceID: tv.ID})
	if code, body := run(movie.ID, paddington.ID); code != http.StatusAccepted {
		t.Fatalf("run while playing: %d %s", code, body)
	}
	deadline = time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case ev := <-sub:
			switch ev.Type {
			case "handoff":
				t.Fatal("the TV was handed the film it is already playing")
			case "mode-run":
				p := ev.Data.(homeassistant.Progress)
				if p.Status == "failed" || (p.Status == "done" && p.PlayedOn != "") {
					t.Fatalf("run while playing: %+v", p)
				}
				done = p.Status == "done"
			}
		case <-deadline:
			t.Fatal("run while playing never finished")
		}
	}

	// Rooms with no Home Assistant say so rather than failing.
	code, body = do(call{as: kid, handler: s.haRooms})
	if code != http.StatusOK || !strings.Contains(body, `"configured":false`) {
		t.Fatalf("rooms: %d %s", code, body)
	}
}
