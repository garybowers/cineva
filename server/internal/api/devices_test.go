package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/store"
)

// "Play on…" reaches only an online device of the same user, and carries
// only what that user may watch.
func TestPlayOnDevice(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	open, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{"/films"})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := st.CreateLibrary(ctx, "Grown-ups", store.MediaMovies, []string{"/grown"})
	if err != nil {
		t.Fatal(err)
	}
	film, err := st.UpsertItem(ctx, store.Item{LibraryID: open.ID, Kind: store.KindMovie, Title: "Paddington", Path: "/films/p.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := st.UpsertItem(ctx, store.Item{LibraryID: hidden.ID, Kind: store.KindMovie, Title: "Alien", Path: "/grown/a.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	kid, err := st.CreateUser(ctx, "kid", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	kid.LibraryIDs = []string{open.ID}
	phone, err := st.CreateSession(ctx, "phone", kid.ID, "2099-01-01 00:00:00", "Safari · iOS", "::1", "")
	if err != nil {
		t.Fatal(err)
	}
	tv, err := st.CreateSession(ctx, "tv", kid.ID, "2099-01-01 00:00:00", "Chrome · Android", "::1", "TV")
	if err != nil {
		t.Fatal(err)
	}

	s := &server{store: st, events: events.NewBus(), presence: newPresence()}
	evCtx, stop := context.WithCancel(ctx)
	defer stop()
	sub := s.events.Subscribe(evCtx)

	call := func(target string) error {
		body := `{"itemIds":["` + secret.ID + `","` + film.ID + `"],"index":1}`
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		c := echo.New().NewContext(req, httptest.NewRecorder())
		c.SetParamNames("id")
		c.SetParamValues(target)
		c.Set(ctxUser, kid)
		c.Set(ctxSession, "phone")
		c.Set(ctxSessionID, phone.ID)
		return s.playOnDevice(c)
	}
	code := func(err error) int {
		var he *echo.HTTPError
		if errors.As(err, &he) {
			return he.Code
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return http.StatusAccepted
	}

	if got := code(call(tv.ID)); got != http.StatusConflict {
		t.Fatalf("offline TV: status %d, want 409", got)
	}
	if got := code(call(phone.ID)); got != http.StatusBadRequest {
		t.Fatalf("sending to itself: status %d, want 400", got)
	}
	s.presence.connect(tv.ID)
	if got := code(call(tv.ID)); got != http.StatusAccepted {
		t.Fatalf("online TV: status %d, want 202", got)
	}
	select {
	case ev := <-sub:
		h, ok := ev.Data.(handoff)
		if ev.Type != "handoff" || ev.Session != tv.ID || ev.User != kid.ID || !ok {
			t.Fatalf("event = %+v, want a handoff addressed to the TV", ev)
		}
		if len(h.Items) != 1 || h.Items[0].ID != film.ID || h.Index != 0 || h.From != "Safari · iOS" {
			t.Fatalf("handoff = %+v: want only the visible film, still current, from the phone", h)
		}
	case <-time.After(time.Second):
		t.Fatal("no handoff event")
	}
}
