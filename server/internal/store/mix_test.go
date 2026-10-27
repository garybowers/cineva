package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

// A small library for the mix queries: two artists with ids, one found
// by name only, a hidden library, plays and ratings.
type mixFixture struct {
	lib, hidden   Library
	user, other   User
	abba, queen   Item
	beatles, kids Item
	tracks        map[string]Item
}

func newMixFixture(t *testing.T, st *Store) mixFixture {
	t.Helper()
	ctx := context.Background()
	f := mixFixture{tracks: map[string]Item{}}
	var err error
	if f.lib, err = st.CreateLibrary(ctx, "Music", MediaMusic, []string{"/m"}); err != nil {
		t.Fatal(err)
	}
	if f.hidden, err = st.CreateLibrary(ctx, "Kids", MediaMusic, []string{"/k"}); err != nil {
		t.Fatal(err)
	}
	if f.user, err = st.CreateUser(ctx, "gary", "", "h", RoleUser); err != nil {
		t.Fatal(err)
	}
	if f.other, err = st.CreateUser(ctx, "rob", "", "h", RoleUser); err != nil {
		t.Fatal(err)
	}
	artist := func(lib Library, name, mbid string) Item {
		it, err := st.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindArtist, Title: name, Artist: name, MBID: mbid, Path: "artist://" + name})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	f.abba = artist(f.lib, "ABBA", "mb-abba")
	f.queen = artist(f.lib, "Queen", "mb-queen")
	f.beatles = artist(f.lib, "The Beatles", "")
	f.kids = artist(f.hidden, "Kids Choir", "mb-kids")
	album := func(parent Item, title string, year int, genres []string, tracks ...string) Item {
		t.Helper()
		var saves []TrackSave
		for i, tt := range tracks {
			saves = append(saves, TrackSave{Item: Item{LibraryID: parent.LibraryID, Kind: KindTrack, Title: tt, Artist: parent.Title,
				TrackNum: i + 1, Year: year, RuntimeSecs: 200, Path: "/x/" + parent.Title + "/" + title + "/" + tt}, Genres: genres})
		}
		al, saved, err := st.SaveAlbum(ctx, Item{LibraryID: parent.LibraryID, Kind: KindAlbum, ParentID: parent.ID, Title: title,
			Artist: parent.Title, Year: year, Path: "/x/" + parent.Title + "/" + title}, saves)
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range saved {
			f.tracks[tr.Title] = tr
		}
		return al
	}
	album(f.abba, "Arrival", 1976, []string{"Pop", "Disco"}, "Dancing Queen", "Fernando", "Money, Money, Money")
	album(f.queen, "A Night at the Opera", 1975, []string{"Rock"}, "Bohemian Rhapsody", "Love of My Life")
	album(f.beatles, "Abbey Road", 1969, []string{"Rock", "Pop"}, "Come Together", "Something")
	album(f.kids, "Songs", 2010, []string{"Pop"}, "Wheels on the Bus")
	return f
}

func TestMixCaches(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	list, fetched, err := st.SimilarArtists(ctx, "mb-abba")
	if err != nil || len(list) != 0 || fetched != 0 {
		t.Fatalf("empty cache: %v %v %d", err, list, fetched)
	}
	now := time.Now().Unix()
	if err := st.SetSimilarArtists(ctx, "mb-abba", []SimilarArtist{{MBID: "mb-queen", Name: "Queen", Score: 4706}, {MBID: "mb-beatles", Name: "The Beatles", Score: 3955}}, now); err != nil {
		t.Fatal(err)
	}
	list, fetched, err = st.SimilarArtists(ctx, "mb-abba")
	if err != nil || fetched != now || len(list) != 2 || list[0].Name != "Queen" || list[1].Score != 3955 {
		t.Fatalf("cached: %v %d %+v", err, fetched, list)
	}
	// A miss is remembered as a fetch time with nothing in it.
	if err := st.SetSimilarArtists(ctx, "mb-nobody", nil, now); err != nil {
		t.Fatal(err)
	}
	list, fetched, _ = st.SimilarArtists(ctx, "mb-nobody")
	if len(list) != 0 || fetched != now {
		t.Errorf("miss: %v %d", list, fetched)
	}
	// Replacing drops the old rows.
	if err := st.SetSimilarArtists(ctx, "mb-abba", []SimilarArtist{{MBID: "mb-elton", Name: "Elton John", Score: 1}}, now+1); err != nil {
		t.Fatal(err)
	}
	list, _, _ = st.SimilarArtists(ctx, "mb-abba")
	if len(list) != 1 || list[0].MBID != "mb-elton" {
		t.Errorf("replaced: %+v", list)
	}
	if err := st.SetTopRecordings(ctx, "mb-queen", []TopRecording{{MBID: "rec-bo", Name: "Bohemian Rhapsody", Rank: 1}, {MBID: "rec-love", Name: "Love of My Life", Rank: 2}}, now); err != nil {
		t.Fatal(err)
	}
	top, fetched, err := st.TopRecordings(ctx, "mb-queen")
	if err != nil || fetched != now || len(top) != 2 || top[0].Rank != 1 || top[1].Name != "Love of My Life" {
		t.Fatalf("top: %v %d %+v", err, fetched, top)
	}
	if err := st.SetTopRecordings(ctx, "mb-x", nil, now); err != nil {
		t.Fatal(err)
	}
	if top, fetched, _ := st.TopRecordings(ctx, "mb-x"); len(top) != 0 || fetched != now {
		t.Errorf("top miss: %v %d", top, fetched)
	}
}

func TestMixArtistQueries(t *testing.T) {
	st := newTestStore(t)
	f := newMixFixture(t, st)
	ctx := context.Background()

	// By MusicBrainz id, from the files or from a lookup, within the
	// viewer's libraries.
	if err := st.SetArtistInfo(ctx, f.beatles.ID, "", "", ArtistInfo{MBID: "mb-beatles", LookedUpAt: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := st.ArtistsByMBIDs(ctx, []string{f.lib.ID}, []string{"mb-queen", "mb-beatles", "mb-kids", "mb-none"})
	if err != nil {
		t.Fatal(err)
	}
	if names := itemTitles(got); !slices.Equal(names, []string{"Queen", "The Beatles"}) && !slices.Equal(names, []string{"The Beatles", "Queen"}) {
		t.Errorf("by mbid: %v", names)
	}
	got, err = st.ArtistsByMBIDs(ctx, nil, []string{"mb-kids"})
	if err != nil || len(got) != 1 {
		t.Errorf("admin sees the hidden library's artist: %v %v", err, got)
	}
	if mbid, _ := st.ArtistMBID(ctx, f.beatles); mbid != "mb-beatles" {
		t.Errorf("artist mbid from the lookup: %q", mbid)
	}
	if mbid, _ := st.ArtistMBID(ctx, f.abba); mbid != "mb-abba" {
		t.Errorf("artist mbid from the files: %q", mbid)
	}

	// By name, insensitive to case and accents.
	got, err = st.ArtistsByNames(ctx, []string{f.lib.ID}, []string{"the beatles", "QUEEN", "Nobody"})
	if err != nil || len(got) != 2 {
		t.Errorf("by name: %v %v", err, itemTitles(got))
	}
	bjork, _ := st.UpsertItem(ctx, Item{LibraryID: f.lib.ID, Kind: KindArtist, Title: "Björk", Artist: "Björk", Path: "artist://bjork"})
	got, err = st.ArtistsByNames(ctx, nil, []string{"bjork"})
	if err != nil || len(got) != 1 || got[0].ID != bjork.ID {
		t.Errorf("accents: %v %v", err, itemTitles(got))
	}

	// The artists' tracks, with the viewer's plays and rating.
	ev, _ := st.RecordPlay(ctx, f.user.ID, f.tracks["Dancing Queen"].ID, KindTrack)
	if err := st.FinishPlay(ctx, ev, 200, true, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRating(ctx, f.user.ID, f.tracks["Fernando"].ID, 4); err != nil {
		t.Fatal(err)
	}
	tracks, err := st.ArtistTracks(ctx, f.user.ID, []string{f.abba.ID, f.queen.ID})
	if err != nil || len(tracks) != 5 {
		t.Fatalf("artist tracks: %v %v", err, itemTitles(tracks))
	}
	for _, tr := range tracks {
		switch tr.Title {
		case "Dancing Queen":
			if tr.PlayCount != 1 || tr.Album != "Arrival" {
				t.Errorf("Dancing Queen: %+v", tr)
			}
		case "Fernando":
			if tr.UserRating != 4 {
				t.Errorf("Fernando rating %d", tr.UserRating)
			}
		}
	}
	byID, err := st.TracksByIDs(ctx, f.user.ID, []string{f.tracks["Something"].ID, "nope"})
	if err != nil || len(byID) != 1 || byID[0].Title != "Something" {
		t.Errorf("by ids: %v %v", err, itemTitles(byID))
	}
}

func TestMixListeningQueries(t *testing.T) {
	st := newTestStore(t)
	f := newMixFixture(t, st)
	ctx := context.Background()

	play := func(user User, title string, counts bool) {
		t.Helper()
		ev, err := st.RecordPlay(ctx, user.ID, f.tracks[title].ID, KindTrack)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.FinishPlay(ctx, ev, 200, true, counts); err != nil {
			t.Fatal(err)
		}
	}
	play(f.user, "Dancing Queen", true)
	play(f.user, "Bohemian Rhapsody", true)
	play(f.other, "Dancing Queen", true)
	play(f.other, "Come Together", true)
	play(f.other, "Come Together", true)

	house, err := st.HouseholdPlays(ctx, []string{f.tracks["Dancing Queen"].ID, f.tracks["Come Together"].ID, f.tracks["Fernando"].ID})
	if err != nil || house[f.tracks["Dancing Queen"].ID] != 2 || house[f.tracks["Come Together"].ID] != 2 || house[f.tracks["Fernando"].ID] != 0 {
		t.Errorf("household plays: %v %v", err, house)
	}
	// Played within an hour of an ABBA track, by anyone: Queen by gary,
	// the Beatles by rob; never the ABBA track itself.
	since := time.Now().Add(-24 * time.Hour).UTC().Format(time.DateTime)
	co, err := st.CoListened(ctx, f.abba.ID, since, 50)
	if err != nil {
		t.Fatal(err)
	}
	if co[f.tracks["Bohemian Rhapsody"].ID] != 1 || co[f.tracks["Come Together"].ID] != 2 || co[f.tracks["Dancing Queen"].ID] != 0 {
		t.Errorf("co-listened: %v", co)
	}
	recent, err := st.RecentlyPlayedIDs(ctx, f.user.ID, since)
	if err != nil || !recent[f.tracks["Dancing Queen"].ID] || recent[f.tracks["Come Together"].ID] {
		t.Errorf("recently played: %v %v", err, recent)
	}
	genres, err := st.GenresFor(ctx, []string{f.tracks["Dancing Queen"].ID, f.tracks["Come Together"].ID})
	if err != nil || !slices.Equal(genres[f.tracks["Dancing Queen"].ID], []string{"Disco", "Pop"}) || len(genres[f.tracks["Come Together"].ID]) != 2 {
		t.Errorf("genres: %v %v", err, genres)
	}
	byGenre, err := st.TracksByGenres(ctx, f.user.ID, []string{f.lib.ID}, []string{"Pop"}, 1970, 1980, 10)
	if err != nil {
		t.Fatal(err)
	}
	if names := itemTitles(byGenre); len(names) != 3 || slices.Contains(names, "Come Together") || slices.Contains(names, "Wheels on the Bus") {
		t.Errorf("by genre and era: %v", names)
	}
	byGenre, _ = st.TracksByGenres(ctx, f.user.ID, nil, []string{"Pop"}, 0, 0, 10)
	if len(byGenre) != 6 {
		t.Errorf("by genre, every library and year: %v", itemTitles(byGenre))
	}
}

func TestPlaylistLastAddedAt(t *testing.T) {
	st := newTestStore(t)
	f := newMixFixture(t, st)
	ctx := context.Background()
	p, err := st.CreatePlaylist(ctx, f.user.ID, "Gym", nil)
	if err != nil {
		t.Fatal(err)
	}
	last := func() string {
		var v *string
		if err := st.db.QueryRowContext(ctx, `SELECT last_added_at FROM playlists WHERE id = ?`, p.ID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v == nil {
			return ""
		}
		return *v
	}
	if last() != "" {
		t.Fatal("set before any add")
	}
	if err := st.writePlaylistTracks(ctx, f.user.ID, p.ID, []string{f.tracks["Dancing Queen"].ID}, true); err != nil {
		t.Fatal(err)
	}
	if last() != "" {
		t.Error("replacing the list is not an add")
	}
	if err := st.writePlaylistTracks(ctx, f.user.ID, p.ID, []string{f.tracks["Fernando"].ID}, false); err != nil {
		t.Fatal(err)
	}
	if last() == "" {
		t.Error("an append sets last_added_at")
	}
}
