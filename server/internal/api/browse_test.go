package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/store"
)

// The music listings scope what the store returns to the caller: a hidden
// library is not found, a video library is refused, pages are clamped, and
// an artist's tracks come in one request.
func TestMusicListings(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	music, err := st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{"/music"})
	if err != nil {
		t.Fatal(err)
	}
	films, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{"/films"})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := st.CreateLibrary(ctx, "Grown-up music", store.MediaMusic, []string{"/grown"})
	if err != nil {
		t.Fatal(err)
	}
	artist, err := st.UpsertItem(ctx, store.Item{LibraryID: music.ID, Kind: store.KindArtist, Title: "Alpha", Artist: "Alpha", Path: "artist://alpha"})
	if err != nil {
		t.Fatal(err)
	}
	var tracks []store.TrackSave
	for i, title := range []string{"One", "Two", "Three"} {
		tracks = append(tracks, store.TrackSave{Item: store.Item{LibraryID: music.ID, Kind: store.KindTrack, Title: title,
			Artist: "Alpha", TrackNum: i + 1, Path: "/music/Alpha/First/" + title + ".flac", RuntimeSecs: 200},
			Lyrics: &store.Lyrics{Text: title + " words"}})
	}
	album, saved, err := st.SaveAlbum(ctx, store.Item{LibraryID: music.ID, Kind: store.KindAlbum, ParentID: artist.ID,
		Title: "First", Artist: "Alpha", Year: 2001, Path: "/music/Alpha/First"}, tracks)
	if err != nil {
		t.Fatal(err)
	}
	hArtist, err := st.UpsertItem(ctx, store.Item{LibraryID: hidden.ID, Kind: store.KindArtist, Title: "Beta", Path: "artist://beta"})
	if err != nil {
		t.Fatal(err)
	}
	kid, err := st.CreateUser(ctx, "kid", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	kid.LibraryIDs = []string{music.ID, films.ID}
	s := &server{store: st}

	call := func(h echo.HandlerFunc, id, query string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(id)
		c.Set(ctxUser, kid)
		if err := h(c); err != nil {
			var he *echo.HTTPError
			if errors.As(err, &he) {
				return he.Code, ""
			}
			t.Fatalf("handler: %v", err)
		}
		return rec.Code, rec.Body.String()
	}

	code, body := call(s.libraryArtists, music.ID, "sort=name")
	var page struct {
		Items []store.Item   `json:"items"`
		Total int            `json:"total"`
		Size  int            `json:"size"`
		Index map[string]int `json:"index"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil || code != http.StatusOK {
		t.Fatalf("artists: %d %s %v", code, body, err)
	}
	if page.Total != 1 || page.Items[0].ChildCount != 1 || page.Index["A"] != 1 || page.Size != musicDefaultSize {
		t.Errorf("artists page: %+v", page)
	}
	if code, _ := call(s.libraryArtists, hidden.ID, ""); code != http.StatusNotFound {
		t.Errorf("hidden library: %d", code)
	}
	if code, _ := call(s.libraryArtists, films.ID, ""); code != http.StatusBadRequest {
		t.Errorf("film library: %d", code)
	}
	_ = hArtist

	code, body = call(s.libraryTracks, music.ID, "size=5000&sort=album")
	if err := json.Unmarshal([]byte(body), &page); err != nil || code != http.StatusOK {
		t.Fatalf("tracks: %d %s %v", code, body, err)
	}
	if page.Size != musicMaxTrackSize || page.Total != 3 || page.Items[0].Album != "First" {
		t.Errorf("tracks page: size %d total %d first %+v", page.Size, page.Total, page.Items[0])
	}
	code, body = call(s.libraryAlbums, music.ID, "artist="+artist.ID+"&size=0")
	if err := json.Unmarshal([]byte(body), &page); err != nil || code != http.StatusOK {
		t.Fatalf("albums: %d %s %v", code, body, err)
	}
	if page.Size != 1 || page.Total != 1 || page.Items[0].ID != album.ID {
		t.Errorf("albums page: %+v", page)
	}

	code, body = call(s.librarySearch, music.ID, "q=tw")
	var found store.LibrarySearch
	if err := json.Unmarshal([]byte(body), &found); err != nil || code != http.StatusOK {
		t.Fatalf("search: %d %s %v", code, body, err)
	}
	if len(found.Tracks) != 1 || found.Tracks[0].Title != "Two" || len(found.Artists) != 0 {
		t.Errorf("search: %+v", found)
	}
	if code, body := call(s.librarySearch, music.ID, "q=t"); code != http.StatusOK || body == "" {
		t.Errorf("short query: %d %s", code, body)
	}

	code, body = call(s.itemTracks, artist.ID, "")
	var list []store.Item
	if err := json.Unmarshal([]byte(body), &list); err != nil || code != http.StatusOK {
		t.Fatalf("item tracks: %d %s %v", code, body, err)
	}
	if len(list) != 3 || list[0].ID != saved[0].ID || list[2].ID != saved[2].ID {
		t.Errorf("artist's tracks: %+v", list)
	}
	if code, _ := call(s.itemTracks, saved[0].ID, ""); code != http.StatusBadRequest {
		t.Errorf("a track's tracks: %d", code)
	}
	if code, _ := call(s.itemTracks, hArtist.ID, ""); code != http.StatusNotFound {
		t.Errorf("hidden artist's tracks: %d", code)
	}

	code, body = call(s.itemLyrics, saved[1].ID, "")
	if code != http.StatusOK || body == "" {
		t.Errorf("lyrics: %d %s", code, body)
	}
	if code, _ := call(s.itemLyrics, artist.ID, ""); code != http.StatusNotFound {
		t.Errorf("no lyrics: %d", code)
	}
}
