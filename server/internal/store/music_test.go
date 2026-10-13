package store

import (
	"context"
	"testing"
)

// musicFixture is a small music library: two artists, three albums (one a
// compilation by Various Artists with a track by Alpha on it), and a user.
type musicFixture struct {
	s                   *Store
	lib                 Library
	user                string
	alpha, beta         Item
	first, second, comp Item
	a1, a2, b1, c1, c2  Item // tracks: a1, a2 on first; b1 on second; c1 (Alpha), c2 on comp
}

func newMusicFixture(t *testing.T) musicFixture {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "Music", MediaMusic, []string{"/music"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser(ctx, "listener", "Listener", "hash", RoleUser)
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
	f := musicFixture{s: s, lib: lib, user: u.ID}
	f.alpha = save(Item{Kind: KindArtist, Title: "Alpha", Artist: "Alpha", Path: "artist://alpha"})
	f.beta = save(Item{Kind: KindArtist, Title: "Beta", Artist: "Beta", Path: "artist://beta"})
	various := save(Item{Kind: KindArtist, Title: "Various Artists", Artist: "Various Artists", Path: "artist://various"})
	f.first = save(Item{Kind: KindAlbum, ParentID: f.alpha.ID, Title: "First", Artist: "Alpha", Year: 1990, Path: "/music/Alpha/First", Poster: "/artwork/first.jpg"})
	f.second = save(Item{Kind: KindAlbum, ParentID: f.beta.ID, Title: "Second", Artist: "Beta", Year: 2001, Path: "/music/Beta/Second"})
	f.comp = save(Item{Kind: KindAlbum, ParentID: various.ID, Title: "Hits", Artist: "Various Artists", Year: 1995, Path: "/music/Various/Hits"})
	f.a1 = save(Item{Kind: KindTrack, ParentID: f.first.ID, Title: "One", Artist: "Alpha", TrackNum: 1, Year: 1990, Path: "/music/Alpha/First/01.flac", RuntimeSecs: 200, Container: "flac", Poster: "/artwork/first.jpg"})
	f.a2 = save(Item{Kind: KindTrack, ParentID: f.first.ID, Title: "Two", Artist: "Alpha", TrackNum: 2, Year: 1990, Path: "/music/Alpha/First/02.flac", RuntimeSecs: 180, Container: "flac", Poster: "/artwork/first.jpg"})
	f.b1 = save(Item{Kind: KindTrack, ParentID: f.second.ID, Title: "Three", Artist: "Beta", TrackNum: 1, Year: 2001, Path: "/music/Beta/Second/01.mp3", RuntimeSecs: 240, Container: "mp3"})
	f.c1 = save(Item{Kind: KindTrack, ParentID: f.comp.ID, Title: "Duet", Artist: "Alpha feat. Gamma", TrackNum: 1, Year: 1995, Path: "/music/Various/Hits/01.mp3", RuntimeSecs: 210, Container: "mp3"})
	f.c2 = save(Item{Kind: KindTrack, ParentID: f.comp.ID, Title: "Other", Artist: "Delta", TrackNum: 2, Year: 1995, Path: "/music/Various/Hits/02.mp3", RuntimeSecs: 190, Container: "mp3"})
	if err := s.ReplaceGenres(ctx, f.b1.ID, []string{"Jazz"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceGenres(ctx, f.a1.ID, []string{"Rock"}); err != nil {
		t.Fatal(err)
	}
	return f
}

// play records a finished playback that counts, as the player's end does.
func (f musicFixture) play(t *testing.T, user string, it Item, times int) {
	t.Helper()
	ctx := context.Background()
	for range times {
		id, err := f.s.RecordPlay(ctx, user, it.ID, it.Kind)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.s.FinishPlay(ctx, id, it.RuntimeSecs, true, true); err != nil {
			t.Fatal(err)
		}
	}
}

func sameTitles(got []Item, want ...string) bool {
	g := titles(got)
	if len(g) != len(want) {
		return false
	}
	for i := range g {
		if g[i] != want[i] {
			return false
		}
	}
	return true
}

// A finished play counts once, however many times its end is reported, and
// one that does not count leaves the count alone.
func TestPlayCounts(t *testing.T) {
	f := newMusicFixture(t)
	ctx := context.Background()
	id, err := f.s.RecordPlay(ctx, f.user, f.a1.ID, KindTrack)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.FinishPlay(ctx, id, 200, true, true); err != nil {
		t.Fatal(err)
	}
	// A reap racing the explicit stop reports the same playback again.
	if err := f.s.FinishPlay(ctx, id, 10, false, true); err != nil {
		t.Fatal(err)
	}
	skipped, err := f.s.RecordPlay(ctx, f.user, f.a1.ID, KindTrack)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.FinishPlay(ctx, skipped, 5, false, false); err != nil {
		t.Fatal(err)
	}
	counts, err := f.s.PlayCountsByLibrary(ctx, f.user, f.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := counts[f.a1.ID].Plays; got != 1 {
		t.Fatalf("plays = %d, want 1", got)
	}
	if counts[f.a1.ID].LastPlayedAt == "" {
		t.Fatal("last played not recorded")
	}
}

func TestListeningRows(t *testing.T) {
	f := newMusicFixture(t)
	ctx := context.Background()
	f.play(t, f.user, f.b1, 1)
	f.play(t, f.user, f.a1, 3)
	f.play(t, f.user, f.a2, 1)
	f.play(t, "someone-else", f.c2, 5)
	// Make the order of last plays certain: Second was played last.
	if _, err := f.s.db.ExecContext(ctx, `UPDATE play_counts SET last_played_at = '2026-01-01 00:00:00' WHERE item_id IN (?, ?)`, f.a1.ID, f.a2.ID); err != nil {
		t.Fatal(err)
	}

	recent, err := f.s.RecentlyPlayedAlbums(ctx, f.user, f.lib.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !sameTitles(recent, "Second", "First") {
		t.Fatalf("recently played = %v, want [Second First]", titles(recent))
	}
	if recent[1].PlayCount != 4 {
		t.Fatalf("First's plays = %d, want 4 (its tracks summed)", recent[1].PlayCount)
	}

	top, err := f.s.MostPlayedTracks(ctx, f.user, f.lib.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !sameTitles(top, "One", "Three", "Two") && !sameTitles(top, "One", "Two", "Three") {
		t.Fatalf("most played = %v, want One first and only this user's plays", titles(top))
	}
	albums, err := f.s.MostPlayedAlbums(ctx, f.user, f.lib.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !sameTitles(albums, "First", "Second") {
		t.Fatalf("most played albums = %v, want [First Second]", titles(albums))
	}
}

func TestArtistQueries(t *testing.T) {
	f := newMusicFixture(t)
	ctx := context.Background()
	// The household plays the duet most; this listener plays Two.
	f.play(t, "someone-else", f.c1, 4)
	f.play(t, f.user, f.a2, 1)

	top, err := f.s.ArtistTopTracks(ctx, f.user, f.alpha, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !sameTitles(top, "Two", "Duet") {
		t.Fatalf("top tracks = %v, want [Two Duet]: own plays first, then the household's, unplayed left out", titles(top))
	}
	if top[0].PlayCount != 1 || top[1].PlayCount != 0 {
		t.Fatalf("play counts = %d, %d; want the viewer's own (1, 0)", top[0].PlayCount, top[1].PlayCount)
	}

	on, err := f.s.ArtistAppearsOn(ctx, f.alpha)
	if err != nil {
		t.Fatal(err)
	}
	if !sameTitles(on, "Hits") {
		t.Fatalf("appears on = %v, want [Hits]", titles(on))
	}
	if on, err := f.s.ArtistAppearsOn(ctx, f.beta); err != nil || len(on) != 0 {
		t.Fatalf("Beta appears on %v (%v), want nothing", titles(on), err)
	}

	st, err := f.s.ArtistStats(ctx, f.user, f.alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Tracks != 2 || st.Secs != 380 || st.Plays != 1 {
		t.Fatalf("stats = %+v, want 2 tracks, 380 s, 1 play", st)
	}
}

func TestSetArtistInfo(t *testing.T) {
	f := newMusicFixture(t)
	ctx := context.Background()
	if info, err := f.s.ArtistInfoFor(ctx, f.alpha.ID); err != nil || info.LookedUpAt != 0 {
		t.Fatalf("before any lookup = %+v (%v), want zero", info, err)
	}
	if err := f.s.SetArtistInfo(ctx, f.alpha.ID, "/artwork/artist-alpha.jpg", "A band.",
		ArtistInfo{MBID: "mb-1", BioURL: "https://en.wikipedia.org/wiki/Alpha", LookedUpAt: 1000}); err != nil {
		t.Fatal(err)
	}
	// A later lookup that found nothing keeps what the first one found.
	if err := f.s.SetArtistInfo(ctx, f.alpha.ID, "", "", ArtistInfo{LookedUpAt: 2000}); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.Item(ctx, f.alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Poster != "/artwork/artist-alpha.jpg" || got.Overview != "A band." {
		t.Fatalf("artist = %q / %q, want the first lookup's image and biography", got.Poster, got.Overview)
	}
	info, err := f.s.ArtistInfoFor(ctx, f.alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.LookedUpAt != 2000 || info.MBID != "mb-1" || info.BioURL == "" {
		t.Fatalf("info = %+v, want the latest time and the first lookup's source", info)
	}
	// An admin's own biography is not replaced.
	if _, err := f.s.db.ExecContext(ctx, `UPDATE items SET overview = 'Mine.', locked_fields = 'overview' WHERE id = ?`, f.beta.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetArtistInfo(ctx, f.beta.ID, "", "Theirs.", ArtistInfo{LookedUpAt: 1}); err != nil {
		t.Fatal(err)
	}
	if it, _ := f.s.Item(ctx, f.beta.ID); it.Overview != "Mine." {
		t.Fatalf("locked biography replaced with %q", it.Overview)
	}
}

// A changed cover reaches the album and the copy each of its tracks carry,
// and the fingerprint is recorded; a locked poster stays.
func TestSetAlbumArtwork(t *testing.T) {
	f := newMusicFixture(t)
	ctx := context.Background()
	if err := f.s.SetAlbumArtwork(ctx, f.first.ID, "/artwork/album-new.jpg?v=2", 42); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{f.first.ID, f.a1.ID, f.a2.ID} {
		it, err := f.s.Item(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if it.Poster != "/artwork/album-new.jpg?v=2" {
			t.Fatalf("%s poster = %q, want the new cover", it.Title, it.Poster)
		}
	}
	stamps, err := f.s.FileStamps(ctx, f.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := stamps[f.first.Path].SidecarStamp; got != 42 {
		t.Fatalf("album stamp = %d, want 42", got)
	}

	if _, err := f.s.db.ExecContext(ctx, `UPDATE items SET locked_fields = 'poster' WHERE id = ?`, f.second.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetAlbumArtwork(ctx, f.second.ID, "/artwork/other.jpg", 7); err != nil {
		t.Fatal(err)
	}
	if it, _ := f.s.Item(ctx, f.second.ID); it.Poster != "" {
		t.Fatalf("locked poster replaced with %q", it.Poster)
	}
}
