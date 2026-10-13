package store

import (
	"context"
	"testing"
	"time"
)

// Recently added TV is a list of series ordered by their newest episode, and
// recently watched series are ordered by their latest play.
func TestHomeQueries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	save := func(it Item) Item {
		t.Helper()
		it.LibraryID = lib.ID
		saved, err := s.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	setAdded := func(id, at string) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, `UPDATE items SET added_at = ? WHERE id = ?`, at, id); err != nil {
			t.Fatal(err)
		}
	}
	// Old Show is older, but gained an episode most recently.
	old := save(Item{Kind: KindSeries, Title: "Old Show", Path: "/tv/old"})
	oldS1 := save(Item{Kind: KindSeason, ParentID: old.ID, Path: "/tv/old/s1"})
	oldE1 := save(Item{Kind: KindEpisode, ParentID: oldS1.ID, Path: "/tv/old/s1/e1.mkv", RuntimeSecs: 1800})
	oldE9 := save(Item{Kind: KindEpisode, ParentID: oldS1.ID, Path: "/tv/old/s1/e9.mkv", RuntimeSecs: 1800})
	newer := save(Item{Kind: KindSeries, Title: "New Show", Path: "/tv/new"})
	newS1 := save(Item{Kind: KindSeason, ParentID: newer.ID, Path: "/tv/new/s1"})
	newE1 := save(Item{Kind: KindEpisode, ParentID: newS1.ID, Path: "/tv/new/s1/e1.mkv", RuntimeSecs: 1800})
	setAdded(oldE1.ID, "2020-01-01 00:00:00")
	setAdded(newE1.ID, "2025-01-01 00:00:00")
	setAdded(oldE9.ID, "2026-01-01 00:00:00")

	recent, err := s.RecentlyAdded(ctx, lib, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].ID != old.ID || recent[1].ID != newer.ID {
		t.Fatalf("recently added = %v, want [Old Show, New Show] — series, newest episode first", titles(recent))
	}

	if _, err := s.SetProgress(ctx, LocalUser, newE1.ID, 1800, 1800); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE play_state SET updated_at = '2026-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProgress(ctx, LocalUser, oldE1.ID, 600, 1800); err != nil {
		t.Fatal(err)
	}
	series, err := s.RecentSeries(ctx, LocalUser, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 2 || series[0].ID != old.ID {
		t.Fatalf("recent series = %v, want Old Show (played last) first", titles(series))
	}
}

func TestAddedAt(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		mtime int64
		want  string
	}{
		{"no file", 0, "2026-10-13 12:00:00"},
		{"file arrived earlier", time.Date(2026, 10, 8, 21, 30, 0, 0, time.UTC).Unix(), "2026-10-08 21:30:00"},
		{"clock in the future", now.Add(time.Hour).Unix(), "2026-10-13 12:00:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := addedAt(tc.mtime, now); got != tc.want {
				t.Fatalf("addedAt = %s, want %s", got, tc.want)
			}
		})
	}
}

// A back catalogue scanned in one pass ranks by when each file arrived, not
// by the order the scanner walked it; episodes that arrived together rank
// by which aired last.
func TestRecentlyAddedByArrival(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	day := func(d int) int64 { return time.Date(2026, 10, d, 20, 0, 0, 0, time.UTC).Unix() }
	// Walked A to Z, as a scanner would.
	for _, sh := range []struct {
		title, aired string
		mtime        int64
	}{
		{"Alpha", "2026-10-01", day(2)},
		{"Bravo", "2026-10-08", day(9)},
		{"Charlie", "2026-10-07", day(9)},
		{"Zulu", "2020-05-01", day(1)},
	} {
		series, err := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindSeries, Title: sh.title, Path: "/tv/" + sh.title})
		if err != nil {
			t.Fatal(err)
		}
		season, err := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindSeason, ParentID: series.ID, Path: "/tv/" + sh.title + "/s1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindEpisode, ParentID: season.ID,
			Path: "/tv/" + sh.title + "/s1/e1.mkv", AirDate: sh.aired, FileMtime: sh.mtime}); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := s.RecentlyAdded(ctx, lib, 10)
	if err != nil {
		t.Fatal(err)
	}
	got, want := titles(recent), []string{"Bravo", "Charlie", "Alpha", "Zulu"}
	if len(got) != len(want) {
		t.Fatalf("recently added = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recently added = %v, want %v", got, want)
		}
	}
}

func titles(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}
