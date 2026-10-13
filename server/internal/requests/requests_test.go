package requests

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/arr"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// fakeTMDB answers discovery from a fixed catalogue.
type fakeTMDB struct{}

func (fakeTMDB) SearchTitles(_ context.Context, q string) ([]metadata.DiscoverResult, error) {
	return []metadata.DiscoverResult{
		{MediaType: "movie", TmdbID: 1, Title: "Matrix (short)"},
		{MediaType: "movie", TmdbID: 603, Title: "The Matrix", Year: 1999, PosterURL: "p", Popularity: 50},
		{MediaType: "movie", TmdbID: 604, Title: "The Matrix Reloaded", Year: 2003, PosterURL: "p", Popularity: 80},
		{MediaType: "tv", TmdbID: 1396, Title: "Breaking Bad", Year: 2008, PosterURL: "p", Popularity: 10},
	}, nil
}

func (fakeTMDB) MovieDetail(_ context.Context, id int) (metadata.TitleDetail, error) {
	return metadata.TitleDetail{MediaType: "movie", VideoResult: metadata.VideoResult{
		TmdbID: id, Title: "Film " + strconv.Itoa(id), Year: 1999, PosterURL: "https://img/p.jpg",
		Cast:           []metadata.Person{{TmdbID: 1, Name: "Keanu Reeves", Character: "Neo"}},
		Certifications: map[string]string{"GB": "15"},
	}}, nil
}

func (fakeTMDB) TVDetail(_ context.Context, id int) (metadata.TitleDetail, error) {
	if id != 1396 {
		return metadata.TitleDetail{}, metadata.ErrNotFound
	}
	return metadata.TitleDetail{MediaType: "tv", TvdbID: 81189, VideoResult: metadata.VideoResult{TmdbID: id, Title: "Breaking Bad", Year: 2008},
		Seasons: []metadata.SeasonSummary{{Number: 0, Name: "Specials"}, {Number: 1, Name: "Season 1", EpisodeCount: 7}, {Number: 2, Name: "Season 2", EpisodeCount: 13}}}, nil
}

func (fakeTMDB) TVSeason(context.Context, int, int) (metadata.SeasonResult, error) {
	return metadata.SeasonResult{}, nil
}

// fakeArr is a Sonarr or Radarr that records what it was asked to do.
type fakeArr struct {
	mu       sync.Mutex
	down     bool
	movies   []map[string]any
	series   []map[string]any
	episodes []map[string]any
	queue    []map[string]any
	calls    []string
}

func (f *fakeArr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if f.down {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	enc := json.NewEncoder(w)
	q := r.URL.Query()
	switch p := r.URL.Path; {
	case p == "/api/v3/qualityprofile":
		_ = enc.Encode([]map[string]any{{"id": 4, "name": "HD"}})
	case p == "/api/v3/rootfolder":
		_ = enc.Encode([]map[string]any{{"id": 1, "path": "/media"}})
	case p == "/api/v3/queue":
		_ = enc.Encode(map[string]any{"records": f.queue})
	case p == "/api/v3/command", p == "/api/v3/episode/monitor", strings.HasPrefix(p, "/api/v3/series/") && r.Method == http.MethodPut:
		_ = enc.Encode(body)
	case p == "/api/v3/movie" && r.Method == http.MethodGet:
		_ = enc.Encode(f.movies)
	case p == "/api/v3/movie/lookup/tmdb":
		id, _ := strconv.Atoi(q.Get("tmdbId"))
		_ = enc.Encode(map[string]any{"title": "Film", "tmdbId": id})
	case p == "/api/v3/movie" && r.Method == http.MethodPost:
		body["id"] = 77
		f.movies = append(f.movies, body)
		_ = enc.Encode(body)
	case p == "/api/v3/series" && r.Method == http.MethodGet:
		_ = enc.Encode(f.series)
	case p == "/api/v3/series/lookup":
		_ = enc.Encode([]map[string]any{{"title": "Breaking Bad", "tvdbId": 81189, "seasons": []any{}}})
	case p == "/api/v3/series" && r.Method == http.MethodPost:
		body["id"] = 55
		f.series = append(f.series, body)
		_ = enc.Encode(body)
	case strings.HasPrefix(p, "/api/v3/series/"):
		_ = enc.Encode(f.series[0])
	case p == "/api/v3/episode":
		_ = enc.Encode(f.episodes)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeArr) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

type fixture struct {
	svc            *Service
	st             *store.Store
	radarr, sonarr *fakeArr
	admin, kid     store.User
	films, tv      store.Library
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f := &fixture{st: st, radarr: &fakeArr{}, sonarr: &fakeArr{}}
	for kind, fake := range map[string]*fakeArr{"radarr": f.radarr, "sonarr": f.sonarr} {
		srv := httptest.NewServer(fake)
		t.Cleanup(srv.Close)
		if _, err := st.CreateArrConnection(ctx, store.ArrConnection{Kind: kind, Name: kind, URL: srv.URL, APIKey: "k", CallbackURL: "http://c"}); err != nil {
			t.Fatal(err)
		}
	}
	if f.admin, err = st.CreateUser(ctx, "admin", "Admin", "h", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if f.kid, err = st.CreateUser(ctx, "kid", "Kid", "h", store.RoleUser); err != nil {
		t.Fatal(err)
	}
	if f.films, err = st.CreateLibrary(ctx, "Movies", store.MediaMovies, []string{"/m"}); err != nil {
		t.Fatal(err)
	}
	if f.tv, err = st.CreateLibrary(ctx, "TV", store.MediaTV, []string{"/tv"}); err != nil {
		t.Fatal(err)
	}
	f.svc = New(st, fakeTMDB{}, nil)
	return f
}

func (f *fixture) item(t *testing.T, it store.Item) store.Item {
	t.Helper()
	saved, err := f.st.UpsertItem(context.Background(), it)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

// holdSeries puts Breaking Bad on disk with the given episodes of season 1.
func (f *fixture) holdSeries(t *testing.T, episodes ...int) store.Item {
	series := f.item(t, store.Item{LibraryID: f.tv.ID, Kind: store.KindSeries, Title: "Breaking Bad", TmdbID: 1396, Path: "/tv/bb"})
	season := f.item(t, store.Item{LibraryID: f.tv.ID, Kind: store.KindSeason, ParentID: series.ID, SeasonNum: 1, Title: "Season 01", Path: "/tv/bb/s1"})
	for _, n := range episodes {
		f.item(t, store.Item{LibraryID: f.tv.ID, Kind: store.KindEpisode, ParentID: season.ID, SeasonNum: 1, EpisodeNum: n,
			Title: "Ep", Path: "/tv/bb/s1/" + strconv.Itoa(n) + ".mkv"})
	}
	return series
}

func (f *fixture) setLevel(t *testing.T, u *store.User, level string) {
	t.Helper()
	if err := f.st.SetRequestLevel(context.Background(), u.ID, level); err != nil {
		t.Fatal(err)
	}
	u.RequestLevel = level
}

func TestCreateByLevel(t *testing.T) {
	ctx := context.Background()
	f := setup(t)

	f.setLevel(t, &f.kid, store.RequestNone)
	if _, _, err := f.svc.Create(ctx, f.kid, NewRequest{MediaType: "movie", TmdbID: 603}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("level none: %v", err)
	}

	f.setLevel(t, &f.kid, store.RequestApproval)
	r, created, err := f.svc.Create(ctx, f.kid, NewRequest{MediaType: "movie", TmdbID: 603})
	if err != nil || !created {
		t.Fatalf("approval: %v, created %v", err, created)
	}
	if r.Status != store.RequestRequested || r.Title != "Film 603" || r.Poster == "" || f.radarr.called("POST /api/v3/movie") != 0 {
		t.Fatalf("waiting request = %+v; radarr calls %v", r, f.radarr.calls)
	}
	// Asking again, by anyone, finds the same request.
	again, created, err := f.svc.Create(ctx, f.admin, NewRequest{MediaType: "movie", TmdbID: 603})
	if err != nil || created || again.ID != r.ID {
		t.Fatalf("duplicate: %+v created %v err %v", again, created, err)
	}

	// Admins are auto whatever their row says.
	sent, created, err := f.svc.Create(ctx, f.admin, NewRequest{MediaType: "movie", TmdbID: 604})
	if err != nil || !created {
		t.Fatal(err)
	}
	if sent.Status != store.RequestApproved || sent.ArrID != 77 || sent.QualityProfileID != 4 || sent.RootFolder != "/media" ||
		sent.DecidedBy != f.admin.ID || f.radarr.called("POST /api/v3/movie") != 1 {
		t.Fatalf("auto request = %+v", sent)
	}

	// Something in the library cannot be requested.
	f.item(t, store.Item{LibraryID: f.films.ID, Kind: store.KindMovie, Title: "Heat", TmdbID: 949, Path: "/m/heat.mkv"})
	if _, _, err := f.svc.Create(ctx, f.admin, NewRequest{MediaType: "movie", TmdbID: 949}); !errors.Is(err, ErrInLibrary) {
		t.Fatalf("held film: %v", err)
	}

	// A series by seasons, sent with its TVDB id; a later request for a
	// season it already covers is the same request, one for another
	// season is a new one.
	f.setLevel(t, &f.kid, store.RequestAuto)
	show, _, err := f.svc.Create(ctx, f.kid, NewRequest{MediaType: "tv", TmdbID: 1396, Seasons: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	if show.Status != store.RequestApproved || show.TvdbID != 81189 || show.ArrID != 55 {
		t.Fatalf("series request = %+v", show)
	}
	if same, created, _ := f.svc.Create(ctx, f.kid, NewRequest{MediaType: "tv", TmdbID: 1396, Episodes: []store.EpisodeRef{{Season: 2, Episode: 3}}}); created || same.ID != show.ID {
		t.Fatal("an episode of a requested season made a new request")
	}
	if _, created, _ := f.svc.Create(ctx, f.kid, NewRequest{MediaType: "tv", TmdbID: 1396, Seasons: []int{3}}); !created {
		t.Fatal("another season was folded into the first request")
	}

	if _, _, err := f.svc.Create(ctx, f.kid, NewRequest{MediaType: "music", TmdbID: 1}); err == nil {
		t.Fatal("unknown media type accepted")
	}
	noTMDB := New(f.st, nil, nil)
	if _, _, err := noTMDB.Create(ctx, f.admin, NewRequest{MediaType: "movie", TmdbID: 1}); !errors.Is(err, ErrNoTMDB) {
		t.Fatalf("without TMDB: %v", err)
	}
}

func TestApproveDeclineWithdraw(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	r, _, err := f.svc.Create(ctx, f.kid, NewRequest{MediaType: "movie", TmdbID: 603})
	if err != nil {
		t.Fatal(err)
	}

	// The instance is down: the request stays waiting, with the reason.
	f.radarr.down = true
	failed, err := f.svc.Approve(ctx, f.admin, r.ID, Override{})
	if err == nil || failed.Status != store.RequestRequested || failed.Error == "" {
		t.Fatalf("approve while down = %+v, %v", failed, err)
	}
	f.radarr.down = false
	ok, err := f.svc.Approve(ctx, f.admin, r.ID, Override{QualityProfileID: 9, RootFolder: "/4k", Note: "Enjoy"})
	if err != nil {
		t.Fatal(err)
	}
	if ok.Status != store.RequestApproved || ok.Error != "" || ok.QualityProfileID != 9 || ok.RootFolder != "/4k" || ok.Note != "Enjoy" {
		t.Fatalf("approved = %+v", ok)
	}
	if _, err := f.svc.Approve(ctx, f.admin, r.ID, Override{}); !errors.Is(err, ErrState) {
		t.Fatalf("approving twice: %v", err)
	}
	if _, err := f.svc.Decline(ctx, f.admin, r.ID, ""); !errors.Is(err, ErrState) {
		t.Fatalf("declining a sent request: %v", err)
	}
	if _, err := f.svc.Withdraw(ctx, f.kid, r.ID); !errors.Is(err, ErrState) {
		t.Fatalf("withdrawing a sent request: %v", err)
	}

	other, _, err := f.svc.Create(ctx, f.kid, NewRequest{MediaType: "movie", TmdbID: 604})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Withdraw(ctx, f.admin, other.ID); err != nil {
		t.Fatalf("admin withdrawing: %v", err)
	}
	other, _, _ = f.svc.Create(ctx, f.kid, NewRequest{MediaType: "movie", TmdbID: 604})
	declined, err := f.svc.Decline(ctx, f.admin, other.ID, "Not this one")
	if err != nil || declined.Status != store.RequestDeclined || declined.Note != "Not this one" {
		t.Fatalf("declined = %+v, %v", declined, err)
	}
	stranger := store.User{ID: "someone", Role: store.RoleUser, RequestLevel: store.RequestApproval}
	if _, err := f.svc.Withdraw(ctx, stranger, other.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("someone else's request: %v", err)
	}

	mine, err := f.svc.List(ctx, f.kid, true, nil)
	if err != nil || len(mine) != 2 {
		t.Fatalf("a user asking for all sees %d, want their own 2 (%v)", len(mine), err)
	}
	all, err := f.svc.List(ctx, f.admin, true, []string{store.RequestDeclined})
	if err != nil || len(all) != 1 {
		t.Fatalf("admin's declined list = %d (%v)", len(all), err)
	}
}

func TestTracking(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	film, _, _ := f.svc.Create(ctx, f.admin, NewRequest{MediaType: "movie", TmdbID: 603})
	show, _, _ := f.svc.Create(ctx, f.admin, NewRequest{MediaType: "tv", TmdbID: 1396, Seasons: []int{1}})
	if film.Status != store.RequestApproved || show.Status != store.RequestApproved {
		t.Fatalf("not sent: %s %s", film.Status, show.Status)
	}
	conns, _ := f.st.ArrConnections(ctx)
	var sonarr store.ArrConnection
	for _, c := range conns {
		if c.Kind == "sonarr" {
			sonarr = c
		}
	}
	status := func(id string) string {
		r, err := f.st.Request(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return r.Status
	}

	// A grab for another series changes nothing; one for this does.
	if err := f.svc.Grabbed(ctx, sonarr, arr.Event{Type: "Grab", ArrID: 999, TvdbID: 1}); err != nil {
		t.Fatal(err)
	}
	if status(show.ID) != store.RequestApproved {
		t.Fatal("an unrelated grab moved the request")
	}
	if err := f.svc.Grabbed(ctx, sonarr, arr.Event{Type: "Grab", TvdbID: 81189}); err != nil {
		t.Fatal(err)
	}
	if status(show.ID) != store.RequestDownloading || status(film.ID) != store.RequestApproved {
		t.Fatalf("after grab: show %s, film %s", status(show.ID), status(film.ID))
	}

	// The film's grab is only seen in Radarr's queue.
	f.radarr.queue = []map[string]any{{"movieId": 77, "title": "Film"}}
	if err := f.svc.PollQueues(ctx); err != nil {
		t.Fatal(err)
	}
	if status(film.ID) != store.RequestDownloading {
		t.Fatalf("film after poll = %s", status(film.ID))
	}

	// A scan brings them in. A season with no episodes does not count.
	f.item(t, store.Item{LibraryID: f.films.ID, Kind: store.KindMovie, Title: "Film", TmdbID: 603, Path: "/m/f.mkv"})
	f.holdSeries(t)
	if err := f.svc.CheckAvailable(ctx); err != nil {
		t.Fatal(err)
	}
	if status(film.ID) != store.RequestAvailable || status(show.ID) != store.RequestDownloading {
		t.Fatalf("after scan: film %s, show %s", status(film.ID), status(show.ID))
	}
	s1, _ := f.st.ItemsByTmdbIDs(ctx, store.KindSeries, []int{1396})
	seasons, _ := f.st.ItemsByParent(ctx, s1[0].ID)
	f.item(t, store.Item{LibraryID: f.tv.ID, Kind: store.KindEpisode, ParentID: seasons[0].ID, SeasonNum: 1, EpisodeNum: 1, Title: "Pilot", Path: "/tv/bb/s1/1.mkv"})
	if err := f.svc.CheckAvailable(ctx); err != nil {
		t.Fatal(err)
	}
	if status(show.ID) != store.RequestAvailable {
		t.Fatalf("show after its season arrived = %s", status(show.ID))
	}
}

func TestSeriesSatisfied(t *testing.T) {
	disk := map[int]map[int]bool{1: {1: true, 2: true}, 2: {}}
	for _, tc := range []struct {
		name string
		r    store.MediaRequest
		want bool
	}{
		{"whole series", store.MediaRequest{MediaType: "tv"}, true},
		{"held season", store.MediaRequest{MediaType: "tv", Seasons: []int{1}}, true},
		{"empty season", store.MediaRequest{MediaType: "tv", Seasons: []int{2}}, false},
		{"held episode", store.MediaRequest{MediaType: "tv", Episodes: []store.EpisodeRef{{Season: 1, Episode: 2}}}, true},
		{"missing episode", store.MediaRequest{MediaType: "tv", Episodes: []store.EpisodeRef{{Season: 1, Episode: 3}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := seriesSatisfied(tc.r, disk); got != tc.want {
				t.Fatalf("satisfied = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSearchAndTitle(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.item(t, store.Item{LibraryID: f.films.ID, Kind: store.KindMovie, Title: "The Matrix", TmdbID: 603, Path: "/m/matrix.mkv"})
	f.holdSeries(t, 1, 2)
	if _, _, err := f.svc.Create(ctx, f.kid, NewRequest{MediaType: "movie", TmdbID: 604}); err != nil {
		t.Fatal(err)
	}
	all := func(string) bool { return true }
	none := func(string) bool { return false }
	st, err := f.svc.StatusFor(ctx, f.kid)
	if err != nil || !st.Enabled || !st.Movies || !st.TV || st.Level != store.RequestApproval {
		t.Fatalf("status = %+v, %v", st, err)
	}

	got, err := f.svc.Search(ctx, st, "matrix", all)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TmdbID != 604 || got[0].Request != store.RequestRequested {
		t.Fatalf("search for someone who sees everything = %+v", got)
	}
	// Held only in libraries this viewer cannot see: still requestable.
	// Artwork first, then popularity.
	if got, _ := f.svc.Search(ctx, st, "matrix", none); len(got) != 4 || got[0].TmdbID != 604 || got[1].TmdbID != 603 || got[3].TmdbID != 1 {
		t.Fatalf("search for someone who sees nothing = %+v", got)
	}
	tvOnly := st
	tvOnly.Movies = false
	if got, _ := f.svc.Search(ctx, tvOnly, "matrix", none); len(got) != 1 || got[0].MediaType != "tv" {
		t.Fatalf("with only Sonarr connected = %+v", got)
	}
	off := st
	off.Enabled = false
	if got, _ := f.svc.Search(ctx, off, "matrix", all); len(got) != 0 {
		t.Fatal("search outside the library while requests are off")
	}

	film, err := f.svc.TitleFor(ctx, st, "movie", 604, "GB", all)
	if err != nil {
		t.Fatal(err)
	}
	if film.Title != "Film 604" || film.Certification != "15" || len(film.Cast) != 1 || len(film.Requests) != 1 || !film.CanRequest || film.ItemID != "" {
		t.Fatalf("film page = %+v", film)
	}
	held, err := f.svc.TitleFor(ctx, st, "movie", 603, "GB", all)
	if err != nil || held.ItemID == "" || held.CanRequest {
		t.Fatalf("held film page = %+v, %v", held, err)
	}
	show, err := f.svc.TitleFor(ctx, st, "tv", 1396, "GB", all)
	if err != nil {
		t.Fatal(err)
	}
	if len(show.Seasons) != 3 || show.Seasons[1].Held != 2 || show.Seasons[2].Held != 0 || !show.CanRequest || show.ItemID == "" {
		t.Fatalf("series page = %+v", show)
	}
}

func TestMissing(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.svc.Now = func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }
	series := f.holdSeries(t, 1, 2)

	// Not in Sonarr: nothing to show.
	got, err := f.svc.MissingFor(ctx, series)
	if err != nil || got.Managed || len(got.Seasons) != 0 {
		t.Fatalf("unmanaged = %+v, %v", got, err)
	}

	f.svc.missing = map[int]missingEntry{} // forget the answer above
	f.sonarr.series = []map[string]any{{"id": 5, "tvdbId": 81189, "title": "Breaking Bad", "seasons": []any{}}}
	ep := func(id, season, n int, air string) map[string]any {
		return map[string]any{"id": id, "seriesId": 5, "seasonNumber": season, "episodeNumber": n, "title": "E" + strconv.Itoa(n),
			"airDate": strings.SplitN(air, "T", 2)[0], "airDateUtc": air, "monitored": true}
	}
	f.sonarr.episodes = []map[string]any{
		ep(1, 0, 1, "2009-01-01T00:00:00Z"), // a special: left out
		ep(2, 1, 1, "2008-01-20T02:00:00Z"),
		ep(3, 1, 2, "2008-01-27T02:00:00Z"),
		ep(4, 1, 3, "2008-02-10T02:00:00Z"), // aired, not held
		ep(5, 2, 1, "2026-09-01T02:00:00Z"), // not aired yet
		{"id": 6, "seriesId": 5, "seasonNumber": 2, "episodeNumber": 2, "title": "TBA"},
	}
	got, err = f.svc.MissingFor(ctx, series)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Managed || len(got.Seasons) != 2 {
		t.Fatalf("managed = %+v", got)
	}
	s1, s2 := got.Seasons[0], got.Seasons[1]
	if s1.Held != 2 || s1.Total != 3 || len(s1.Episodes) != 1 || s1.Episodes[0].Episode != 3 || s1.Episodes[0].Status != "missing" {
		t.Fatalf("season 1 = %+v", s1)
	}
	if s2.Held != 0 || len(s2.Episodes) != 1 || s2.Episodes[0].Status != "upcoming" || s2.Episodes[0].AirDate != "2026-09-01" {
		t.Fatalf("season 2 = %+v", s2)
	}
	// Cached: a second look does not ask Sonarr again.
	before := f.sonarr.called("GET /api/v3/episode")
	if _, err := f.svc.MissingFor(ctx, series); err != nil {
		t.Fatal(err)
	}
	if f.sonarr.called("GET /api/v3/episode") != before {
		t.Fatal("asked Sonarr again within the cache window")
	}

	if err := f.svc.SearchMissing(ctx, f.kid, series, []int{1}, nil); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("a user needing approval searched: %v", err)
	}
	if err := f.svc.SearchMissing(ctx, f.admin, series, nil, []store.EpisodeRef{{Season: 1, Episode: 3}}); err != nil {
		t.Fatal(err)
	}
	if f.sonarr.called("POST /api/v3/command") != 1 || f.sonarr.called("PUT /api/v3/episode/monitor") != 1 {
		t.Fatalf("sonarr calls = %v", f.sonarr.calls)
	}
	if err := f.svc.SearchMissing(ctx, f.admin, series, nil, nil); err == nil {
		t.Fatal("a search for nothing succeeded")
	}
}
