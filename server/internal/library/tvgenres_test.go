package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// stubVideo stands in for TMDB.
type stubVideo struct {
	tv     metadata.VideoResult
	season metadata.SeasonResult
}

func (s stubVideo) SearchTV(context.Context, string, int) (metadata.VideoResult, error) {
	return s.tv, nil
}
func (s stubVideo) SearchMovie(context.Context, string, int) (metadata.VideoResult, error) {
	return metadata.VideoResult{}, metadata.ErrNotFound
}
func (s stubVideo) SearchMovies(context.Context, string, int) ([]metadata.VideoResult, error) {
	return nil, metadata.ErrNotFound
}
func (s stubVideo) MovieByID(context.Context, int) (metadata.VideoResult, error) {
	return metadata.VideoResult{}, metadata.ErrNotFound
}
func (s stubVideo) TVSeason(context.Context, int, int) (metadata.SeasonResult, error) {
	return s.season, nil
}

// Genres and credits live in their own tables and need saving explicitly
// once the row has an id. The film scanner always did; the series scanner
// never did, so every series had an empty genre list and no cast while every
// film had both.
func TestScanTVSavesSeriesGenresAndCredits(t *testing.T) {
	root := t.TempDir()
	epDir := filepath.Join(root, "Breaking Bad", "Season 1")
	if err := os.MkdirAll(epDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(epDir, "Breaking.Bad.S01E01.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{root})
	if err != nil {
		t.Fatal(err)
	}

	m := &Manager{
		Store: st,
		Probe: func(string) (media.Info, error) {
			return media.Info{RuntimeSecs: 2700, Container: "mkv", VideoCodec: "h264"}, nil
		},
		Video: stubVideo{tv: metadata.VideoResult{
			TmdbID: 1396, Title: "Breaking Bad", Year: 2008,
			Genres: []string{"Drama", "Crime"},
			Cast:   []metadata.Person{{TmdbID: 17419, Name: "Bryan Cranston", Character: "Walter White"}},
		}},
	}
	if _, _, err := m.scanTV(ctx, lib, fullPlan(t, m, lib), func(int, int, int) {}); err != nil {
		t.Fatalf("scanTV: %v", err)
	}

	items, err := st.ItemsByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	var seriesID string
	for _, it := range items {
		if it.Kind == store.KindSeries {
			seriesID = it.ID
		}
	}
	if seriesID == "" {
		t.Fatal("no series created")
	}

	genres, err := st.GenresForItem(ctx, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	if len(genres) != 2 {
		t.Errorf("series genres = %v, want two — they are being fetched and then dropped", genres)
	}

	cast, _, err := st.CreditsForItem(ctx, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cast) != 1 || cast[0].Name != "Bryan Cranston" {
		t.Errorf("series cast = %+v, want Bryan Cranston", cast)
	}
}

// fullPlan is a scan plan that rescans everything, for tests that drive a
// scanner directly.
func fullPlan(t *testing.T, m *Manager, lib store.Library) *scanPlan {
	t.Helper()
	p, err := m.newScanPlan(context.Background(), lib, true)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newLibTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// Everything TMDB returns for television that used to be dropped: the
// series' status, broadcaster and declared size; the season's own id and air
// date; and each episode's id, air date and rating. Episodes in particular
// had no date at all for two thirds of the library, because the broadcast
// date is the only date an episode has.
func TestScanTVStoresTelevisionDetail(t *testing.T) {
	root := t.TempDir()
	epDir := filepath.Join(root, "Breaking Bad", "Season 1")
	if err := os.MkdirAll(epDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(epDir, "Breaking.Bad.S01E01.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{
		Store: st,
		Probe: func(string) (media.Info, error) { return media.Info{RuntimeSecs: 2700, Container: "mkv"}, nil },
		Video: stubVideo{
			tv: metadata.VideoResult{
				TmdbID: 1396, Title: "Breaking Bad", Year: 2008,
				Status: "Ended", Network: "AMC",
				SeasonCount: 5, EpisodeCount: 62, FirstAired: "2008-01-20",
				LastAired: "2013-09-29", NextAired: "2026-06-01",
				OriginalTitle: "Breaking Bad (de)", Country: "US",
			},
			season: metadata.SeasonResult{
				TmdbID: 3572, AirDate: "2008-01-20",
				Episodes: []metadata.EpisodeResult{{
					TmdbID: 62085, Num: 1, Title: "Pilot",
					AirDate: "2008-01-20", Rating: 8.2, VoteCount: 1200,
					GuestStars: []metadata.Person{
						{TmdbID: 1, Name: "Carmen Serano", Character: "Carmen Molina"},
					},
				}},
			},
		},
	}
	if _, _, err := m.scanTV(ctx, lib, fullPlan(t, m, lib), func(int, int, int) {}); err != nil {
		t.Fatalf("scanTV: %v", err)
	}

	items, err := st.ItemsByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	var series, season, episode store.Item
	for _, it := range items {
		switch it.Kind {
		case store.KindSeries:
			series = it
		case store.KindSeason:
			season = it
		case store.KindEpisode:
			episode = it
		}
	}

	for _, c := range []struct {
		what string
		got  any
		want any
	}{
		{"series status", series.Status, "Ended"},
		{"series network", series.Network, "AMC"},
		{"series season count", series.SeasonCount, 5},
		{"series episode count", series.EpisodeCount, 62},
		{"season tmdb id", season.TmdbID, 3572},
		{"season air date", season.AirDate, "2008-01-20"},
		{"season year", season.Year, 2008},
		{"episode tmdb id", episode.TmdbID, 62085},
		{"episode air date", episode.AirDate, "2008-01-20"},
		{"episode year from air date", episode.Year, 2008},
		{"episode rating", episode.Rating, 8.2},
		{"episode vote count", episode.VoteCount, 1200},
		{"series last aired", series.LastAirDate, "2013-09-29"},
		{"series next aired", series.NextAirDate, "2026-06-01"},
		{"series original title", series.OriginalTitle, "Breaking Bad (de)"},
		{"series country", series.Country, "US"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.what, c.got, c.want)
		}
	}

	// Guest stars belong to the episode, not the series: they are the
	// people in this one and no other, which is the only thing an
	// episode-level cast is for.
	guests, _, err := st.CreditsForItem(ctx, episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(guests) != 1 || guests[0].Name != "Carmen Serano" {
		t.Errorf("episode guest stars = %+v, want Carmen Serano", guests)
	}
	if guests[0].Role != "Carmen Molina" {
		t.Errorf("guest role = %q, want the character name", guests[0].Role)
	}
	// ...and they must not be mistaken for series cast.
	seriesCast, _, err := st.CreditsForItem(ctx, series.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range seriesCast {
		if p.Name == "Carmen Serano" {
			t.Error("a guest star was credited on the series")
		}
	}
}
