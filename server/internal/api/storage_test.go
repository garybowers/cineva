package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

// storageFixture is a server with one TV library holding one episode whose
// file exists on disk.
func storageFixture(t *testing.T) (*server, store.Library, store.Item) {
	t.Helper()
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
	path := filepath.Join(root, "e1.mkv")
	if err := os.WriteFile(path, []byte(strings.Repeat("0123456789", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	item, err := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindEpisode,
		Title: "Episode", Path: path, RuntimeSecs: 1800})
	if err != nil {
		t.Fatal(err)
	}
	return &server{store: st, storage: library.NewStorageMonitor(st)}, lib, item
}

func storageRequest(method, target, id string, header http.Header) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(id)
	c.Set(ctxUser, store.User{ID: "u1", Role: store.RoleAdmin})
	return c, rec
}

func want503(t *testing.T, what string, err error, contains ...string) {
	t.Helper()
	var he *echo.HTTPError
	if !errors.As(err, &he) || he.Code != http.StatusServiceUnavailable {
		t.Fatalf("%s: err = %v, want 503", what, err)
	}
	msg, _ := he.Message.(string)
	for _, s := range contains {
		if !strings.Contains(msg, s) {
			t.Errorf("%s: message %q should contain %q", what, msg, s)
		}
	}
}

// Direct play and downloads serve the file with ranges, as c.File did, now
// through deadline-bound reads.
func TestServeMedia(t *testing.T) {
	s, _, item := storageFixture(t)
	tests := []struct {
		name        string
		handler     func(echo.Context) error
		rangeHeader string
		wantStatus  int
		wantBody    string
		attachment  bool
	}{
		{name: "stream, whole file", handler: s.streamItem, wantStatus: http.StatusOK, wantBody: strings.Repeat("0123456789", 100)},
		{name: "stream, a range", handler: s.streamItem, rangeHeader: "bytes=10-19", wantStatus: http.StatusPartialContent, wantBody: "0123456789"},
		{name: "stream, from the end", handler: s.streamItem, rangeHeader: "bytes=-5", wantStatus: http.StatusPartialContent, wantBody: "56789"},
		{name: "download", handler: s.downloadItem, wantStatus: http.StatusOK, wantBody: strings.Repeat("0123456789", 100), attachment: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.rangeHeader != "" {
				h.Set("Range", tc.rangeHeader)
			}
			c, rec := storageRequest(http.MethodGet, "/", item.ID, h)
			if err := tc.handler(c); err != nil {
				t.Fatal(err)
			}
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if body, _ := io.ReadAll(rec.Body); string(body) != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
			if cd := rec.Header().Get("Content-Disposition"); tc.attachment != strings.Contains(cd, `attachment; filename="e1.mkv"`) {
				t.Errorf("Content-Disposition = %q, attachment wanted: %v", cd, tc.attachment)
			}
		})
	}
}

// A file with a read still blocked from an earlier attempt is refused at
// once, rather than a second read joining the first; the storage check the
// player makes says the same.
func TestStuckFileRefused(t *testing.T) {
	s, _, item := storageFixture(t)
	release := make(chan struct{})
	library.MarkStuck(item.Path, release)
	t.Cleanup(func() { close(release) })

	for _, tc := range []struct {
		name    string
		handler func(echo.Context) error
	}{
		{"play", s.playItem},
		{"stream", s.streamItem},
		{"download", s.downloadItem},
	} {
		c, _ := storageRequest(http.MethodPost, "/", item.ID, nil)
		start := time.Now()
		want503(t, tc.name, tc.handler(c), "TV Shows", item.Path, library.StuckReason)
		if took := time.Since(start); took > time.Second {
			t.Errorf("%s: took %v to refuse", tc.name, took)
		}
	}

	c, rec := storageRequest(http.MethodGet, "/", item.ID, nil)
	if err := s.itemStorage(c); err != nil {
		t.Fatal(err)
	}
	var got itemStorageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Available || got.Library != "TV Shows" || !strings.Contains(got.Message, "TV Shows") {
		t.Errorf("storage check = %+v; want unavailable, naming the library", got)
	}
}

func TestItemStorage(t *testing.T) {
	s, lib, item := storageFixture(t)
	check := func() itemStorageResponse {
		t.Helper()
		c, rec := storageRequest(http.MethodGet, "/", item.ID, nil)
		if err := s.itemStorage(c); err != nil {
			t.Fatal(err)
		}
		var got itemStorageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := check(); !got.Available || got.Message != "" {
		t.Errorf("healthy library: %+v, want available with no message", got)
	}
	// The check is made now, not read from the last scheduled one.
	if err := os.RemoveAll(lib.Paths[0]); err != nil {
		t.Fatal(err)
	}
	got := check()
	if got.Available || got.Path != lib.Paths[0] || !strings.Contains(got.Message, "TV Shows") {
		t.Errorf("library gone: %+v, want unavailable naming the library and folder", got)
	}
}

// A transcode ended by the stall watchdog answers its playlist and segment
// requests with the same storage message as a refused play.
func TestStalledSessionAnswersWithStorageMessage(t *testing.T) {
	s, _, item := storageFixture(t)
	sess := &media.Session{ID: "sid", ItemID: item.ID}
	err := s.sessionFailed(context.Background(), sess, fmt.Errorf("%w: nothing read for 20s", media.ErrInputStalled))
	want503(t, "stalled session", err, "TV Shows", item.Path, "not responding")

	var he *echo.HTTPError
	err = s.sessionFailed(context.Background(), sess, fmt.Errorf("%w: hls open: Input/output error", media.ErrTranscodeFailed))
	if !errors.As(err, &he) || he.Code != http.StatusInternalServerError {
		t.Errorf("failed transcode: err = %v, want 500", err)
	}
}

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
		{"download", s.downloadItem},
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
