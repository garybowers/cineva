package library

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// fakeVideo is a provider whose answers a test can change between scans,
// counting what it was asked. Safe to ask from several goroutines, as the
// enrichment job's workers do.
type fakeVideo struct {
	movie   func(title string, year int) (metadata.VideoResult, error)
	tv      func(title string, year int) (metadata.VideoResult, error)
	season  func(tvID, season int) (metadata.SeasonResult, error)
	mu      sync.Mutex
	asked   map[string]int // "movie", "tv", "season" -> calls
	seasons []int          // season numbers fetched, in order
}

func (f *fakeVideo) count(what string, season ...int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked[what]++
	f.seasons = append(f.seasons, season...)
}

// calls is how many times the provider was asked about what.
func (f *fakeVideo) calls(what string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked[what]
}

func newFakeVideo() *fakeVideo {
	notFound := func(string, int) (metadata.VideoResult, error) { return metadata.VideoResult{}, metadata.ErrNotFound }
	return &fakeVideo{
		movie: notFound, tv: notFound,
		season: func(int, int) (metadata.SeasonResult, error) { return metadata.SeasonResult{}, metadata.ErrNotFound },
		asked:  map[string]int{},
	}
}

func (f *fakeVideo) SearchMovie(_ context.Context, title string, year int) (metadata.VideoResult, error) {
	f.count("movie")
	return f.movie(title, year)
}
func (f *fakeVideo) SearchTV(_ context.Context, title string, year int) (metadata.VideoResult, error) {
	f.count("tv")
	return f.tv(title, year)
}
func (f *fakeVideo) TVSeason(_ context.Context, tvID, season int) (metadata.SeasonResult, error) {
	f.count("season", season)
	return f.season(tvID, season)
}
func (f *fakeVideo) SearchMovies(context.Context, string, int) ([]metadata.VideoResult, error) {
	return nil, metadata.ErrNotFound
}
func (f *fakeVideo) MovieByID(context.Context, int) (metadata.VideoResult, error) {
	return metadata.VideoResult{}, metadata.ErrNotFound
}

func TestRetryWait(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{0, day}, {1, day}, {2, 2 * day}, {3, 4 * day}, {4, 8 * day},
		{5, 16 * day}, {6, 32 * day}, {7, 32 * day}, {50, 32 * day},
	}
	for _, tc := range tests {
		if got := retryWait(tc.attempts); got != tc.want {
			t.Errorf("retryWait(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}

func TestRetryDue(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		attempts int
		tried    time.Time
		now      time.Time
		want     bool
	}{
		{"never tried", 0, time.Time{}, t0, true},
		{"failed an hour ago", 1, t0, t0.Add(time.Hour), false},
		{"failed a day ago", 1, t0, t0.Add(24 * time.Hour), true},
		{"third failure, three days on", 3, t0, t0.Add(72 * time.Hour), false},
		{"third failure, four days on", 3, t0, t0.Add(96 * time.Hour), true},
		{"many failures, a month on", 20, t0, t0.Add(31 * 24 * time.Hour), false},
		{"many failures, 32 days on", 20, t0, t0.Add(32 * 24 * time.Hour), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			it := store.UnmatchedItem{Attempts: tc.attempts, TriedAt: tc.tried}
			if got := retryDue(it, tc.now); got != tc.want {
				t.Errorf("retryDue = %v, want %v", got, tc.want)
			}
		})
	}
}

// An unmatched film is looked up again by later incremental scans — not
// every scan, but once a day at most, backing off after repeated failures —
// and matched once the provider knows it.
func TestRetryUnmatchedFilm(t *testing.T) {
	root := t.TempDir()
	writeAt(t, filepath.Join(root, "Obscure Film (1987)", "Obscure Film (1987).mkv"), time.Now().Add(-time.Hour))
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	video := newFakeVideo()
	m := NewManager(st, video, "")
	m.retryPace = 0
	m.Probe = func(string) (media.Info, error) { return media.Info{RuntimeSecs: 5400}, nil }
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }

	scanAt := func(at time.Time) int {
		t.Helper()
		now = at
		video.asked = map[string]int{}
		scanOnce(t, m, lib, false)
		return video.asked["movie"]
	}
	day := 24 * time.Hour
	t0 := now

	if n := scanAt(t0); n != 1 {
		t.Fatalf("first scan: %d lookups, want 1", n)
	}
	if n := scanAt(t0.Add(time.Hour)); n != 0 {
		t.Errorf("an hour later: %d lookups, want 0", n)
	}
	if n := scanAt(t0.Add(day)); n != 1 {
		t.Errorf("a day later: %d lookups, want 1", n)
	}
	// Two failures now: the next try waits two days, not one.
	if n := scanAt(t0.Add(2*day + time.Hour)); n != 0 {
		t.Errorf("a day after the second failure: %d lookups, want 0", n)
	}

	video.movie = func(title string, year int) (metadata.VideoResult, error) {
		if title != "Obscure Film" || year != 1987 {
			t.Errorf("looked up %q (%d), want the scanned title and year", title, year)
		}
		return metadata.VideoResult{TmdbID: 4242, Title: "Obscure Film", Overview: "Found at last.", Genres: []string{"Drama"}}, nil
	}
	if n := scanAt(t0.Add(3 * day)); n != 1 {
		t.Errorf("two days after the second failure: %d lookups, want 1", n)
	}
	items, err := st.ItemsByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].TmdbID != 4242 || items[0].Overview != "Found at last." {
		t.Fatalf("after the retry: %+v, want the film matched", items)
	}
	if genres, err := st.GenresForItem(ctx, items[0].ID); err != nil || len(genres) != 1 {
		t.Errorf("genres after the retry: %v %v, want [Drama]", genres, err)
	}
	// Matched now, so never retried again.
	if n := scanAt(t0.Add(60 * day)); n != 0 {
		t.Errorf("after matching: %d lookups, want 0", n)
	}
}

// A provider that cannot be reached says nothing about the title: no
// failure is recorded, so the retry comes at the next scan, not a day on.
func TestRetryUnmatchedProviderDown(t *testing.T) {
	root := t.TempDir()
	writeAt(t, filepath.Join(root, "Film", "Film.mkv"), time.Now().Add(-time.Hour))
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	video := newFakeVideo()
	video.movie = func(string, int) (metadata.VideoResult, error) {
		return metadata.VideoResult{}, errors.New("status 503")
	}
	m := NewManager(st, video, "")
	m.retryPace = 0
	m.Probe = func(string) (media.Info, error) { return media.Info{RuntimeSecs: 5400}, nil }

	scanOnce(t, m, lib, false) // scan's own lookup fails: 1
	scanOnce(t, m, lib, false) // retried at once: 2
	if video.asked["movie"] != 2 {
		t.Errorf("%d lookups, want 2", video.asked["movie"])
	}
	un, err := st.UnmatchedItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(un) != 1 || un[0].Attempts != 0 {
		t.Errorf("unmatched %+v, want one with no failures recorded", un)
	}
}

// A series matched by a retry gets what a scan of a matched series would
// have given its seasons and episodes, from the rows already stored. And an
// unmatched series is no longer looked up on every incremental scan, as an
// on-air one is.
func TestRetryUnmatchedSeries(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Quiet Show", "Season 1")
	writeAt(t, filepath.Join(dir, "Quiet.Show.S01E01.mkv"), time.Now().Add(-time.Hour))
	writeAt(t, filepath.Join(dir, "Quiet.Show.S01E02.Named.mkv"), time.Now().Add(-time.Hour))
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	video := newFakeVideo()
	m := NewManager(st, video, "")
	m.retryPace = 0
	m.Probe = func(string) (media.Info, error) { return media.Info{RuntimeSecs: 1800}, nil }
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }

	scanOnce(t, m, lib, false)
	if video.asked["tv"] != 1 {
		t.Fatalf("first scan: %d series lookups, want 1", video.asked["tv"])
	}
	now = now.Add(time.Hour)
	scanOnce(t, m, lib, false)
	if video.asked["tv"] != 1 {
		t.Errorf("an hour later: %d series lookups in all, want still 1", video.asked["tv"])
	}

	video.tv = func(string, int) (metadata.VideoResult, error) {
		return metadata.VideoResult{TmdbID: 77, Title: "Quiet Show", Status: "Ended"}, nil
	}
	video.season = func(tvID, season int) (metadata.SeasonResult, error) {
		if tvID != 77 || season != 1 {
			return metadata.SeasonResult{}, metadata.ErrNotFound
		}
		return metadata.SeasonResult{TmdbID: 700, Overview: "The first season.", Episodes: []metadata.EpisodeResult{
			{TmdbID: 701, Num: 1, Title: "Pilot", AirDate: "2020-05-01"},
			{TmdbID: 702, Num: 2, Title: "Provider Title", AirDate: "2020-05-08"},
		}}, nil
	}
	now = now.Add(25 * time.Hour)
	scanOnce(t, m, lib, false)

	items, err := st.ItemsByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[store.ItemKind][]store.Item{}
	for _, it := range items {
		byKind[it.Kind] = append(byKind[it.Kind], it)
	}
	if s := byKind[store.KindSeries]; len(s) != 1 || s[0].TmdbID != 77 {
		t.Fatalf("series %+v, want matched to 77", s)
	}
	if s := byKind[store.KindSeason]; len(s) != 1 || s[0].TmdbID != 700 || s[0].Overview != "The first season." {
		t.Errorf("season %+v, want filled from the provider", s)
	}
	eps := byKind[store.KindEpisode]
	if len(eps) != 2 {
		t.Fatalf("%d episodes, want 2", len(eps))
	}
	for _, ep := range eps {
		want := map[int]string{1: "Pilot", 2: "Named"}[ep.EpisodeNum]
		if ep.Title != want || ep.TmdbID == 0 || ep.AirDate == "" {
			t.Errorf("episode %d: title %q tmdb %d aired %q; want %q, an id and a date (a filename title is kept)",
				ep.EpisodeNum, ep.Title, ep.TmdbID, ep.AirDate, want)
		}
	}
}
