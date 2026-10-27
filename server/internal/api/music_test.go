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

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/store"
)

func TestCountsAsPlay(t *testing.T) {
	tests := []struct {
		name               string
		kind               string
		position, duration int
		completed          bool
		want               bool
	}{
		{"track skipped after the intro", "track", 20, 200, false, false},
		{"track played half way", "track", 100, 200, false, true},
		{"long track, four minutes in", "track", 240, 1200, false, true},
		{"short track, not finished", "track", 20, 25, false, false},
		{"short track, finished", "track", 25, 25, true, true},
		{"film part watched", "movie", 3000, 7200, false, false},
		{"film watched", "movie", 7000, 7200, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := countsAsPlay(tc.kind, tc.position, tc.duration, tc.completed); got != tc.want {
				t.Fatalf("countsAsPlay = %v, want %v", got, tc.want)
			}
		})
	}
}

// Playlists are the user's own, carry only what they may see, and refuse
// rules that cannot run.
func TestPlaylistHandlers(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	open, err := st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{"/music"})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := st.CreateLibrary(ctx, "Grown-up music", store.MediaMusic, []string{"/grown"})
	if err != nil {
		t.Fatal(err)
	}
	save := func(it store.Item) store.Item {
		t.Helper()
		saved, err := st.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	artist := save(store.Item{LibraryID: open.ID, Kind: store.KindArtist, Title: "Alpha", Path: "artist://alpha"})
	album := save(store.Item{LibraryID: open.ID, Kind: store.KindAlbum, ParentID: artist.ID, Title: "First", Artist: "Alpha", Path: "/music/Alpha/First"})
	one := save(store.Item{LibraryID: open.ID, Kind: store.KindTrack, ParentID: album.ID, Title: "One", Artist: "Alpha", TrackNum: 1, Path: "/music/Alpha/First/01.flac", RuntimeSecs: 200})
	two := save(store.Item{LibraryID: open.ID, Kind: store.KindTrack, ParentID: album.ID, Title: "Two", Artist: "Alpha", TrackNum: 2, Path: "/music/Alpha/First/02.flac", RuntimeSecs: 200})
	hArtist := save(store.Item{LibraryID: hidden.ID, Kind: store.KindArtist, Title: "Beta", Path: "artist://beta"})
	hAlbum := save(store.Item{LibraryID: hidden.ID, Kind: store.KindAlbum, ParentID: hArtist.ID, Title: "Loud", Path: "/grown/Beta/Loud"})
	secret := save(store.Item{LibraryID: hidden.ID, Kind: store.KindTrack, ParentID: hAlbum.ID, Title: "Secret", Path: "/grown/Beta/Loud/01.flac"})

	kid, err := st.CreateUser(ctx, "kid", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	kid.LibraryIDs = []string{open.ID}
	other, err := st.CreateUser(ctx, "other", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	other.LibraryIDs = []string{open.ID}
	s := &server{store: st}

	call := func(u store.User, h echo.HandlerFunc, method, id, body string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		if id != "" {
			c.SetParamNames("id")
			c.SetParamValues(id)
		}
		c.Set(ctxUser, u)
		err := h(c)
		var he *echo.HTTPError
		if errors.As(err, &he) {
			return he.Code, ""
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return rec.Code, rec.Body.String()
	}

	code, body := call(kid, s.createPlaylist, http.MethodPost, "", `{"name":"Mine"}`)
	if code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	var p store.Playlist
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}

	// The album expands to its tracks; the hidden track is skipped.
	code, body = call(kid, s.appendPlaylistItems, http.MethodPost, p.ID,
		`{"itemIds":["`+album.ID+`","`+secret.ID+`"]}`)
	if code != http.StatusOK {
		t.Fatalf("append: %d", code)
	}
	var detail playlistDetail
	if err := json.Unmarshal([]byte(body), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Tracks) != 2 || detail.Tracks[0].ID != one.ID || detail.Tracks[1].ID != two.ID {
		t.Fatalf("tracks = %+v, want One, Two and not the hidden track", detail.Tracks)
	}

	// Reorder.
	if code, body = call(kid, s.setPlaylistItems, http.MethodPut, p.ID, `{"itemIds":["`+two.ID+`","`+one.ID+`"]}`); code != http.StatusOK {
		t.Fatalf("reorder: %d", code)
	}
	if err := json.Unmarshal([]byte(body), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Tracks[0].ID != two.ID {
		t.Fatalf("after reorder first = %s, want Two", detail.Tracks[0].Title)
	}

	// Someone else's playlist does not exist, as far as they can tell.
	if code, _ := call(other, s.getPlaylist, http.MethodGet, p.ID, ""); code != http.StatusNotFound {
		t.Fatalf("other user's read: %d, want 404", code)
	}
	if code, _ := call(other, s.deletePlaylist, http.MethodDelete, p.ID, ""); code != http.StatusNotFound {
		t.Fatalf("other user's delete: %d, want 404", code)
	}
	if code, body := call(other, s.listPlaylists, http.MethodGet, "", ""); code != http.StatusOK || strings.TrimSpace(body) != "[]" {
		t.Fatalf("other user's list: %d %s, want empty", code, body)
	}

	// Rules that cannot run are refused; good ones are evaluated.
	if code, _ := call(kid, s.createPlaylist, http.MethodPost, "", `{"name":"Bad","smart":{"rules":[{"field":"mood","op":"is","value":"x"}]}}`); code != http.StatusBadRequest {
		t.Fatalf("bad rules: %d, want 400", code)
	}
	code, body = call(kid, s.createPlaylist, http.MethodPost, "", `{"name":"Alpha","smart":{"rules":[{"field":"artist","op":"is","value":"Alpha"}],"sort":"title"}}`)
	if code != http.StatusCreated {
		t.Fatalf("smart create: %d", code)
	}
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	if code, body = call(kid, s.getPlaylist, http.MethodGet, p.ID, ""); code != http.StatusOK {
		t.Fatalf("smart read: %d", code)
	}
	if err := json.Unmarshal([]byte(body), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Tracks) != 2 || detail.Playlist.Smart == nil {
		t.Fatalf("smart playlist = %+v", detail)
	}
	if code, _ := call(kid, s.appendPlaylistItems, http.MethodPost, p.ID, `{"itemIds":["`+one.ID+`"]}`); code != http.StatusBadRequest {
		t.Fatalf("adding to a smart playlist: %d, want 400", code)
	}

	// The artist page lists the album, and nothing from a hidden library.
	code, body = call(kid, s.getArtist, http.MethodGet, artist.ID, "")
	if code != http.StatusOK {
		t.Fatalf("artist: %d", code)
	}
	var page artistPage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Albums) != 1 || page.Stats.Tracks != 2 || page.LookingUp {
		t.Fatalf("artist page = %+v", page)
	}
	if code, _ := call(kid, s.getArtist, http.MethodGet, hArtist.ID, ""); code != http.StatusNotFound {
		t.Fatalf("hidden artist: %d, want 404", code)
	}
	if code, _ := call(kid, s.getArtist, http.MethodGet, album.ID, ""); code != http.StatusBadRequest {
		t.Fatalf("album as artist: %d, want 400", code)
	}
}

// The picker's listing and the undo of an append (server/docs/mix.md).
func TestPlaylistPicker(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	lib, err := st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{"/music"})
	if err != nil {
		t.Fatal(err)
	}
	artist, _ := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindArtist, Title: "Alpha", Path: "artist://alpha"})
	album, _ := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindAlbum, ParentID: artist.ID, Title: "First", Artist: "Alpha", Path: "/music/Alpha/First"})
	one, _ := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindTrack, ParentID: album.ID, Title: "One", Artist: "Alpha", TrackNum: 1, Path: "/music/Alpha/First/01.flac", RuntimeSecs: 200})
	two, _ := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindTrack, ParentID: album.ID, Title: "Two", Artist: "Alpha", TrackNum: 2, Path: "/music/Alpha/First/02.flac", RuntimeSecs: 200})
	kid, err := st.CreateUser(ctx, "kid", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	kid.LibraryIDs = []string{lib.ID}
	s := &server{store: st}
	call := func(h echo.HandlerFunc, method, target, id, body string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		if id != "" {
			c.SetParamNames("id")
			c.SetParamValues(id)
		}
		c.Set(ctxUser, kid)
		err := h(c)
		var he *echo.HTTPError
		if errors.As(err, &he) {
			return he.Code, ""
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return rec.Code, rec.Body.String()
	}
	var gym, zed store.Playlist
	_, body := call(s.createPlaylist, http.MethodPost, "/", "", `{"name":"Zed"}`)
	if err := json.Unmarshal([]byte(body), &zed); err != nil {
		t.Fatal(err)
	}
	_, body = call(s.createPlaylist, http.MethodPost, "/", "", `{"name":"Gym"}`)
	if err := json.Unmarshal([]byte(body), &gym); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(s.appendPlaylistItems, http.MethodPost, "/", gym.ID, `{"itemIds":["`+one.ID+`","`+two.ID+`"]}`); code != http.StatusOK {
		t.Fatalf("append: %d", code)
	}
	code, body := call(s.listPlaylists, http.MethodGet, "/?for="+one.ID, "", "")
	var lists []store.Playlist
	if err := json.Unmarshal([]byte(body), &lists); err != nil || code != http.StatusOK {
		t.Fatalf("for: %d %s %v", code, body, err)
	}
	if len(lists) != 2 || lists[0].Name != "Gym" || !lists[0].Holds || lists[1].Holds {
		t.Fatalf("picker listing: %+v", lists)
	}
	code, body = call(s.removeLastPlaylistItems, http.MethodDelete, "/?count=1", gym.ID, "")
	if code != http.StatusOK || !strings.Contains(body, `"removed":1`) {
		t.Fatalf("undo: %d %s", code, body)
	}
	_, body = call(s.getPlaylist, http.MethodGet, "/", gym.ID, "")
	if !strings.Contains(body, `"title":"One"`) || strings.Contains(body, `"title":"Two"`) {
		t.Fatalf("after undo: %s", body)
	}
	if code, _ := call(s.removeLastPlaylistItems, http.MethodDelete, "/", "nope", ""); code != http.StatusNotFound {
		t.Fatalf("unknown playlist: %d", code)
	}
}
