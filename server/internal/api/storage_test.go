package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/store"
)

// Pressing play on a title whose share is down answers at once, with which
// library and folder and what to check, rather than waiting on the share.
func TestPlayFailsFastWhenStorageIsDown(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	lib, err := st.CreateLibrary(ctx, "TV Shows", store.MediaTV, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	item, err := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindEpisode,
		Title: "Episode", Path: filepath.Join(root, "e1.mkv"), RuntimeSecs: 1800})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{store: st, storage: library.NewStorageMonitor(st)}

	// The share goes: its root folder no longer answers.
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	s.storage.Check(lib)

	for _, tc := range []struct {
		name    string
		handler func(echo.Context) error
	}{
		{"play", s.playItem},
		{"stream", s.streamItem},
	} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		c := echo.New().NewContext(req, httptest.NewRecorder())
		c.SetParamNames("id")
		c.SetParamValues(item.ID)
		c.Set(ctxUser, store.User{ID: "u1", Role: store.RoleAdmin})

		start := time.Now()
		err := tc.handler(c)
		var he *echo.HTTPError
		if !errors.As(err, &he) || he.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: err = %v, want 503", tc.name, err)
		}
		msg, _ := he.Message.(string)
		if !strings.Contains(msg, "TV Shows") || !strings.Contains(msg, root) {
			t.Errorf("%s: message %q should name the library and the folder", tc.name, msg)
		}
		if took := time.Since(start); took > time.Second {
			t.Errorf("%s: took %v to refuse; it should not wait on the storage at all", tc.name, took)
		}
	}
}
