package store

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
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
	got, err := s.ContinueWatching(ctx, LocalUser, 10)
	if err != nil {
		t.Fatalf("continue watching: %v", err)
	}
	if len(got) != 1 || got[0].ID != item.ID || got[0].PositionSecs != 600 {
		t.Fatalf("continue watching = %+v, want the single in-progress item at 600s", got)
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
		if err := s.RecordPlay(ctx, LocalUser, item.ID, KindMovie); err != nil {
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
	}
	saved, err := st.UpsertItem(ctx, want)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := st.Item(ctx, saved.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"Title", got.Title, want.Title},
		{"Year", got.Year, want.Year},
		{"Path", got.Path, want.Path},
		{"SizeBytes", got.SizeBytes, want.SizeBytes},
		{"RuntimeSecs", got.RuntimeSecs, want.RuntimeSecs},
		{"Container", got.Container, want.Container},
		{"VideoCodec", got.VideoCodec, want.VideoCodec},
		{"AudioCodec", got.AudioCodec, want.AudioCodec},
		{"Width", got.Width, want.Width},
		{"Height", got.Height, want.Height},
		{"TmdbID", got.TmdbID, want.TmdbID},
		{"Overview", got.Overview, want.Overview},
		{"Poster", got.Poster, want.Poster},
		{"Rating", got.Rating, want.Rating},
		{"VoteCount", got.VoteCount, want.VoteCount},
		{"Tagline", got.Tagline, want.Tagline},
		{"Backdrop", got.Backdrop, want.Backdrop},
		{"CollectionID", got.CollectionID, want.CollectionID},
		{"CollectionName", got.CollectionName, want.CollectionName},
		{"Certification", got.Certification, want.Certification},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v — stored but not read back?", c.field, c.got, c.want)
		}
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
