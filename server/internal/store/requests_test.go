package store

import (
	"context"
	"errors"
	"testing"
)

func TestMediaRequests(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	kid, err := s.CreateUser(ctx, "kid", "The Kid", "h", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if kid.RequestLevel != RequestApproval || kid.EffectiveRequestLevel() != RequestApproval {
		t.Fatalf("new user's request level = %q", kid.RequestLevel)
	}
	admin, err := s.CreateUser(ctx, "gary", "", "h", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRequestLevel(ctx, admin.ID, RequestNone); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.UserByID(ctx, admin.ID); u.EffectiveRequestLevel() != RequestAuto {
		t.Fatalf("an admin's effective level = %q, want auto whatever is stored", u.EffectiveRequestLevel())
	}
	if err := s.SetRequestLevel(ctx, kid.ID, "sometimes"); err == nil {
		t.Fatal("unknown request level accepted")
	}

	for _, tc := range []struct {
		name string
		req  MediaRequest
	}{
		{"no media type", MediaRequest{UserID: kid.ID, TmdbID: 1, Title: "x"}},
		{"no tmdb id", MediaRequest{UserID: kid.ID, MediaType: RequestMovie, Title: "x"}},
		{"film with seasons", MediaRequest{UserID: kid.ID, MediaType: RequestMovie, TmdbID: 1, Title: "x", Seasons: []int{1}}},
		{"bad status", MediaRequest{UserID: kid.ID, MediaType: RequestMovie, TmdbID: 1, Title: "x", Status: "lost"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.CreateRequest(ctx, tc.req); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}

	film, err := s.CreateRequest(ctx, MediaRequest{UserID: kid.ID, MediaType: RequestMovie, TmdbID: 603, Title: "The Matrix", Year: 1999})
	if err != nil {
		t.Fatal(err)
	}
	if film.Status != RequestRequested || film.UserName != "The Kid" || !film.Open() {
		t.Fatalf("film request = %+v", film)
	}
	show, err := s.CreateRequest(ctx, MediaRequest{
		UserID: admin.ID, MediaType: RequestTV, TmdbID: 1396, Title: "Breaking Bad",
		Seasons: []int{3, 1, 3}, Episodes: []EpisodeRef{{2, 4}, {1, 1}, {2, 4}}, Status: RequestApproved,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(show.Seasons) != 2 || show.Seasons[0] != 1 || len(show.Episodes) != 2 || show.Episodes[0] != (EpisodeRef{1, 1}) {
		t.Fatalf("scope not sorted and de-duplicated: %+v %+v", show.Seasons, show.Episodes)
	}
	whole, err := s.CreateRequest(ctx, MediaRequest{UserID: kid.ID, MediaType: RequestTV, TmdbID: 1399, Title: "Game of Thrones"})
	if err != nil {
		t.Fatal(err)
	}
	if !whole.WholeSeries() || show.WholeSeries() {
		t.Fatal("WholeSeries is wrong")
	}

	for _, tc := range []struct {
		name string
		f    RequestFilter
		want int
	}{
		{"everything", RequestFilter{}, 3},
		{"one user", RequestFilter{UserID: kid.ID}, 2},
		{"pending", RequestFilter{Statuses: []string{RequestRequested}}, 2},
		{"several statuses", RequestFilter{Statuses: []string{RequestRequested, RequestApproved}}, 3},
		{"by title", RequestFilter{MediaType: RequestTV, TmdbID: 1396}, 1},
		{"limited", RequestFilter{Limit: 1}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Requests(ctx, tc.f)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Fatalf("got %d requests, want %d", len(got), tc.want)
			}
		})
	}

	film.Status, film.Note, film.ArrID, film.DecidedBy = RequestDeclined, "Not tonight", 7, admin.ID
	film.Title = "ignored" // who asked for what is fixed at creation
	if film, err = s.UpdateRequest(ctx, film); err != nil {
		t.Fatal(err)
	}
	if film.Status != RequestDeclined || film.Note != "Not tonight" || film.ArrID != 7 || film.Title != "The Matrix" || film.Open() {
		t.Fatalf("updated = %+v", film)
	}
	if err := s.DeleteRequest(ctx, film.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Request(ctx, film.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted request still there: %v", err)
	}
	if err := s.DeleteRequest(ctx, film.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting twice: %v", err)
	}

	// A user's requests go with them.
	if err := s.DeleteUser(ctx, kid.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.Requests(ctx, RequestFilter{}); len(left) != 1 {
		t.Fatalf("%d requests left after deleting their owner, want 1", len(left))
	}
}

func TestLibraryLookupsByTmdb(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	films, err := s.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	tv, err := s.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	up := func(it Item) Item {
		t.Helper()
		saved, err := s.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	up(Item{LibraryID: films.ID, Kind: KindMovie, Title: "The Matrix", TmdbID: 603, Path: "/m/matrix.mkv"})
	up(Item{LibraryID: films.ID, Kind: KindMovie, Title: "The Matrix", TmdbID: 603, Path: "/m/matrix-4k.mkv", Height: 2160})
	up(Item{LibraryID: films.ID, Kind: KindMovie, Title: "Heat", TmdbID: 949, Path: "/m/heat.mkv"})
	series := up(Item{LibraryID: tv.ID, Kind: KindSeries, Title: "Breaking Bad", TmdbID: 1396, Path: "/tv/bb"})
	s1 := up(Item{LibraryID: tv.ID, Kind: KindSeason, ParentID: series.ID, Title: "Season 01", SeasonNum: 1, Path: "/tv/bb/s1"})
	up(Item{LibraryID: tv.ID, Kind: KindSeason, ParentID: series.ID, Title: "Season 02", SeasonNum: 2, Path: "/tv/bb/s2"})
	for _, n := range []int{1, 2} {
		up(Item{LibraryID: tv.ID, Kind: KindEpisode, ParentID: s1.ID, Title: "Ep", SeasonNum: 1, EpisodeNum: n, Path: "/tv/bb/s1/e" + string(rune('0'+n)) + ".mkv"})
	}

	if _, err := s.GroupVersions(ctx, films.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ItemsByTmdbIDs(ctx, KindMovie, []int{603, 949, 1396, 42})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("films by tmdb id = %d, want 2 (versions and series left out)", len(got))
	}
	if none, err := s.ItemsByTmdbIDs(ctx, KindSeries, nil); err != nil || len(none) != 0 {
		t.Fatalf("no ids: %v %v", none, err)
	}

	disk, found, err := s.SeriesOnDisk(ctx, 1396)
	if err != nil || !found {
		t.Fatalf("series on disk: %v %v", found, err)
	}
	if !disk[1][1] || !disk[1][2] || disk[1][3] || disk[2] == nil || len(disk[2]) != 0 {
		t.Fatalf("on disk = %v", disk)
	}
	if _, found, err := s.SeriesOnDisk(ctx, 1399); err != nil || found {
		t.Fatalf("a series not held was found: %v", err)
	}
}

func TestArrOptionsAndEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	c, err := s.CreateArrConnection(ctx, ArrConnection{Kind: "sonarr", Name: "Sonarr", URL: "http://s", APIKey: "k", CallbackURL: "http://c"})
	if err != nil {
		t.Fatal(err)
	}
	if c.PathMaps == nil || len(c.PathMaps) != 0 {
		t.Fatalf("fresh path maps = %#v", c.PathMaps)
	}
	maps := []PathMap{{From: "/tv", To: "/mnt/media/TV"}}
	if err := s.SetArrOptions(ctx, c.ID, 4, "/tv", maps); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordArrEvent(ctx, c.ID, "Download", "Show S02E04"); err != nil {
		t.Fatal(err)
	}
	// Re-registering the webhook rewrites the connection but must leave
	// the options and the last event alone.
	c.Version = "4.0.1"
	if _, err := s.UpdateArrConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := s.ArrConnection(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.QualityProfileID != 4 || got.RootFolder != "/tv" || len(got.PathMaps) != 1 || got.PathMaps[0] != maps[0] ||
		got.LastEvent != "Download" || got.LastEventTitle != "Show S02E04" || got.LastEventAt == "" || got.Version != "4.0.1" {
		t.Fatalf("connection = %+v", got)
	}
	if err := s.SetArrOptions(ctx, "nope", 1, "", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("options for a missing connection: %v", err)
	}
}
