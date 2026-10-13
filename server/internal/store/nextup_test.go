package store

import (
	"context"
	"testing"
)

// buildSeries makes a series with two seasons of two episodes.
func buildSeries(t *testing.T, st *Store, ctx context.Context) (seriesID string, eps []Item) {
	t.Helper()
	lib, err := st.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatalf("library: %v", err)
	}
	series, err := st.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindSeries, Title: "Show", Path: "/tv/Show"})
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	for _, sn := range []int{1, 2} {
		season, err := st.UpsertItem(ctx, Item{
			LibraryID: lib.ID, Kind: KindSeason, ParentID: series.ID,
			Title: "Season", SeasonNum: sn, Path: "/tv/Show/S" + string(rune('0'+sn)),
		})
		if err != nil {
			t.Fatalf("season: %v", err)
		}
		for _, en := range []int{1, 2} {
			ep, err := st.UpsertItem(ctx, Item{
				LibraryID: lib.ID, Kind: KindEpisode, ParentID: season.ID,
				Title: "Ep", SeasonNum: sn, EpisodeNum: en, RuntimeSecs: 1000,
				Path: "/tv/Show/S" + string(rune('0'+sn)) + "/e" + string(rune('0'+en)) + ".mkv",
			})
			if err != nil {
				t.Fatalf("episode: %v", err)
			}
			eps = append(eps, ep)
		}
	}
	return series.ID, eps
}

func TestNextUp(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seriesID, eps := buildSeries(t, st, ctx)
	const user = LocalUser

	// Nothing watched: the very first episode.
	got, ok, err := st.NextUp(ctx, user, seriesID)
	if err != nil || !ok {
		t.Fatalf("NextUp: %v ok=%v", err, ok)
	}
	if got.Item.ID != eps[0].ID || got.Reason != "next" {
		t.Errorf("fresh series -> %s/%s, want S01E01/next", got.Item.ID, got.Reason)
	}

	// Part-way through S01E02: resume it, with the position.
	if _, err := st.SetProgress(ctx, user, eps[1].ID, 300, 1000); err != nil {
		t.Fatal(err)
	}
	// S01E01 finished.
	if _, err := st.SetWatched(ctx, user, eps[0].ID, true); err != nil {
		t.Fatal(err)
	}
	got, _, _ = st.NextUp(ctx, user, seriesID)
	if got.Item.ID != eps[1].ID || got.Reason != "resume" || got.PositionSecs != 300 {
		t.Errorf("part-watched -> %s/%s@%d, want S01E02/resume@300", got.Item.ID, got.Reason, got.PositionSecs)
	}

	// S01 done: the pointer must cross into season 2 on its own.
	if _, err := st.SetWatched(ctx, user, eps[1].ID, true); err != nil {
		t.Fatal(err)
	}
	got, _, _ = st.NextUp(ctx, user, seriesID)
	if got.Item.ID != eps[2].ID || got.Reason != "next" {
		t.Errorf("end of season -> %s/%s, want S02E01/next", got.Item.ID, got.Reason)
	}

	// Everything watched: offer the first episode again, labelled honestly.
	for _, e := range eps[2:] {
		if _, err := st.SetWatched(ctx, user, e.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, _ = st.NextUp(ctx, user, seriesID)
	if !ok || got.Item.ID != eps[0].ID || got.Reason != "restart" {
		t.Errorf("fully watched -> %s/%s, want S01E01/restart", got.Item.ID, got.Reason)
	}
}

// A season scopes the answer to its own episodes.
func TestNextUpForSeason(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, eps := buildSeries(t, st, ctx)
	season2 := eps[2].ParentID

	got, ok, err := st.NextUp(ctx, LocalUser, season2)
	if err != nil || !ok {
		t.Fatalf("NextUp(season): %v ok=%v", err, ok)
	}
	if got.Item.ID != eps[2].ID {
		t.Errorf("season 2 -> %s, want its own first episode", got.Item.ID)
	}
}

// A barely-started episode is not a resume point: a few seconds is a
// mis-click, and resuming from it would be indistinguishable from starting.
func TestNextUpIgnoresTrivialPosition(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seriesID, eps := buildSeries(t, st, ctx)
	if _, err := st.SetProgress(ctx, LocalUser, eps[0].ID, 5, 1000); err != nil {
		t.Fatal(err)
	}
	got, _, _ := st.NextUp(ctx, LocalUser, seriesID)
	if got.Reason != "next" {
		t.Errorf("5 seconds in -> %s, want next (not a resume)", got.Reason)
	}
}

// Asking a movie for its next-up is a no, not an error.
func TestNextUpOnNonSeries(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, _ := st.CreateLibrary(ctx, "M", MediaMovies, []string{"/m"})
	film, _ := st.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindMovie, Title: "Film", Path: "/m/f.mkv"})
	if _, ok, err := st.NextUp(ctx, LocalUser, film.ID); ok || err != nil {
		t.Errorf("movie -> ok=%v err=%v, want false/nil", ok, err)
	}
}
