package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/store"
)

// A native app signs in with bearer: true, gets a token and a device of
// its own, uses the token on every request, and signs out.
func TestBearerSignIn(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("Correct-horse-2")
	u, err := st.CreateUser(ctx, "gary", "Gary", hash, store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{store: st, throttle: auth.NewThrottle(), events: events.NewBus(), sessionTTL: 30 * 24 * time.Hour}
	e := echo.New()

	login := func(body string) (int, bearerLogin, *httptest.ResponseRecorder) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		var out bearerLogin
		if err := s.authLogin(c); err != nil {
			var he *echo.HTTPError
			if errors.As(err, &he) {
				return he.Code, out, rec
			}
			t.Fatal(err)
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out, rec
	}
	code, res, rec := login(`{"username":"gary","password":"Correct-horse-2","bearer":true,"device":{"id":"install-1","name":"Pixel 8","client":"Cineva Android 0.1"}}`)
	if code != 200 || res.Token == "" || res.SessionID == "" || res.Username != "gary" {
		t.Fatalf("bearer login: %d %+v", code, res)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("a bearer sign-in set cookies: %v", rec.Result().Cookies())
	}
	sessions, err := st.UserSessions(ctx, u.ID)
	if err != nil || len(sessions) != 1 || sessions[0].Name != "Pixel 8" || sessions[0].Client != "Cineva Android 0.1" {
		t.Fatalf("sessions: %v %+v", err, sessions)
	}
	// The same install is the same device; another install another.
	_, again, _ := login(`{"username":"gary","password":"Correct-horse-2","bearer":true,"device":{"id":"install-1"}}`)
	if again.SessionID != res.SessionID {
		t.Errorf("same device id made another device: %s vs %s", again.SessionID, res.SessionID)
	}
	_, other, _ := login(`{"username":"gary","password":"Correct-horse-2","bearer":true,"device":{"id":"install-2","name":"Tablet"}}`)
	if other.SessionID == res.SessionID {
		t.Error("a second install shares the first's device")
	}
	if code, _, _ := login(`{"username":"gary","password":"wrong","bearer":true}`); code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", code)
	}
	// A cookie sign-in is unchanged.
	code, _, rec = login(`{"username":"gary","password":"Correct-horse-2"}`)
	if code != 200 || len(rec.Result().Cookies()) == 0 {
		t.Errorf("cookie login: %d cookies %d", code, len(rec.Result().Cookies()))
	}

	// The token authenticates a request as the session, device included,
	// and a logout ends it.
	call := func(token string, h echo.HandlerFunc) (int, echo.Context) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		if err := s.authenticate(h)(c); err != nil {
			var he *echo.HTTPError
			if errors.As(err, &he) {
				return he.Code, c
			}
			t.Fatal(err)
		}
		return rec.Code, c
	}
	code, c := call(again.Token, func(c echo.Context) error { return c.NoContent(http.StatusOK) })
	if code != 200 || currentUser(c).ID != u.ID || sessionIDOf(c) != res.SessionID {
		t.Errorf("bearer request: %d user %s session %s", code, currentUser(c).ID, sessionIDOf(c))
	}
	if code, _ := call("not-a-token", func(c echo.Context) error { return nil }); code != http.StatusUnauthorized {
		t.Errorf("bad token: %d", code)
	}
	if code, _ := call(again.Token, s.authLogout); code != http.StatusNoContent {
		t.Errorf("logout: %d", code)
	}
	if code, _ := call(again.Token, func(c echo.Context) error { return nil }); code != http.StatusUnauthorized {
		t.Errorf("after logout: %d", code)
	}
}

// Download jobs: originals are ready at once, transcodes are prepared in
// the background, access is the user's, and the ids list is complete.
func TestDownloadJobs(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	music, err := st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := st.CreateLibrary(ctx, "Hidden", store.MediaMusic, []string{"/h"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryAccess(ctx, hidden.ID, store.LibraryAccess{Restricted: true}); err != nil {
		t.Fatal(err)
	}
	dir := music.Paths[0]
	artist, _ := st.UpsertItem(ctx, store.Item{LibraryID: music.ID, Kind: store.KindArtist, Title: "Alpha", Artist: "Alpha", Path: "artist://alpha"})
	var saves []store.TrackSave
	for i, title := range []string{"One", "Two"} {
		p := filepath.Join(dir, title+".flac")
		if err := os.WriteFile(p, []byte(strings.Repeat("x", 100+i)), 0o644); err != nil {
			t.Fatal(err)
		}
		saves = append(saves, store.TrackSave{Item: store.Item{LibraryID: music.ID, Kind: store.KindTrack, Title: title,
			Artist: "Alpha", TrackNum: i + 1, Path: p, SizeBytes: int64(100 + i), RuntimeSecs: 200}})
	}
	album, tracks, err := st.SaveAlbum(ctx, store.Item{LibraryID: music.ID, Kind: store.KindAlbum, ParentID: artist.ID,
		Title: "First", Artist: "Alpha", Path: filepath.Join(dir, "First")}, saves)
	if err != nil {
		t.Fatal(err)
	}
	hArtist, _ := st.UpsertItem(ctx, store.Item{LibraryID: hidden.ID, Kind: store.KindArtist, Title: "Beta", Path: "artist://beta"})
	kid, err := st.CreateUser(ctx, "kid", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	kid.LibraryIDs = []string{music.ID}

	prepared := make(chan string, 4)
	s := &server{store: st, events: events.NewBus()}
	s.prepareDownload = func(_ context.Context, item store.Item, format string, kbps int) (int64, error) {
		prepared <- item.Title
		if item.Title == "Two" {
			return 0, errors.New("encoder exploded")
		}
		return 555, nil
	}
	e := echo.New()
	call := func(method, path, id, body string, h echo.HandlerFunc) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		if id != "" {
			c.SetParamNames("id")
			c.SetParamValues(id)
		}
		c.Set(ctxUser, kid)
		c.Set(ctxSessionID, "device-1")
		if err := h(c); err != nil {
			var he *echo.HTTPError
			if errors.As(err, &he) {
				return he.Code, ""
			}
			t.Fatal(err)
		}
		return rec.Code, rec.Body.String()
	}

	// Originals from an album: ready at once, with sizes and URLs.
	code, body := call(http.MethodPost, "/downloads", "", `{"itemId":"`+album.ID+`","profile":"original"}`, s.createDownloads)
	var jobs []store.DownloadJob
	if err := json.Unmarshal([]byte(body), &jobs); err != nil || code != http.StatusCreated {
		t.Fatalf("originals: %d %s %v", code, body, err)
	}
	if len(jobs) != 2 || jobs[0].Status != store.DownloadReady || jobs[0].Bytes != 100 || jobs[0].URL == "" || jobs[0].DeviceID != "device-1" {
		t.Errorf("original jobs %+v", jobs)
	}
	code, body = call(http.MethodGet, "/", jobs[0].ID, "", s.downloadFile)
	if code != 200 || body != strings.Repeat("x", 100) {
		t.Errorf("original file: %d %q", code, body)
	}

	// Transcodes: queued, prepared in the background, one ready, one failed.
	code, body = call(http.MethodPost, "/downloads", "", `{"itemIds":["`+tracks[0].ID+`","`+tracks[1].ID+`"],"profile":"opus-128"}`, s.createDownloads)
	jobs = nil // a decode into a used slice keeps old fields
	if err := json.Unmarshal([]byte(body), &jobs); err != nil || code != http.StatusCreated || len(jobs) != 2 {
		t.Fatalf("transcodes: %d %s %v", code, body, err)
	}
	if jobs[0].Status != store.DownloadQueued || jobs[0].URL != "" {
		t.Errorf("queued job %+v", jobs[0])
	}
	for i := 0; i < 2; i++ {
		select {
		case <-prepared:
		case <-time.After(5 * time.Second):
			t.Fatal("prepare not called")
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	var one, two store.DownloadJob
	for time.Now().Before(deadline) {
		_, b1 := call(http.MethodGet, "/", jobs[0].ID, "", s.getDownload)
		_, b2 := call(http.MethodGet, "/", jobs[1].ID, "", s.getDownload)
		_ = json.Unmarshal([]byte(b1), &one)
		_ = json.Unmarshal([]byte(b2), &two)
		if one.Status == store.DownloadReady && two.Status == store.DownloadFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if one.Status != store.DownloadReady || one.Bytes != 555 || one.URL == "" {
		t.Errorf("ready transcode %+v", one)
	}
	if two.Status != store.DownloadFailed || two.Error != "encoder exploded" {
		t.Errorf("failed transcode %+v", two)
	}
	if code, _ := call(http.MethodGet, "/", two.ID, "", s.downloadFile); code != http.StatusConflict {
		t.Errorf("failed job's file: %d", code)
	}

	// Access, profiles and the list.
	if code, _ := call(http.MethodPost, "/downloads", "", `{"itemId":"`+hArtist.ID+`","profile":"original"}`, s.createDownloads); code != http.StatusNotFound {
		t.Errorf("hidden item: %d", code)
	}
	if code, _ := call(http.MethodPost, "/downloads", "", `{"itemId":"`+tracks[0].ID+`","profile":"mp4-720"}`, s.createDownloads); code != http.StatusBadRequest {
		t.Errorf("video profile: %d", code)
	}
	if code, _ := call(http.MethodPost, "/downloads", "", `{"itemId":"`+tracks[0].ID+`","profile":"flac-fast"}`, s.createDownloads); code != http.StatusBadRequest {
		t.Errorf("unknown profile: %d", code)
	}
	code, body = call(http.MethodGet, "/downloads", "", "", s.listDownloads)
	jobs = nil
	if err := json.Unmarshal([]byte(body), &jobs); err != nil || code != 200 || len(jobs) != 4 {
		t.Errorf("list: %d %s", code, body)
	}
	if code, _ := call(http.MethodDelete, "/", one.ID, "", s.deleteDownload); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if code, _ := call(http.MethodGet, "/", one.ID, "", s.getDownload); code != http.StatusNotFound {
		t.Errorf("after delete: %d", code)
	}

	// The ids list is every id in the library, and a hidden one is 404.
	code, body = call(http.MethodGet, "/", music.ID, "", s.libraryIDs)
	var ids []string
	if err := json.Unmarshal([]byte(body), &ids); err != nil || code != 200 || len(ids) != 4 {
		t.Errorf("ids: %d %s", code, body)
	}
	if code, _ := call(http.MethodGet, "/", hidden.ID, "", s.libraryIDs); code != http.StatusNotFound {
		t.Errorf("hidden ids: %d", code)
	}
}
