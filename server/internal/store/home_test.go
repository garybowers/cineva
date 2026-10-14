package store

import (
	"context"
	"fmt"
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

// New episodes are those that aired within the window, newest broadcast
// first and, of a day's broadcasts, the latest to arrive; a back catalogue
// that arrived today is not new. Each names its series and carries the
// viewer's own watch state, and only libraries in scope are listed.
func TestNewEpisodes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	at := func(month time.Month, d, h int) int64 { return time.Date(2026, month, d, h, 0, 0, 0, time.UTC).Unix() }
	tv, err := s.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	kids, err := s.CreateLibrary(ctx, "Kids TV", MediaTV, []string{"/kids"})
	if err != nil {
		t.Fatal(err)
	}
	seasons := map[string]Item{}
	season := func(lib Library, show string, n int) Item {
		t.Helper()
		key := fmt.Sprintf("%s/%d", show, n)
		if se, ok := seasons[key]; ok {
			return se
		}
		root := lib.Paths[0] + "/" + show
		sr, err := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindSeries, Title: show, Path: root, Poster: "/artwork/" + show})
		if err != nil {
			t.Fatal(err)
		}
		se, err := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindSeason, ParentID: sr.ID, SeasonNum: n, Path: fmt.Sprintf("%s/s%d", root, n)})
		if err != nil {
			t.Fatal(err)
		}
		seasons[key] = se
		return se
	}
	ep := func(lib Library, show string, sn, en int, title, aired string, mtime int64) Item {
		t.Helper()
		se := season(lib, show, sn)
		it, err := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindEpisode, ParentID: se.ID, Title: title,
			SeasonNum: sn, EpisodeNum: en, AirDate: aired, FileMtime: mtime, RuntimeSecs: 3000,
			Path: fmt.Sprintf("%s/e%d.mkv", se.Path, en)})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	ep(tv, "Slow Horses", 6, 4, "Hello Goodbye", "2026-10-01", at(10, 1, 22))
	e65 := ep(tv, "Slow Horses", 6, 5, "Footprints", "2026-10-08", at(10, 9, 6))
	ep(tv, "Other Show", 2, 1, "Pilot Again", "2026-10-08", at(10, 8, 23))
	ep(tv, "Old Show", 1, 1, "Back Catalogue", "2022-04-01", at(10, 13, 9)) // arrived today, aired years ago
	ep(tv, "Old Show", 1, 2, "No Date", "", at(10, 13, 9))
	ep(tv, "Slow Horses", 5, 6, "Last Season", "2025-09-24", at(9, 1, 9)) // outside the window
	ep(kids, "Cartoon", 1, 3, "Hidden", "2026-10-10", at(10, 10, 9))
	// Here since 2024, so not the episode that aired this week: a mismatch.
	ep(tv, "Format Show", 2, 9, "Mismatched", "2026-10-13", time.Date(2024, 5, 1, 14, 0, 0, 0, time.UTC).Unix())

	if _, err := s.SetWatched(ctx, LocalUser, e65.ID, true); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		user    string
		libs    []string
		want    []string
		watched string // the one title the viewer has seen, if any
	}{
		{"one library", LocalUser, []string{tv.ID}, []string{"Footprints", "Pilot Again", "Hello Goodbye"}, "Footprints"},
		{"every library", LocalUser, nil, []string{"Hidden", "Footprints", "Pilot Again", "Hello Goodbye"}, "Footprints"},
		{"no libraries", LocalUser, []string{}, []string{}, ""},
		{"another viewer", "someone-else", []string{tv.ID}, []string{"Footprints", "Pilot Again", "Hello Goodbye"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.NewEpisodes(ctx, tc.user, tc.libs, now, 20)
			if err != nil {
				t.Fatal(err)
			}
			names := make([]string, len(got))
			for i, r := range got {
				names[i] = r.Title
				if r.Watched != (r.Title == tc.watched) {
					t.Errorf("%s watched = %v", r.Title, r.Watched)
				}
				if r.SeriesTitle == "" || r.SeriesID == "" || r.SeriesPoster != "/artwork/"+r.SeriesTitle {
					t.Errorf("%s carries series %q (%q), want its own", r.Title, r.SeriesTitle, r.SeriesPoster)
				}
			}
			if fmt.Sprint(names) != fmt.Sprint(tc.want) {
				t.Fatalf("new episodes = %v, want %v", names, tc.want)
			}
		})
	}

	if got, err := s.NewEpisodes(ctx, LocalUser, nil, now, 2); err != nil || len(got) != 2 {
		t.Fatalf("limit 2 gave %d (%v)", len(got), err)
	}

	// Recently added labels a series with its newest arrival when that is
	// within the window, and leaves an untouched series alone.
	if _, err := s.UpsertItem(ctx, Item{LibraryID: tv.ID, Kind: KindSeries, Title: "Dormant", Path: "/tv/Dormant"}); err != nil {
		t.Fatal(err)
	}
	ep(tv, "Dormant", 1, 1, "Long Ago", "2019-01-01", at(1, 5, 9))
	recent, err := s.recentlyAdded(ctx, tv, 10, now)
	if err != nil {
		t.Fatal(err)
	}
	latest := map[string]string{}
	for _, sr := range recent {
		if sr.LatestEpisode != nil {
			latest[sr.Title] = fmt.Sprintf("S%d E%d %s", sr.LatestEpisode.SeasonNum, sr.LatestEpisode.EpisodeNum, sr.LatestEpisode.Title)
		}
	}
	want := map[string]string{
		"Old Show":    "S1 E2 No Date", // arrived together: the later in the running order
		"Slow Horses": "S6 E5 Footprints",
		"Other Show":  "S2 E1 Pilot Again",
	}
	if fmt.Sprint(latest) != fmt.Sprint(want) {
		t.Fatalf("latest episodes = %v, want %v", latest, want)
	}
}

func titles(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}
