package mix

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/store"
)

// fakeLB stands in for ListenBrainz: similar artists and top recordings
// by artist id, and a count of requests so the cache can be checked.
type fakeLB struct {
	similar  map[string][]map[string]any
	top      map[string][]map[string]any
	requests atomic.Int32
	srv      *httptest.Server
}

func newFakeLB(t *testing.T) *fakeLB {
	t.Helper()
	f := &fakeLB{similar: map[string][]map[string]any{}, top: map[string][]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "no user agent", http.StatusBadRequest)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/similar-artists/json"):
			mbid := r.URL.Query().Get("artist_mbids")
			if r.URL.Query().Get("algorithm") == "" {
				http.Error(w, "algorithm", http.StatusBadRequest)
				return
			}
			list, ok := f.similar[mbid]
			if !ok {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, "[]")
				return
			}
			json.NewEncoder(w).Encode(list)
		case strings.HasPrefix(r.URL.Path, "/1/popularity/top-recordings-for-artist/"):
			mbid := strings.TrimPrefix(r.URL.Path, "/1/popularity/top-recordings-for-artist/")
			list, ok := f.top[mbid]
			if !ok {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(list)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLB) client() *ListenBrainz {
	lb := NewListenBrainz()
	lb.LabsBase, lb.APIBase, lb.Pace = f.srv.URL, f.srv.URL, time.Millisecond
	return lb
}

// fixture is a library with ABBA (the seed), Queen (similar by id), The
// Beatles (similar, found by name), Elton John (not similar), Kids Choir
// in a library the viewer cannot see, and enough tracks to diversify.
type fixture struct {
	st                                   *store.Store
	lib, hidden                          store.Library
	user, other                          store.User
	abba, queen, beatles, elton, kids    store.Item
	arrival, voyage, opera, abbey, songs store.Item
	tracks                               map[string]store.Item
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mix.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f := &fixture{st: st, tracks: map[string]store.Item{}}
	if f.lib, err = st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{"/m"}); err != nil {
		t.Fatal(err)
	}
	if f.hidden, err = st.CreateLibrary(ctx, "Kids", store.MediaMusic, []string{"/k"}); err != nil {
		t.Fatal(err)
	}
	if f.user, err = st.CreateUser(ctx, "gary", "", "h", store.RoleUser); err != nil {
		t.Fatal(err)
	}
	if f.other, err = st.CreateUser(ctx, "rob", "", "h", store.RoleUser); err != nil {
		t.Fatal(err)
	}
	artist := func(lib store.Library, name, mbid string) store.Item {
		it, err := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindArtist, Title: name, Artist: name, MBID: mbid, Path: "artist://" + name})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	f.abba = artist(f.lib, "ABBA", "mb-abba")
	f.queen = artist(f.lib, "Queen", "mb-queen")
	f.beatles = artist(f.lib, "The Beatles", "")
	f.elton = artist(f.lib, "Elton John", "mb-elton")
	f.kids = artist(f.hidden, "Kids Choir", "mb-kids")
	album := func(parent store.Item, title string, year int, genres []string, tracks ...string) store.Item {
		t.Helper()
		var saves []store.TrackSave
		for i, tt := range tracks {
			title, mbid, _ := strings.Cut(tt, "#")
			saves = append(saves, store.TrackSave{Item: store.Item{LibraryID: parent.LibraryID, Kind: store.KindTrack, Title: title, Artist: parent.Title,
				TrackNum: i + 1, Year: year, RuntimeSecs: 200, MBID: mbid, Path: "/x/" + parent.Title + "/" + title + "/" + fmt.Sprint(i)}, Genres: genres})
		}
		al, saved, err := st.SaveAlbum(ctx, store.Item{LibraryID: parent.LibraryID, Kind: store.KindAlbum, ParentID: parent.ID, Title: title,
			Artist: parent.Title, Year: year, Path: "/x/" + parent.Title + "/" + title}, saves)
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range saved {
			f.tracks[tr.Title] = tr
		}
		return al
	}
	f.arrival = album(f.abba, "Arrival", 1976, []string{"Pop", "Disco"}, "Dancing Queen#rec-dq", "Fernando", "Money, Money, Money", "Knowing Me, Knowing You", "That's Me")
	f.voyage = album(f.abba, "Voyage", 2021, []string{"Pop"}, "I Still Have Faith in You", "Don't Shut Me Down", "Just a Notion")
	f.opera = album(f.queen, "A Night at the Opera", 1975, []string{"Rock"}, "Bohemian Rhapsody#rec-bo", "Love of My Life", "You're My Best Friend", "'39", "Death on Two Legs", "Lazing on a Sunday Afternoon")
	f.abbey = album(f.beatles, "Abbey Road", 1969, []string{"Rock", "Pop"}, "Come Together", "Something", "Here Comes the Sun", "Octopus's Garden")
	album(f.elton, "Goodbye Yellow Brick Road", 1973, []string{"Pop"}, "Candle in the Wind", "Bennie and the Jets")
	f.songs = album(f.kids, "Songs", 2010, []string{"Pop"}, "Wheels on the Bus")
	return f
}

func (f *fixture) engine(lb *fakeLB) *Engine {
	e := New(f.st, lb.client())
	e.Rand = func() float64 { return 0 } // deterministic
	return e
}

func titles(items []store.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}

func artistsOf(items []store.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.AlbumArtist
	}
	return out
}

func TestMixSimilarArtistsAndCache(t *testing.T) {
	f := newFixture(t)
	lb := newFakeLB(t)
	lb.similar["mb-abba"] = []map[string]any{
		{"artist_mbid": "mb-queen", "name": "Queen", "score": 4706},
		{"artist_mbid": "mb-beatles", "name": "The Beatles", "score": 3955}, // found by name: the library's row has no id
		{"artist_mbid": "mb-kids", "name": "Kids Choir", "score": 3000},     // a library the viewer cannot see
		{"artist_mbid": "mb-nobody", "name": "Nobody Here", "score": 2000},  // not in the library
	}
	lb.top["mb-queen"] = []map[string]any{
		{"recording_mbid": "rec-bo", "recording_name": "Bohemian Rhapsody"},
		{"recording_mbid": "rec-x", "recording_name": "Love of My Life"}, // matched by title
	}
	e := f.engine(lb)
	ctx := context.Background()
	libs := []string{f.lib.ID}
	seed := f.tracks["Dancing Queen"]

	// Nothing cached yet: the first batch has no similar artists and
	// starts the lookups in the background.
	first, err := e.Mix(ctx, f.user.ID, seed, Options{LibraryIDs: libs})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing is known, so it falls back to genre and the seed's own
	// artist, and says so.
	if slices.Contains(first.Basis, BasisSimilar) || !slices.Contains(first.Basis, BasisGenre) {
		t.Errorf("first batch's basis: %v", first.Basis)
	}
	e.Wait()
	if n := lb.requests.Load(); n != 2 { // similar artists for ABBA, top songs for ABBA (a miss)
		t.Errorf("requests after the first mix: %d", n)
	}

	// The second batch knows the similar artists, by id and by name,
	// never the hidden library's, and the cache answers without a request.
	second, err := e.Mix(ctx, f.user.ID, seed, Options{LibraryIDs: libs, Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(second.Basis, BasisSimilar) {
		t.Errorf("basis %v", second.Basis)
	}
	got := artistsOf(second.Tracks)
	if !slices.Contains(got, "Queen") || !slices.Contains(got, "The Beatles") {
		t.Errorf("similar artists missing from %v", got)
	}
	if slices.Contains(got, "Kids Choir") || slices.Contains(got, "Elton John") {
		t.Errorf("unwanted artists in %v", got)
	}
	if slices.Contains(titles(second.Tracks), "Dancing Queen") {
		t.Error("the seed itself in a carried-on mix")
	}
	e.Wait()
	before := lb.requests.Load()
	if _, err := e.Mix(ctx, f.user.ID, seed, Options{LibraryIDs: libs}); err != nil {
		t.Fatal(err)
	}
	e.Wait()
	if lb.requests.Load() != before {
		t.Errorf("a cached mix made %d more requests", lb.requests.Load()-before)
	}

	// Top songs: Queen's best-known lead its tracks, by recording id and
	// by title.
	third, _ := e.Mix(ctx, f.user.ID, seed, Options{LibraryIDs: libs, Size: 20})
	if !slices.Contains(third.Basis, BasisTopSongs) {
		t.Errorf("basis %v", third.Basis)
	}
	var queen []string
	for _, tr := range third.Tracks {
		if tr.AlbumArtist == "Queen" {
			queen = append(queen, tr.Title)
		}
	}
	if len(queen) < 2 || queen[0] != "Bohemian Rhapsody" || queen[1] != "Love of My Life" {
		t.Errorf("Queen's order %v", queen)
	}
}

func TestMixRules(t *testing.T) {
	f := newFixture(t)
	lb := newFakeLB(t)
	ctx := context.Background()
	libs := []string{f.lib.ID}
	now := time.Now().Unix()
	// Pre-filled caches, so no background work is involved.
	if err := f.st.SetSimilarArtists(ctx, "mb-abba", []store.SimilarArtist{{MBID: "mb-queen", Name: "Queen", Score: 100}, {MBID: "mb-beatles", Name: "The Beatles", Score: 80}}, now); err != nil {
		t.Fatal(err)
	}
	for _, mbid := range []string{"mb-abba", "mb-queen"} {
		if err := f.st.SetTopRecordings(ctx, mbid, nil, now); err != nil {
			t.Fatal(err)
		}
	}
	e := f.engine(lb)
	seed := f.tracks["Dancing Queen"]

	// Freshness and taste: a track played in the last three hours, an
	// excluded one and a one-star one are never picked; a rated-up one
	// comes early.
	ev, _ := f.st.RecordPlay(ctx, f.user.ID, f.tracks["Fernando"].ID, store.KindTrack)
	if err := f.st.FinishPlay(ctx, ev, 200, true, true); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetRating(ctx, f.user.ID, f.tracks["'39"].ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetRating(ctx, f.user.ID, f.tracks["Here Comes the Sun"].ID, 5); err != nil {
		t.Fatal(err)
	}
	res, err := e.Mix(ctx, f.user.ID, seed, Options{LibraryIDs: libs, Size: 20, Exclude: []string{f.tracks["Something"].ID}})
	if err != nil {
		t.Fatal(err)
	}
	got := titles(res.Tracks)
	for _, never := range []string{"Fernando", "'39", "Something", "Dancing Queen", "Wheels on the Bus"} {
		if slices.Contains(got, never) {
			t.Errorf("%q must not be picked: %v", never, got)
		}
	}
	if !slices.Contains(res.Basis, BasisHousehold) {
		t.Errorf("basis %v", res.Basis)
	}
	if idx := slices.Index(got, "Here Comes the Sun"); idx < 0 || idx > 3 {
		t.Errorf("the five-star track came %dth: %v", idx+1, got)
	}

	// Diversity: never two in a row by one artist and the seed's album at
	// most twice, until the small library has nothing else left and the
	// rules relax. Two tracks by one artist in a row may therefore only
	// happen once every other artist's tracks are used up, and a third
	// track from the seed's album only after every other track.
	artists := artistsOf(res.Tracks)
	for i := 1; i < len(artists); i++ {
		if artists[i] == artists[i-1] {
			for _, later := range artists[i+1:] {
				if later != artists[i] {
					t.Errorf("two in a row by %s at %d with others left: %v", artists[i], i, artists)
					break
				}
			}
		}
	}
	arrival := 0
	for i, tr := range res.Tracks {
		if tr.ParentID == f.arrival.ID {
			arrival++
			if arrival == 3 {
				for _, later := range res.Tracks[i+1:] {
					if later.ParentID != f.arrival.ID {
						t.Errorf("the seed's album a third time at %d with %q still to come: %v", i, later.Title, got)
						break
					}
				}
			}
		}
	}
	if len(res.Tracks) < 12 {
		t.Errorf("the batch stopped short at %d: %v", len(res.Tracks), got)
	}

	// Never: a track under 30 seconds, and the same recording twice.
	short, _ := f.st.UpsertItem(ctx, store.Item{LibraryID: f.lib.ID, Kind: store.KindTrack, ParentID: f.opera.ID, Title: "Intro", Artist: "Queen", RuntimeSecs: 12, Path: "/x/Queen/intro"})
	dup, _ := f.st.UpsertItem(ctx, store.Item{LibraryID: f.lib.ID, Kind: store.KindTrack, ParentID: f.abbey.ID, Title: "Bohemian Rhapsody", Artist: "The Beatles", MBID: "rec-bo", RuntimeSecs: 300, Path: "/x/dup"})
	res, _ = e.Mix(ctx, f.user.ID, seed, Options{LibraryIDs: libs, Size: 30})
	seen := 0
	for _, tr := range res.Tracks {
		if tr.ID == short.ID {
			t.Error("a 12-second track was picked")
		}
		if tr.MBID == "rec-bo" {
			seen++
		}
	}
	if seen > 1 {
		t.Errorf("the same recording twice (%s)", dup.ID)
	}
}

func TestMixFirstBatch(t *testing.T) {
	f := newFixture(t)
	lb := newFakeLB(t)
	ctx := context.Background()
	libs := []string{f.lib.ID}
	now := time.Now().Unix()
	if err := f.st.SetSimilarArtists(ctx, "mb-abba", []store.SimilarArtist{{MBID: "mb-queen", Name: "Queen", Score: 100}, {MBID: "mb-beatles", Name: "The Beatles", Score: 80}}, now); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetTopRecordings(ctx, "mb-abba", []store.TopRecording{{MBID: "rec-dq", Name: "Dancing Queen", Rank: 1}, {MBID: "rec-f", Name: "Fernando", Rank: 2}, {MBID: "rec-m", Name: "Money, Money, Money", Rank: 3}}, now); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetTopRecordings(ctx, "mb-queen", nil, now); err != nil {
		t.Fatal(err)
	}
	e := f.engine(lb)

	// A track seed started on purpose comes first.
	res, err := e.Mix(ctx, f.user.ID, f.tracks["That's Me"], Options{LibraryIDs: libs, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(res.Tracks); len(got) == 0 || got[0] != "That's Me" {
		t.Errorf("track seed: %v", got)
	}

	// An album seed opens with a few of its best, spread through, and
	// nothing else from the album.
	res, err = e.Mix(ctx, f.user.ID, f.arrival, Options{LibraryIDs: libs, Start: true, Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	var own []int
	for i, tr := range res.Tracks {
		if tr.ParentID == f.arrival.ID {
			own = append(own, i)
		}
	}
	if len(own) != 4 || own[0] != 0 {
		t.Errorf("album seed: own tracks at %v of %d: %v", own, len(res.Tracks), titles(res.Tracks))
	}
	if len(own) == 4 && own[3]-own[0] < 6 {
		t.Errorf("album seed: own tracks bunched at %v", own)
	}
	if res.Tracks[0].Title != "Dancing Queen" {
		t.Errorf("the album's best-known leads: %v", titles(res.Tracks))
	}

	// An artist seed leads with their best-known.
	res, err = e.Mix(ctx, f.user.ID, f.abba, Options{LibraryIDs: libs, Start: true, Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(res.Tracks); len(got) < 3 || got[0] != "Dancing Queen" || got[1] != "Fernando" || got[2] != "Money, Money, Money" {
		t.Errorf("artist seed: %v", got)
	}
	if res.Tracks[3].AlbumArtist == "ABBA" {
		t.Errorf("the seed artist ran on after the lead: %v", artistsOf(res.Tracks))
	}

	// The seed kind is checked.
	if _, err := e.Mix(ctx, f.user.ID, f.lib2Item(t), Options{LibraryIDs: libs}); err == nil {
		t.Error("a film seeded a mix")
	}
}

func (f *fixture) lib2Item(t *testing.T) store.Item {
	t.Helper()
	ctx := context.Background()
	films, err := f.st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{"/f"})
	if err != nil {
		t.Fatal(err)
	}
	it, err := f.st.UpsertItem(ctx, store.Item{LibraryID: films.ID, Kind: store.KindMovie, Title: "Heat", Path: "/f/heat.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestMixFallbackWithoutIDs(t *testing.T) {
	f := newFixture(t)
	lb := newFakeLB(t)
	ctx := context.Background()
	libs := []string{f.lib.ID}
	e := f.engine(lb)
	e.LookupArtist = func(a store.Item) bool { return true }

	// The Beatles have no MusicBrainz id and nothing cached: the mix falls
	// back to what the household played beside them and their genres.
	for _, title := range []string{"Come Together", "Candle in the Wind"} {
		ev, _ := f.st.RecordPlay(ctx, f.other.ID, f.tracks[title].ID, store.KindTrack)
		if err := f.st.FinishPlay(ctx, ev, 200, true, true); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.Mix(ctx, f.user.ID, f.tracks["Octopus's Garden"], Options{LibraryIDs: libs, Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(res.Basis, BasisSimilar) || !slices.Contains(res.Basis, BasisGenre) || !slices.Contains(res.Basis, BasisCoListen) {
		t.Errorf("fallback basis %v", res.Basis)
	}
	got := titles(res.Tracks)
	if !slices.Contains(got, "Candle in the Wind") {
		t.Errorf("co-listened track missing: %v", got)
	}
	if slices.Contains(got, "Wheels on the Bus") {
		t.Errorf("hidden library in the fallback: %v", got)
	}
	e.Wait()
	if lb.requests.Load() != 0 {
		t.Errorf("requests without an id: %d", lb.requests.Load())
	}
}

func TestListenBrainzClientParsing(t *testing.T) {
	lb := newFakeLB(t)
	lb.similar["mb-abba"] = []map[string]any{{"artist_mbid": "mb-queen", "name": "Queen", "score": 4706, "reference_mbid": "mb-abba"}}
	lb.top["mb-abba"] = []map[string]any{{"recording_mbid": "rec-dq", "recording_name": "Dancing Queen", "artist_name": "ABBA"}}
	c := lb.client()
	ctx := context.Background()
	sim, err := c.SimilarArtists(ctx, "mb-abba")
	if err != nil || len(sim) != 1 || sim[0].MBID != "mb-queen" || sim[0].Score != 4706 {
		t.Errorf("similar: %v %+v", err, sim)
	}
	if _, err := c.SimilarArtists(ctx, "mb-none"); err != ErrNotFound {
		t.Errorf("empty answer: %v", err)
	}
	top, err := c.TopRecordings(ctx, "mb-abba")
	if err != nil || len(top) != 1 || top[0].Rank != 1 || top[0].Name != "Dancing Queen" {
		t.Errorf("top: %v %+v", err, top)
	}
	if _, err := c.TopRecordings(ctx, "mb-none"); err != ErrNotFound {
		t.Errorf("404: %v", err)
	}
}
