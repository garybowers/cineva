package store

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestEnsureIdentityIsStable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first, err := s.EnsureIdentity(ctx)
	if err != nil {
		t.Fatalf("first EnsureIdentity: %v", err)
	}
	second, err := s.EnsureIdentity(ctx)
	if err != nil {
		t.Fatalf("second EnsureIdentity: %v", err)
	}
	if !bytes.Equal(first.PublicKey, second.PublicKey) {
		t.Fatal("identity changed between calls; it must persist")
	}
}

func TestCreateLibraryValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		libName   string
		mediaType MediaType
		paths     []string
		wantErr   bool
	}{
		{"valid movies", "Movies", MediaMovies, []string{"/media/movies"}, false},
		{"valid tv", "TV", MediaTV, []string{"/media/tv"}, false},
		{"empty name", "", MediaMovies, []string{"/media/x"}, true},
		{"bad media type", "Photos", MediaType("photos"), []string{"/media/photos"}, true},
		{"no paths", "Empty", MediaMusic, nil, true},
		{"duplicate name", "Movies", MediaMovies, []string{"/media/other"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.CreateLibrary(ctx, tt.libName, tt.mediaType, tt.paths)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CreateLibrary(%q) error = %v, wantErr %v", tt.libName, err, tt.wantErr)
			}
		})
	}
}

func TestLibraryRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, err := s.CreateLibrary(ctx, "Kids Movies", MediaMovies, []string{"/media/kids", "/media/more-kids"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Source != "local" {
		t.Errorf("source = %q, want local", created.Source)
	}
	if len(created.Paths) != 2 {
		t.Errorf("paths = %v, want 2 entries", created.Paths)
	}

	libs, err := s.Libraries(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(libs) != 1 || libs[0].ID != created.ID {
		t.Fatalf("list = %+v, want the created library", libs)
	}

	if err := s.DeleteLibrary(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeleteLibrary(ctx, created.ID); err == nil {
		t.Fatal("second delete should report not found")
	}
	if libs, _ = s.Libraries(ctx); len(libs) != 0 {
		t.Fatalf("libraries after delete = %+v, want empty", libs)
	}
}

// Guards the resume query against itemColumns/scan-list drift: adding a
// column to the items table broke ContinueWatching silently until it was
// queried, because its row scan is written out by hand.
func TestContinueWatchingSurvivesSchema(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	item, err := s.UpsertItem(ctx, Item{
		LibraryID: lib.ID, Kind: KindMovie, Title: "Film", Path: "/m/film.mkv", RuntimeSecs: 5400,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := s.SetProgress(ctx, LocalUser, item.ID, 600, item.RuntimeSecs); err != nil {
		t.Fatalf("set progress: %v", err)
	}
	got, err := s.ContinueWatching(ctx, LocalUser, nil, 10)
	if err != nil {
		t.Fatalf("continue watching: %v", err)
	}
	if len(got) != 1 || got[0].ID != item.ID || got[0].PositionSecs != 600 {
		t.Fatalf("continue watching = %+v, want the single in-progress item at 600s", got)
	}
}

// An episode's title alone does not say which show it is, so the resume row
// carries its series' title; a film carries none.
func TestContinueWatchingNamesTheSeries(t *testing.T) {
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
	series := save(Item{Kind: KindSeries, Title: "The Big Bang Theory", Path: "/tv/tbbt"})
	season := save(Item{Kind: KindSeason, ParentID: series.ID, Path: "/tv/tbbt/s1"})
	ep := save(Item{Kind: KindEpisode, ParentID: season.ID, Title: "The Big Bran Hypothesis",
		Path: "/tv/tbbt/s1/e2.mkv", RuntimeSecs: 1300})
	film := save(Item{Kind: KindMovie, Title: "Film", Path: "/tv/film.mkv", RuntimeSecs: 5400})
	for _, id := range []string{ep.ID, film.ID} {
		if _, err := s.SetProgress(ctx, LocalUser, id, 300, 5400); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ContinueWatching(ctx, LocalUser, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	for _, r := range got {
		titles[r.ID] = r.SeriesTitle
	}
	if titles[ep.ID] != "The Big Bang Theory" || titles[film.ID] != "" {
		t.Fatalf("series titles = %v, want the episode's series and nothing for the film", titles)
	}
}

// Continue Watching shows only the libraries a viewer may see, and the
// limit counts only those: items from a hidden library, however recent,
// must not push the visible ones off the row.
func TestContinueWatchingScopedToLibraries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	kids, err := s.CreateLibrary(ctx, "Kids", MediaMovies, []string{"/kids"})
	if err != nil {
		t.Fatal(err)
	}
	grown, err := s.CreateLibrary(ctx, "Grown-ups", MediaMovies, []string{"/grown"})
	if err != nil {
		t.Fatal(err)
	}
	start := func(lib Library, name, at string) Item {
		t.Helper()
		it, err := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindMovie, Title: name, Path: "/" + name + ".mkv", RuntimeSecs: 5400})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetProgress(ctx, LocalUser, it.ID, 600, it.RuntimeSecs); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE play_state SET updated_at = ? WHERE item_id = ?`, at, it.ID); err != nil {
			t.Fatal(err)
		}
		return it
	}
	cartoon := start(kids, "cartoon", "2026-01-01 00:00:00")
	start(grown, "thriller", "2026-02-01 00:00:00")
	start(grown, "horror", "2026-03-01 00:00:00")

	for _, tc := range []struct {
		name  string
		libs  []string
		limit int
		want  []string
	}{
		{"every library", nil, 10, []string{"horror", "thriller", "cartoon"}},
		{"one library, limit after scoping", []string{kids.ID}, 1, []string{cartoon.Title}},
		{"no libraries at all", []string{}, 10, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.ContinueWatching(ctx, LocalUser, tc.libs, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, r := range got {
				names = append(names, r.Title)
			}
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("continue watching = %v, want %v", names, tc.want)
			}
		})
	}
}

func TestPlaysByDayHasContinuousSpine(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	item, err := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindMovie, Title: "Film", Path: "/m/f.mkv"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.RecordPlay(ctx, LocalUser, item.ID, KindMovie); err != nil {
			t.Fatalf("record play: %v", err)
		}
	}
	days, err := s.PlaysByDay(ctx, 7)
	if err != nil {
		t.Fatalf("plays by day: %v", err)
	}
	if len(days) != 7 {
		t.Fatalf("got %d days, want 7 (empty days must be present)", len(days))
	}
	today := days[6]
	if today.Movies != 3 || today.Episodes != 0 || today.Tracks != 0 {
		t.Fatalf("today = %+v, want 3 movie plays", today)
	}
	for _, d := range days[:6] {
		if d.Movies+d.Episodes+d.Tracks != 0 {
			t.Fatalf("unexpected plays on %s: %+v", d.Date, d)
		}
	}
}

func TestAuditLogFilterAndOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, a := range []string{"scan.start", "play.start", "scan.completed"} {
		if err := s.RecordAudit(ctx, AuditEntry{UserID: "local", Action: a, TargetTitle: a}); err != nil {
			t.Fatalf("record %s: %v", a, err)
		}
	}
	all, err := s.AuditLog(ctx, 10, 0, "")
	if err != nil || len(all) != 3 || all[0].Action != "scan.completed" {
		t.Fatalf("all = %+v, %v; want 3 newest-first", all, err)
	}
	scans, err := s.AuditLog(ctx, 10, 0, "scan.")
	if err != nil || len(scans) != 2 {
		t.Fatalf("scan. filter = %+v, %v; want 2", scans, err)
	}
}

func TestAdjacentEpisodesCrossSeasons(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, _ := s.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	series, _ := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindSeries, Title: "Show", Path: "/tv/show"})
	var eps []Item
	for season := 1; season <= 2; season++ {
		se, _ := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindSeason, ParentID: series.ID, Title: "S", SeasonNum: season, Path: fmt.Sprintf("/tv/show/s%d", season)})
		for n := 1; n <= 2; n++ {
			e, _ := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindEpisode, ParentID: se.ID, Title: "E", SeasonNum: season, EpisodeNum: n, Path: fmt.Sprintf("/tv/show/s%de%d.mkv", season, n)})
			eps = append(eps, e)
		}
	}
	if p, ok, _ := s.PrevEpisode(ctx, eps[0].ID); ok {
		t.Fatalf("first episode has a previous: %+v", p)
	}
	if n, ok, _ := s.NextEpisode(ctx, eps[3].ID); ok {
		t.Fatalf("last episode has a next: %+v", n)
	}
	if p, ok, _ := s.PrevEpisode(ctx, eps[2].ID); !ok || p.ID != eps[1].ID {
		t.Fatalf("prev of S02E01 = %+v, want S01E02", p)
	}
	if n, ok, _ := s.NextEpisode(ctx, eps[1].ID); !ok || n.ID != eps[2].ID {
		t.Fatalf("next of S01E02 = %+v, want S02E01", n)
	}
}

// Multi-version grouping: the same film held twice should show once, with
// the better copy chosen as the primary.
func TestGroupVersions(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	add := func(title string, year, height int, size int64, path string) Item {
		it, err := st.UpsertItem(ctx, Item{
			LibraryID: lib.ID, Kind: KindMovie, Title: title, Year: year,
			Height: height, SizeBytes: size, Path: path,
		})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	// Two copies of one film, plus an unrelated one.
	sd := add("Heat", 1995, 1080, 8_000_000_000, "/m/Heat/Heat 1080p.mkv")
	hd := add("Heat", 1995, 2160, 40_000_000_000, "/m/Heat/Heat 2160p.mkv")
	solo := add("Alien", 1979, 1080, 9_000_000_000, "/m/Alien/Alien.mkv")
	// Same title, different year — a remake, not a version.
	remake := add("Heat", 2020, 1080, 7_000_000_000, "/m/Heat (2020)/Heat.mkv")

	linked, err := st.GroupVersions(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if linked != 1 {
		t.Errorf("linked %d versions, want 1", linked)
	}

	listed, err := st.ItemsByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, it := range listed {
		got[it.ID] = true
	}
	if got[sd.ID] {
		t.Error("the lesser copy should not appear in the library grid")
	}
	for _, want := range []Item{hd, solo, remake} {
		if !got[want.ID] {
			t.Errorf("%s (%d) missing from the grid", want.Title, want.Year)
		}
	}

	versions, err := st.VersionsOf(ctx, hd.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].ID != sd.ID {
		t.Errorf("VersionsOf(2160p) = %d items, want the 1080p copy", len(versions))
	}

	// Re-running must be idempotent, not cumulative.
	if linked, err = st.GroupVersions(ctx, lib.ID); err != nil || linked != 1 {
		t.Errorf("second run linked %d (err %v), want 1 — grouping is not idempotent", linked, err)
	}
}

func TestGroupVersionsByTmdbID(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(title string, year, tmdbID, height int, size int64) Item {
		it, err := st.UpsertItem(ctx, Item{
			LibraryID: lib.ID, Kind: KindMovie, Title: title, Year: year,
			TmdbID: tmdbID, Height: height, SizeBytes: size,
			Path: fmt.Sprintf("/m/%s-%d.mkv", title, height),
		})
		if err != nil {
			t.Fatalf("upsert %s: %v", title, err)
		}
		return it
	}

	// Same film, same TMDB id, but the titles and years disagree — exactly
	// what title+year grouping could not handle.
	uhd := mk("The Matrix", 1999, 603, 2160, 60<<30)
	hd := mk("Matrix, The", 0, 603, 1080, 8<<30)
	// A different film sharing neither id nor title.
	other := mk("Heat", 1995, 949, 1080, 9<<30)
	// Unmatched pair (no tmdb id) still groups on title+year.
	dupA := mk("Unmatched Film", 2001, 0, 1080, 5<<30)
	dupB := mk("Unmatched Film", 2001, 0, 720, 2<<30)

	n, gerr := st.GroupVersions(ctx, lib.ID)
	if gerr != nil {
		t.Fatalf("group versions: %v", gerr)
	}
	if n != 2 {
		t.Errorf("grouped %d rows, want 2", n)
	}

	get := func(id string) Item {
		it, err := st.Item(ctx, id)
		if err != nil {
			t.Fatalf("item %s: %v", id, err)
		}
		return it
	}
	// The 4K copy is the primary; the 1080p one hangs off it.
	if got := get(uhd.ID).VersionOf; got != "" {
		t.Errorf("4K copy should be the primary, got version_of=%q", got)
	}
	if got := get(hd.ID).VersionOf; got != uhd.ID {
		t.Errorf("1080p copy version_of = %q, want %q (grouped by tmdb id)", got, uhd.ID)
	}
	if got := get(other.ID).VersionOf; got != "" {
		t.Errorf("unrelated film was grouped: version_of=%q", got)
	}
	if got := get(dupA.ID).VersionOf; got != "" {
		t.Errorf("best unmatched copy should be primary, got %q", got)
	}
	if got := get(dupB.ID).VersionOf; got != dupA.ID {
		t.Errorf("unmatched duplicate version_of = %q, want %q (title+year fallback)", got, dupA.ID)
	}
}

func TestLibraryCollections(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	add := func(title string, year, collID int, collName, poster string) Item {
		it, err := st.UpsertItem(ctx, Item{
			LibraryID: lib.ID, Kind: KindMovie, Title: title, Year: year,
			CollectionID: collID, CollectionName: collName, Poster: poster,
			Path: fmt.Sprintf("/m/%s.mkv", title),
		})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	add("The Matrix", 1999, 2344, "The Matrix Collection", "/artwork/m1.jpg")
	add("The Matrix Reloaded", 2003, 2344, "The Matrix Collection", "/artwork/m2.jpg")
	add("The Matrix Revolutions", 2003, 2344, "The Matrix Collection", "")
	// A collection with only one film held is not grouping, it is noise.
	add("Alien", 1979, 8091, "Alien Collection", "/artwork/a.jpg")
	// No collection at all.
	add("Heat", 1995, 0, "", "/artwork/h.jpg")

	cols, err := st.LibraryCollections(ctx, "", lib.ID)
	if err != nil {
		t.Fatalf("collections: %v", err)
	}
	if len(cols) != 1 {
		t.Fatalf("got %d collections, want 1 (singletons excluded): %+v", len(cols), cols)
	}
	if cols[0].TmdbID != 2344 || cols[0].Count != 3 {
		t.Errorf("collection = %+v, want tmdb 2344 count 3", cols[0])
	}
	if cols[0].Key != "tmdb:2344" || cols[0].Managed {
		t.Errorf("key = %q managed=%v, want tmdb:2344 and unmanaged", cols[0].Key, cols[0].Managed)
	}
	// The stand-in poster is the earliest film's, not an arbitrary row's.
	if cols[0].Poster != "/artwork/m1.jpg" {
		t.Errorf("poster = %q, want the earliest film's", cols[0].Poster)
	}

	items, err := st.ItemsByCollection(ctx, lib.ID, "tmdb:2344")
	if err != nil {
		t.Fatalf("items by collection: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	// Release order: that is how a series is watched.
	if items[0].Title != "The Matrix" || items[0].Year != 1999 {
		t.Errorf("first item = %q (%d), want The Matrix (1999)", items[0].Title, items[0].Year)
	}
	if items[1].Year != 2003 || items[2].Year != 2003 {
		t.Errorf("sequels out of order: %+v", []string{items[1].Title, items[2].Title})
	}
}

func TestSearchItemsAcrossFields(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	add := func(title string, year int) Item {
		it, err := st.UpsertItem(ctx, Item{
			LibraryID: lib.ID, Kind: KindMovie, Title: title, Year: year,
			Path: fmt.Sprintf("/m/%s.mkv", title),
		})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	goodfellas := add("Goodfellas", 1990)
	casino := add("Casino", 1995)
	heat := add("Heat", 1995)
	_ = heat

	// Credits: Scorsese directed two of them.
	for _, id := range []string{goodfellas.ID, casino.ID} {
		if err := st.ReplaceCredits(ctx, id, nil, []Person{
			{TmdbID: 1032, Name: "Martin Scorsese", Role: "Director"},
		}); err != nil {
			t.Fatalf("credits: %v", err)
		}
	}

	names := func(items []Item) []string {
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = it.Title
		}
		return out
	}

	t.Run("matches a title", func(t *testing.T) {
		got, err := st.SearchItems(ctx, "Goodfellas", 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Title != "Goodfellas" {
			t.Errorf("got %v, want [Goodfellas]", names(got))
		}
	})

	t.Run("matches a person", func(t *testing.T) {
		got, err := st.SearchItems(ctx, "Scorsese", 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("got %v, want the two Scorsese films", names(got))
		}
	})

	t.Run("matches a year", func(t *testing.T) {
		got, err := st.SearchItems(ctx, "1995", 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Errorf("got %v, want the two 1995 films", names(got))
		}
	})

	t.Run("a title match outranks a person match", func(t *testing.T) {
		// "Casino" is a title, and is also a Scorsese film; searching the
		// title must not bury it under person matches.
		got, err := st.SearchItems(ctx, "Casino", 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 || got[0].Title != "Casino" {
			t.Errorf("got %v, want Casino first", names(got))
		}
	})

	t.Run("a non-year number does not match every runtime", func(t *testing.T) {
		got, err := st.SearchItems(ctx, "12", 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want nothing", names(got))
		}
	})
}

func TestCollectionProgressAndNextUp(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := st.CreateUser(ctx, "viewer", "Viewer", "hash", "user")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	add := func(title string, year int) Item {
		it, err := st.UpsertItem(ctx, Item{
			LibraryID: lib.ID, Kind: KindMovie, Title: title, Year: year,
			CollectionID: 1241, CollectionName: "Harry Potter Collection",
			RuntimeSecs: 9000, Path: fmt.Sprintf("/m/%s.mkv", title),
		})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	one := add("Philosopher's Stone", 2001)
	two := add("Chamber of Secrets", 2002)
	three := add("Prisoner of Azkaban", 2004)

	get := func() Collection {
		cols, err := st.LibraryCollections(ctx, user.ID, lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != 1 {
			t.Fatalf("got %d collections, want 1", len(cols))
		}
		return cols[0]
	}

	// Nothing watched: next up is the earliest film.
	c := get()
	if c.Count != 3 || c.Watched != 0 {
		t.Errorf("fresh collection = %d/%d, want 0/3", c.Watched, c.Count)
	}
	if c.NextUpID != one.ID {
		t.Errorf("next up = %q, want the first film", c.NextUpName)
	}

	// Watching the first advances next-up to the second, in release order
	// rather than alphabetical ("Chamber" < "Philosopher's" alphabetically).
	if _, err := st.SetWatched(ctx, user.ID, one.ID, true); err != nil {
		t.Fatal(err)
	}
	c = get()
	if c.Watched != 1 {
		t.Errorf("watched = %d, want 1", c.Watched)
	}
	if c.NextUpID != two.ID {
		t.Errorf("next up = %q, want Chamber of Secrets", c.NextUpName)
	}

	// Watching out of order still points at the earliest unwatched.
	if _, err := st.SetWatched(ctx, user.ID, three.ID, true); err != nil {
		t.Fatal(err)
	}
	c = get()
	if c.Watched != 2 || c.NextUpID != two.ID {
		t.Errorf("got %d/%d next=%q, want 2/3 next=Chamber of Secrets", c.Watched, c.Count, c.NextUpName)
	}

	// Finishing the set leaves no next-up.
	if _, err := st.SetWatched(ctx, user.ID, two.ID, true); err != nil {
		t.Fatal(err)
	}
	c = get()
	if c.Watched != 3 || c.NextUpID != "" {
		t.Errorf("completed collection = %d/%d next=%q, want 3/3 and no next", c.Watched, c.Count, c.NextUpID)
	}

	// Another viewer's progress is their own.
	other, err := st.CreateUser(ctx, "other", "Other", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	cols, err := st.LibraryCollections(ctx, other.ID, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cols[0].Watched != 0 || cols[0].NextUpID != one.ID {
		t.Errorf("second viewer sees %d watched, next %q — progress leaked", cols[0].Watched, cols[0].NextUpName)
	}
}

// Every field written by UpsertItem must come back from Item(). This exists
// because `certification` was once added to the struct, the INSERT and the
// UPDATE but not to itemColumns/itemScanTargets, so it was stored and never
// read — invisible to every test that did not look for that one field.
func TestItemRoundTripsEveryField(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	want := Item{
		LibraryID: lib.ID, Kind: KindMovie, Title: "The Matrix", Year: 1999,
		Path: "/m/matrix.mkv", SizeBytes: 12345, RuntimeSecs: 8160,
		Container: "mkv", VideoCodec: "h264", AudioCodec: "eac3",
		Width: 1920, Height: 1080, TmdbID: 603,
		Overview: "A hacker learns the truth.", Poster: "/artwork/p.jpg",
		Rating: 8.2, VoteCount: 24000, Tagline: "Welcome to the Real World.",
		Backdrop: "/artwork/b.jpg", CollectionID: 2344,
		CollectionName: "The Matrix Collection", Certification: "15",
		SeasonNum: 1, EpisodeNum: 2, DiscNum: 1, TrackNum: 3, Artist: "n/a",
		Status: "Ended", Network: "HBO", AirDate: "1999-03-31",
		SeasonCount: 5, EpisodeCount: 62,
		LastAirDate: "2013-09-29", NextAirDate: "2026-01-01",
		OriginalTitle: "Breaking Bad (original)", Country: "US",
	}
	// Upserted twice on purpose, with *different* values the second time.
	// The first call inserts, the second takes the UPDATE branch — the one
	// every rescan uses, and the one a single upsert leaves untested. The
	// values have to differ or the test proves nothing: the UPDATE keeps the
	// existing value when handed an empty one, so re-upserting the same item
	// passes whether or not the column is in the statement at all.
	first, err := st.UpsertItem(ctx, want)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Kept for the zero-check below: bumpFields makes every field non-zero,
	// so checking after it would pass for a field this test never set — the
	// exact vacuous-pass this guard exists to prevent.
	seeded := want
	want = bumpFields(want)
	saved, err := st.UpsertItem(ctx, want)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if saved.ID != first.ID {
		t.Fatalf("second upsert created a second row (%s then %s)", first.ID, saved.ID)
	}
	got, err := st.Item(ctx, saved.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	// Walked by reflection rather than listed by hand. The hand-written
	// list this replaced went on passing when `status`, `network`,
	// `air_date`, `season_count` and `episode_count` were added — a test
	// that cannot fail for the thing it exists to catch is worse than none,
	// because it is believed.
	//
	// Anything not set by the caller is skipped and named, so a new field
	// has to be deliberately classified rather than silently ignored.
	serverSet := map[string]bool{
		"ID": true, "LibraryID": true, "AddedAt": true, "UpdatedAt": true,
		"Kind": true, "ParentID": true, "Path": true,
	}
	elsewhere := map[string]bool{
		"LockedFields": true, // its own CSV column, checked below
		"Genres":       true, // its own table, see ReplaceGenres
		"Watched":      true, // per-user play state, not an item column
		"PositionSecs": true,
		"WatchedAt":    true,
		"VersionOf":    true, // set by version grouping, not the caller
		"FileMtime":    true, // write-only; read back through FileStamps, below
	}
	wv, gv := reflect.ValueOf(want), reflect.ValueOf(got)
	typ := wv.Type()
	var checked int
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if serverSet[name] || elsewhere[name] {
			continue
		}
		if reflect.ValueOf(seeded).Field(i).IsZero() {
			t.Errorf("%s was left zero by this test's fixture, so it proves nothing about that column", name)
			continue
		}
		if !reflect.DeepEqual(wv.Field(i).Interface(), gv.Field(i).Interface()) {
			t.Errorf("%s = %v, want %v — stored but not read back?", name, gv.Field(i).Interface(), wv.Field(i).Interface())
		}
		checked++
	}
	if checked < 20 {
		t.Errorf("only %d fields checked; the walk is not reaching the struct", checked)
	}

	// The file stamp a rescan compares against is read back separately.
	stamps, err := st.FileStamps(ctx, saved.LibraryID)
	if err != nil {
		t.Fatalf("file stamps: %v", err)
	}
	if want.FileMtime == 0 || stamps[saved.Path].FileMtime != want.FileMtime {
		t.Errorf("FileMtime = %d, want %d", stamps[saved.Path].FileMtime, want.FileMtime)
	}

	// Locked fields round-trip through their own CSV column.
	if err := st.SetLockedFields(ctx, saved.ID, []string{"title", "year"}); err != nil {
		t.Fatalf("set locked: %v", err)
	}
	got, err = st.Item(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsLocked("title") || !got.IsLocked("year") || got.IsLocked("overview") {
		t.Errorf("LockedFields = %v, want [title year]", got.LockedFields)
	}
}

func TestLockedFieldsSurviveRescan(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	orig, err := st.UpsertItem(ctx, Item{
		LibraryID: lib.ID, Kind: KindMovie, Path: "/m/f.mkv",
		Title: "Wrong Title", Year: 1990, Overview: "provider text",
		Certification: "PG", Rating: 5.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The operator corrects the title and certification.
	fixed, err := st.UpdateItem(ctx, orig.ID, ItemEdit{
		Title:         ptr("Right Title"),
		Certification: ptr("18"),
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if fixed.Title != "Right Title" || fixed.Certification != "18" {
		t.Fatalf("edit did not apply: %+v", fixed)
	}

	// A rescan re-upserts the same path with the provider's values again.
	after, err := st.UpsertItem(ctx, Item{
		LibraryID: lib.ID, Kind: KindMovie, Path: "/m/f.mkv",
		Title: "Wrong Title", Year: 1991, Overview: "newer provider text",
		Certification: "PG", Rating: 6.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "Right Title" {
		t.Errorf("title = %q — the rescan undid the correction", after.Title)
	}
	if after.Certification != "18" {
		t.Errorf("certification = %q — the rescan undid the correction", after.Certification)
	}
	// Unlocked fields are still the provider's to update.
	if after.Year != 1991 {
		t.Errorf("year = %d, want 1991 — unlocked fields should still refresh", after.Year)
	}
	if after.Overview != "newer provider text" {
		t.Errorf("overview = %q — unlocked fields should still refresh", after.Overview)
	}
	if after.Rating != 6.0 {
		t.Errorf("rating = %v, want 6 — unlocked fields should still refresh", after.Rating)
	}
}

func ptr[T any](v T) *T { return &v }

func TestItemsByLibrarySinceReturnsOnlyChanges(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	add := func(title string) Item {
		it, err := st.UpsertItem(ctx, Item{
			LibraryID: lib.ID, Kind: KindMovie, Title: title,
			Path: fmt.Sprintf("/m/%s.mkv", title),
		})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	add("A")
	add("B")

	all, mark, err := st.ItemsByLibrarySince(ctx, lib.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d items, want 2", len(all))
	}
	if mark == "" {
		t.Fatal("no high-water mark returned")
	}

	// Nothing has changed: asking from the mark returns the items stamped at
	// exactly that moment (the query is inclusive, so a change in the same
	// second is never missed) and never more than the whole library.
	same, _, err := st.ItemsByLibrarySince(ctx, lib.ID, mark)
	if err != nil {
		t.Fatal(err)
	}
	if len(same) > 2 {
		t.Errorf("got %d items from the mark, want at most 2", len(same))
	}

	// Age the existing rows so a new one is distinguishable. SQLite's
	// datetime('now') has one-second resolution, which is exactly why the
	// query is `>=` rather than `>`: items written in the same second as
	// the mark must be re-sent rather than silently dropped.
	if _, err := st.db.ExecContext(ctx,
		`UPDATE items SET updated_at = datetime('now', '-10 seconds')`); err != nil {
		t.Fatal(err)
	}
	c := add("C")

	var since string
	if err := st.db.QueryRowContext(ctx, `SELECT datetime('now', '-5 seconds')`).Scan(&since); err != nil {
		t.Fatal(err)
	}
	delta, newMark, err := st.ItemsByLibrarySince(ctx, lib.ID, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta) != 1 || delta[0].ID != c.ID {
		titles := make([]string, len(delta))
		for i, d := range delta {
			titles[i] = d.Title
		}
		t.Errorf("delta = %v, want just [C]", titles)
	}
	if newMark <= since {
		t.Errorf("mark did not advance past the request: %q -> %q", since, newMark)
	}

	// The inclusive boundary, stated as a guarantee rather than left to be
	// rediscovered: asking from a mark returns anything stamped at it.
	atMark, _, err := st.ItemsByLibrarySince(ctx, lib.ID, newMark)
	if err != nil {
		t.Fatal(err)
	}
	if len(atMark) != 1 || atMark[0].ID != c.ID {
		t.Errorf("items at the mark are not re-sent; a same-second write could be missed")
	}

	// An alternative version must not appear in either form: the grid lists
	// primaries only.
	v, err := st.UpsertItem(ctx, Item{
		LibraryID: lib.ID, Kind: KindMovie, Title: "C", Path: "/m/C-4k.mkv",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.GroupVersions(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	after, _, err := st.ItemsByLibrarySince(ctx, lib.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range after {
		if it.ID == v.ID && it.VersionOf != "" {
			t.Errorf("an alternative version leaked into the listing")
		}
	}
}

func TestPruneHistory(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	u, err := st.CreateUser(ctx, "viewer", "Viewer", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	lib, err := st.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	it, err := st.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindMovie, Title: "F", Path: "/m/f.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	// Two of each: one recent, one aged past any sane retention window.
	for i := 0; i < 2; i++ {
		if _, err := st.RecordPlay(ctx, u.ID, it.ID, KindMovie); err != nil {
			t.Fatal(err)
		}
		if err := st.RecordAudit(ctx, AuditEntry{UserID: u.ID, Action: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`UPDATE play_events SET started_at = datetime('now','-100 days') WHERE rowid = (SELECT MIN(rowid) FROM play_events)`,
		`UPDATE audit_log SET at = datetime('now','-100 days') WHERE id = (SELECT MIN(id) FROM audit_log)`,
	} {
		if _, err := st.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	// Zero disables pruning entirely — an operator may want the lot.
	if a, p, err := st.PruneHistory(ctx, 0); err != nil || a != 0 || p != 0 {
		t.Fatalf("PruneHistory(0) = (%d, %d, %v), want nothing pruned", a, p, err)
	}

	a, p, err := st.PruneHistory(ctx, 30)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if a != 1 || p != 1 {
		t.Errorf("pruned %d audit and %d play rows, want 1 each", a, p)
	}
	// The recent rows survive.
	var n int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d audit rows left, want 1", n)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM play_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d play rows left, want 1", n)
	}
}

// Dashboard samples are persisted so the charts survive a restart.
func TestStatsSamplesRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	for i, cpu := range []float64{10.5, 20.25, 30} {
		if err := st.AppendStatsSample(ctx, StatsSample{
			At: base.Add(time.Duration(i) * time.Minute), CPUPct: cpu,
			MemUsed: 1 << 30, MemTotal: 8 << 30, ProcRSS: 1 << 20, NumCPU: 16,
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := st.StatsSamplesSince(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d samples, want 3", len(got))
	}
	// Oldest first, and the float survives the round trip.
	if got[0].CPUPct != 10.5 || got[2].CPUPct != 30 {
		t.Errorf("values out of order or mangled: %v", got)
	}
	if !got[0].At.Equal(base) {
		t.Errorf("time round-tripped as %v, want %v", got[0].At, base)
	}
	if got[0].MemTotal != 8<<30 || got[0].NumCPU != 16 {
		t.Errorf("fields lost: %+v", got[0])
	}

	// `since` is a real filter, not decoration.
	recent, err := st.StatsSamplesSince(ctx, base.Add(90*time.Second))
	if err != nil {
		t.Fatalf("filtered read: %v", err)
	}
	if len(recent) != 1 || recent[0].CPUPct != 30 {
		t.Errorf("since filter returned %v", recent)
	}

	// Same minute twice replaces rather than duplicating.
	if err := st.AppendStatsSample(ctx, StatsSample{At: base, CPUPct: 99, NumCPU: 16}); err != nil {
		t.Fatalf("re-append: %v", err)
	}
	got, _ = st.StatsSamplesSince(ctx, base.Add(-time.Hour))
	if len(got) != 3 {
		t.Fatalf("duplicate inserted: %d rows", len(got))
	}
	if got[0].CPUPct != 99 {
		t.Errorf("conflicting insert did not update: %v", got[0].CPUPct)
	}

	n, err := st.PruneStatsSamples(ctx, base.Add(90*time.Second))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 2 {
		t.Errorf("pruned %d rows, want 2", n)
	}
	left, _ := st.StatsSamplesSince(ctx, base.Add(-time.Hour))
	if len(left) != 1 {
		t.Errorf("%d rows survived the prune, want 1", len(left))
	}
}

// bumpFields returns a copy of an item with every refreshable field changed,
// so an update can be told apart from the insert that preceded it. Identity
// and server-set fields are left alone: changing Path would address a
// different row entirely.
func bumpFields(it Item) Item {
	out := it
	v := reflect.ValueOf(&out).Elem()
	typ := v.Type()
	keep := map[string]bool{
		"ID": true, "LibraryID": true, "Kind": true, "ParentID": true, "Path": true,
		"AddedAt": true, "UpdatedAt": true, "VersionOf": true,
		"LockedFields": true, "Genres": true,
		"Watched": true, "PositionSecs": true, "WatchedAt": true,
	}
	for i := 0; i < typ.NumField(); i++ {
		if keep[typ.Field(i).Name] {
			continue
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(f.String() + "-v2")
		case reflect.Int, reflect.Int64:
			f.SetInt(f.Int() + 7)
		case reflect.Float64:
			f.SetFloat(f.Float() + 0.5)
		}
	}
	return out
}
