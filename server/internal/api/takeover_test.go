package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/playback"
	"github.com/garybowers/cineva/server/internal/store"
)

// Continuing on the phone what the TV plays: the TV is told to pause, the
// phone gets the title from where the TV stopped, and the paused TV can no
// longer overwrite the phone's place.
func TestTakeOverDevice(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{"/films"})
	if err != nil {
		t.Fatal(err)
	}
	film, err := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindMovie, Title: "Paddington", Path: "/films/p.mkv", RuntimeSecs: 5700})
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(ctx, "viewer", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	phone, err := st.CreateSession(ctx, "phone", u.ID, "2099-01-01 00:00:00", "Safari · iOS", "::1", "")
	if err != nil {
		t.Fatal(err)
	}
	tv, err := st.CreateSession(ctx, "tv", u.ID, "2099-01-01 00:00:00", "Chrome · Android", "::1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetDeviceName(ctx, tv.ID, "Living-room TV"); err != nil {
		t.Fatal(err)
	}

	s := &server{store: st, events: events.NewBus(), presence: newPresence(), playbacks: playback.NewRegistry(time.Minute)}
	ctxFor := func(device store.Session, method, body string) (echo.Context, *httptest.ResponseRecorder) {
		req := httptest.NewRequest(method, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.Set(ctxUser, u)
		c.Set(ctxSessionID, device.ID)
		return c, rec
	}
	takeOver := func() (takeover, error) {
		c, rec := ctxFor(phone, http.MethodPost, "")
		c.SetParamNames("id")
		c.SetParamValues(tv.ID)
		if err := s.takeOverDevice(c); err != nil {
			return takeover{}, err
		}
		var out takeover
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out, nil
	}
	progress := func(device store.Session, secs int, paused bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"positionSecs": secs, "paused": paused})
		c, _ := ctxFor(device, http.MethodPost, string(body))
		c.SetParamNames("id")
		c.SetParamValues(film.ID)
		if err := s.postProgress(c); err != nil {
			t.Fatal(err)
		}
	}

	var he *echo.HTTPError
	if _, err := takeOver(); !errors.As(err, &he) || he.Code != http.StatusConflict {
		t.Fatalf("nothing playing on the TV: %v, want 409", err)
	}

	// The TV plays the film, and is online to be told.
	s.playbacks.Start(playback.Playback{UserID: u.ID, ItemID: film.ID, DeviceID: tv.ID, DurationSecs: 5700})
	progress(tv, 600, false)
	s.presence.connect(tv.ID)
	sub := s.events.Subscribe(ctx)
	go func() {
		for ev := range sub {
			if ev.Type == "takeover" {
				if ev.Session != tv.ID {
					t.Errorf("takeover sent to %q, want the TV", ev.Session)
				}
				// The TV pauses where it got to and says so.
				progress(tv, 642, true)
				return
			}
		}
	}()
	got, err := takeOver()
	if err != nil {
		t.Fatal(err)
	}
	if got.PositionSecs != 642 || len(got.Items) != 1 || got.Items[0].ID != film.ID || got.From != "Living-room TV" {
		t.Fatalf("takeover = %+v, want the film at 642 s from the TV", got)
	}
	if ps, _ := st.PlayStateFor(ctx, u.ID, film.ID); ps.PositionSecs != 642 {
		t.Fatalf("saved position = %d, want 642 for the phone's play to resume at", ps.PositionSecs)
	}

	// The phone plays on; the paused TV's heartbeats change nothing.
	s.playbacks.Start(playback.Playback{UserID: u.ID, ItemID: film.ID, DeviceID: phone.ID, DurationSecs: 5700})
	progress(phone, 700, false)
	progress(tv, 642, true)
	if ps, _ := st.PlayStateFor(ctx, u.ID, film.ID); ps.PositionSecs != 700 {
		t.Fatalf("after the paused TV's heartbeat: position %d, want the phone's 700", ps.PositionSecs)
	}
	if p, _ := s.playbacks.Get(u.ID, film.ID); p.DeviceID != phone.ID || p.Paused {
		t.Fatalf("playback = %+v, want the phone's, playing", p)
	}
	// Playing on the TV again takes it back.
	progress(tv, 642, false)
	if p, _ := s.playbacks.Get(u.ID, film.ID); p.DeviceID != tv.ID {
		t.Fatalf("after the TV played again: device %q, want the TV", p.DeviceID)
	}
}
