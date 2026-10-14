package importer

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/store"
)

// fakeSource is a source with fixed answers.
type fakeSource struct {
	users     []User
	watches   map[string][]Watch
	playlists map[string][]Playlist
}

func (f fakeSource) Check(context.Context) (Info, error) {
	return Info{Kind: "jellyfin", Name: "Old box", Users: f.users}, nil
}
func (f fakeSource) Watches(_ context.Context, u User) ([]Watch, error) { return f.watches[u.ID], nil }
func (f fakeSource) Playlists(_ context.Context, u User) ([]Playlist, error) {
	return f.playlists[u.ID], nil
}

func wait(t *testing.T, r *Runner) Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if run, ok := r.Current(); ok && run.Status != "running" {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the run did not finish")
	return Run{}
}

func TestRunner(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	films, _ := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{"/mnt/media/Films"})
	tv, _ := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{"/mnt/media/TV"})
	music, _ := st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{"/mnt/media/Music"})
	save := func(it store.Item) store.Item {
		t.Helper()
		saved, err := st.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	paddington := save(store.Item{LibraryID: films.ID, Kind: store.KindMovie, Title: "Paddington", TmdbID: 100,
		Path: "/mnt/media/Films/Paddington (2014)/Paddington.mkv", RuntimeSecs: 5700})
	// No TMDB id: only its path can match it.
	alien := save(store.Item{LibraryID: films.ID, Kind: store.KindMovie, Title: "Alien",
		Path: "/mnt/media/Films/Alien (1979)/Alien.mkv", RuntimeSecs: 7000})
	show := save(store.Item{LibraryID: tv.ID, Kind: store.KindSeries, Title: "Slow Horses", TmdbID: 200, Path: "/mnt/media/TV/Slow Horses"})
	season := save(store.Item{LibraryID: tv.ID, Kind: store.KindSeason, ParentID: show.ID, SeasonNum: 1, Path: "/mnt/media/TV/Slow Horses/Season 1"})
	ep2 := save(store.Item{LibraryID: tv.ID, Kind: store.KindEpisode, ParentID: season.ID, SeasonNum: 1, EpisodeNum: 2,
		Title: "Episode 2", Path: "/mnt/media/TV/Slow Horses/Season 1/e2.mkv", RuntimeSecs: 3000})
	artist := save(store.Item{LibraryID: music.ID, Kind: store.KindArtist, Title: "Elbow", Artist: "Elbow", Path: "artist:elbow"})
	album := save(store.Item{LibraryID: music.ID, Kind: store.KindAlbum, ParentID: artist.ID, Title: "The Seldom Seen Kid", Artist: "Elbow",
		Path: "/mnt/media/Music/Elbow/The Seldom Seen Kid"})
	track := save(store.Item{LibraryID: music.ID, Kind: store.KindTrack, ParentID: album.ID, Title: "One Day Like This", Artist: "Elbow",
		Path: "/mnt/media/Music/Elbow/The Seldom Seen Kid/11.flac"})

	gary, _ := st.CreateUser(ctx, "gary", "Gary", "h", store.RoleAdmin)
	// Gary watched Paddington here yesterday: an older import must not undo it.
	if _, err := st.SetProgress(ctx, gary.ID, paddington.ID, 600, 5700); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2025, 6, 1, 20, 0, 0, 0, time.UTC)
	src := fakeSource{
		users: []User{{ID: "g", Name: "Gary"}, {ID: "k", Name: "Kid Two"}},
		watches: map[string][]Watch{
			"g": {
				{Movie: true, Title: "Paddington", TmdbID: 100, Watched: true, At: old},
				{Movie: true, Title: "Alien", Path: `D:\Films\Alien (1979)\Alien.mkv`, Watched: true, At: old},
				{SeriesTitle: "Slow Horses", SeriesTmdbID: 200, Season: 1, Episode: 2, PositionSecs: 900, At: old},
				{Movie: true, Title: "Not here", TmdbID: 999, Watched: true},
			},
			"k": {{Movie: true, Title: "Paddington", TmdbID: 100, Watched: true, At: old}},
		},
		playlists: map[string][]Playlist{"g": {{Name: "Road trip", Tracks: []Track{
			{Artist: "elbow", Album: "the seldom seen kid", Title: "one day like this"}, {Artist: "Nobody", Title: "Missing"}}}}},
	}
	r := &Runner{Store: st, NewSource: func(string, string, string) (Source, bool) { return src, true }}
	req := Request{Kind: "jellyfin", Users: []Mapping{
		{SourceID: "g", SourceName: "Gary", UserID: gary.ID},
		{SourceID: "k", SourceName: "Kid Two", Create: true},
	}, Options: Options{WatchState: true, Playlists: true}, DryRun: true}

	// A dry run reports and writes nothing.
	if _, err := r.Start(req); err != nil {
		t.Fatal(err)
	}
	run := wait(t, r)
	if run.Status != "done" || len(run.Users) != 2 {
		t.Fatalf("dry run = %+v", run)
	}
	g := run.Users[0]
	if g.Watched != 2 || g.InProgress != 1 || g.Unmatched != 1 || g.Written != 0 || g.Playlists != 1 || g.Tracks != 1 ||
		len(g.UnmatchedTitles) != 1 || g.UnmatchedTitles[0] != "Not here" {
		t.Fatalf("dry run for Gary = %+v", g)
	}
	if run.Users[1].Created != nil {
		t.Fatal("a dry run created a user")
	}
	if ps, _ := st.PlayStatesByLibrary(ctx, gary.ID, films.ID); ps[alien.ID].Watched {
		t.Fatal("a dry run wrote watch state")
	}

	// The import: Alien by path, the episode by series and number;
	// Paddington stays as watched here, being newer.
	req.DryRun = false
	if _, err := r.Start(req); err != nil {
		t.Fatal(err)
	}
	run = wait(t, r)
	g = run.Users[0]
	if run.Status != "done" || g.Written != 2 || g.Error != "" {
		t.Fatalf("import for Gary = %+v (run %+v)", g, run)
	}
	filmStates, _ := st.PlayStatesByLibrary(ctx, gary.ID, films.ID)
	if !filmStates[alien.ID].Watched {
		t.Error("Alien not marked watched")
	}
	if p := filmStates[paddington.ID]; p.Watched || p.PositionSecs != 600 {
		t.Errorf("Paddington = %+v, want Cineva's own newer state kept", p)
	}
	if e := (func() store.PlayState { s, _ := st.PlayStatesByLibrary(ctx, gary.ID, tv.ID); return s[ep2.ID] })(); e.PositionSecs != 900 {
		t.Errorf("episode = %+v, want 900 s in", e)
	}
	lists, _ := st.Playlists(ctx, gary.ID, nil)
	if len(lists) != 1 || lists[0].Name != "Road trip" {
		t.Fatalf("playlists = %+v", lists)
	}
	if _, tracks, err := st.PlaylistWithTracks(ctx, gary.ID, lists[0].ID, nil); err != nil || len(tracks) != 1 || tracks[0].ID != track.ID {
		t.Fatalf("playlist tracks = %+v, %v", tracks, err)
	}

	// The kid got an account with a temporary password that works.
	k := run.Users[1]
	if k.Created == nil || k.Created.Username != "kid-two" || k.Created.Password == "" {
		t.Fatalf("created = %+v", k.Created)
	}
	kid, err := st.UserByUsername(ctx, "kid-two")
	if err != nil || kid.DisplayName != "Kid Two" {
		t.Fatalf("kid = %+v, %v", kid, err)
	}
	if s, _ := st.PlayStatesByLibrary(ctx, kid.ID, films.ID); !s[paddington.ID].Watched {
		t.Error("the new user's watch state was not imported")
	}

	// Again: nothing new to write, and the playlist is not duplicated.
	req.Users = req.Users[:1]
	if _, err := r.Start(req); err != nil {
		t.Fatal(err)
	}
	if run = wait(t, r); run.Users[0].Written != 0 {
		t.Fatalf("second import wrote %d states", run.Users[0].Written)
	}
	if lists, _ := st.Playlists(ctx, gary.ID, nil); len(lists) != 2 || lists[1].Name != "Road trip (from Jellyfin)" {
		t.Fatalf("second import's playlists = %+v", lists)
	}
}

func TestPathTail(t *testing.T) {
	for _, tc := range [][2]string{
		{"/mnt/media/TV/Show/Season 1/E1.mkv", "season 1/e1.mkv"},
		{`D:\TV\Show\Season 1\E1.mkv`, "season 1/e1.mkv"},
		{"/data/tv/show/season 1/e1.mkv", "season 1/e1.mkv"},
	} {
		if got := pathTail(tc[0]); got != tc[1] {
			t.Errorf("pathTail(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}
