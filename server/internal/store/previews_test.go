package store

import (
	"context"
	"testing"
)

// Preview candidates are a library's films and episodes with a running
// time, newest additions first, episodes labelled with their series.
func TestPreviewItems(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tv, err := s.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	films, err := s.CreateLibrary(ctx, "Films", MediaMovies, []string{"/films"})
	if err != nil {
		t.Fatal(err)
	}
	upsert := func(lib string, it Item) Item {
		t.Helper()
		it.LibraryID = lib
		saved, err := s.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	series := upsert(tv.ID, Item{Kind: KindSeries, Title: "Show", Path: "/tv/Show", RuntimeSecs: 2400})
	season := upsert(tv.ID, Item{Kind: KindSeason, ParentID: series.ID, SeasonNum: 2, Path: "/tv/Show/S2"})
	// added_at follows the file's mtime, so these were added oldest first.
	older := upsert(tv.ID, Item{Kind: KindEpisode, ParentID: season.ID, Title: "One", SeasonNum: 2, EpisodeNum: 1,
		Path: "/tv/Show/S2/e1.mkv", RuntimeSecs: 2400, FileMtime: 1_600_000_000})
	newer := upsert(tv.ID, Item{Kind: KindEpisode, ParentID: season.ID, Title: "Two", SeasonNum: 2, EpisodeNum: 2,
		Path: "/tv/Show/S2/e2.mkv", RuntimeSecs: 2400, FileMtime: 1_700_000_000})
	// No running time: nothing to place thumbnails along.
	upsert(tv.ID, Item{Kind: KindEpisode, ParentID: season.ID, Title: "Three", EpisodeNum: 3,
		Path: "/tv/Show/S2/e3.mkv", FileMtime: 1_650_000_000})
	film := upsert(films.ID, Item{Kind: KindMovie, Title: "Film", Path: "/films/f.mkv", RuntimeSecs: 6000, FileMtime: 1_650_000_000})

	got, err := s.PreviewItems(ctx, tv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != newer.ID || got[1].ID != older.ID {
		t.Fatalf("tv candidates = %+v, want the two timed episodes, newest first", got)
	}
	if p := got[0]; p.Series != "Show" || p.SeasonNum != 2 || p.EpisodeNum != 2 || p.Path != newer.Path ||
		p.RuntimeSecs != 2400 || p.FileMtime != 1_700_000_000 || p.LibraryID != tv.ID {
		t.Errorf("episode = %+v, want it labelled with its series and carrying its file", p)
	}

	one, err := s.PreviewItem(ctx, film.ID)
	if err != nil {
		t.Fatal(err)
	}
	if one.Title != "Film" || one.Series != "" || one.LibraryID != films.ID || one.RuntimeSecs != 6000 {
		t.Errorf("film = %+v", one)
	}
}
