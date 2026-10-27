package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/mix"
	"github.com/garybowers/cineva/server/internal/store"
)

// A source that knows nothing: the handler's checks are what is tested.
type noSource struct{}

func (noSource) SimilarArtists(context.Context, string) ([]store.SimilarArtist, error) {
	return nil, mix.ErrNotFound
}
func (noSource) TopRecordings(context.Context, string) ([]store.TopRecording, error) {
	return nil, mix.ErrNotFound
}

// The mix handler scopes the engine to the caller: a hidden seed is not
// found, a film cannot seed a mix, and the batch carries the seed, the
// basis and tracks the caller may see.
func TestItemMix(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	music, _ := st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{"/m"})
	hidden, _ := st.CreateLibrary(ctx, "Grown-up", store.MediaMusic, []string{"/g"})
	films, _ := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{"/f"})
	if err := st.SetLibraryAccess(ctx, hidden.ID, store.LibraryAccess{Restricted: true}); err != nil {
		t.Fatal(err)
	}
	artist := func(lib store.Library, name, mbid string) store.Item {
		it, err := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindArtist, Title: name, Artist: name, MBID: mbid, Path: "artist://" + name})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	abba := artist(music, "ABBA", "mb-abba")
	queen := artist(music, "Queen", "mb-queen")
	loud := artist(hidden, "Loud", "mb-loud")
	album := func(parent store.Item, title string, tracks ...string) []store.Item {
		var saves []store.TrackSave
		for i, tt := range tracks {
			saves = append(saves, store.TrackSave{Item: store.Item{LibraryID: parent.LibraryID, Kind: store.KindTrack, Title: tt, Artist: parent.Title,
				TrackNum: i + 1, RuntimeSecs: 200, Path: "/x/" + parent.Title + "/" + title + "/" + tt}})
		}
		_, saved, err := st.SaveAlbum(ctx, store.Item{LibraryID: parent.LibraryID, Kind: store.KindAlbum, ParentID: parent.ID, Title: title, Artist: parent.Title, Path: "/x/" + parent.Title + "/" + title}, saves)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	abbaTracks := album(abba, "Arrival", "Dancing Queen", "Fernando", "Knowing Me, Knowing You")
	queenTracks := album(queen, "A Night at the Opera", "Bohemian Rhapsody", "Love of My Life")
	loudTracks := album(loud, "Loud", "Noise")
	film, _ := st.UpsertItem(ctx, store.Item{LibraryID: films.ID, Kind: store.KindMovie, Title: "Heat", Path: "/f/heat.mkv"})
	if err := st.SetSimilarArtists(ctx, "mb-abba", []store.SimilarArtist{{MBID: "mb-queen", Name: "Queen", Score: 100}, {MBID: "mb-loud", Name: "Loud", Score: 90}}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	kid, _ := st.CreateUser(ctx, "kid", "", "h", store.RoleUser)
	kid.LibraryIDs = []string{music.ID, films.ID}
	admin, _ := st.CreateUser(ctx, "admin", "", "h", store.RoleAdmin)

	s := &server{store: st, mixes: mix.New(st, noSource{})}
	call := func(u store.User, id, query string) (int, mix.Result) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(id)
		c.Set(ctxUser, u)
		var res mix.Result
		if err := s.itemMix(c); err != nil {
			var he *echo.HTTPError
			if errors.As(err, &he) {
				return he.Code, res
			}
			t.Fatalf("handler: %v", err)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v %s", err, rec.Body.String())
		}
		return rec.Code, res
	}

	code, res := call(kid, abbaTracks[0].ID, "size=5&exclude="+abbaTracks[1].ID+",junk")
	if code != http.StatusOK || res.Seed.ID != abbaTracks[0].ID {
		t.Fatalf("mix: %d %+v", code, res.Seed)
	}
	for _, tr := range res.Tracks {
		if tr.LibraryID == hidden.ID || tr.ID == abbaTracks[1].ID || tr.ID == abbaTracks[0].ID {
			t.Errorf("unwanted track %q in the kid's mix", tr.Title)
		}
	}
	if len(res.Tracks) == 0 || len(res.Tracks) > 5 {
		t.Errorf("size: %d tracks", len(res.Tracks))
	}
	// The admin sees the hidden library's tracks in the same mix.
	_, res = call(admin, abbaTracks[0].ID, "size=20")
	found := false
	for _, tr := range res.Tracks {
		if tr.ID == loudTracks[0].ID {
			found = true
		}
	}
	if !found {
		t.Errorf("admin's mix lacks the hidden library: %+v", res.Tracks)
	}
	if code, _ := call(kid, loudTracks[0].ID, ""); code != http.StatusNotFound {
		t.Errorf("hidden seed: %d", code)
	}
	if code, _ := call(kid, "nope", ""); code != http.StatusNotFound {
		t.Errorf("unknown seed: %d", code)
	}
	if code, _ := call(kid, film.ID, ""); code != http.StatusBadRequest {
		t.Errorf("film seed: %d", code)
	}
	// start=true on a track puts it first; after= is accepted.
	code, res = call(kid, queenTracks[1].ID, "start=true&after="+abbaTracks[2].ID)
	if code != http.StatusOK || len(res.Tracks) == 0 || res.Tracks[0].ID != queenTracks[1].ID {
		t.Errorf("start: %d %v", code, res.Tracks)
	}
}
