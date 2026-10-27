package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestSortKey(t *testing.T) {
	tests := []struct{ in, want string }{
		{"The Beatles", "beatles"},
		{"the beatles", "beatles"},
		{"A Day at the Races", "day at the races"},
		{"An Awesome Wave", "awesome wave"},
		{"Björk", "bjork"},
		{"Mø", "mo"},
		{"  Blur ", "blur"},
		{"Theatre of Hate", "theatre of hate"}, // "the" only as a word
		{"1989", "1989"},
	}
	for _, tc := range tests {
		if got := SortKey(tc.in); got != tc.want {
			t.Errorf("SortKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// browseFixture is a small music library: three artists, their albums and
// tracks, with genres, plus the viewer's plays and ratings.
type browseFixture struct {
	lib                   Library
	user                  User
	arctic, blur, various Item
	am, humbug, leisure   Item
	nowThatsWhat          Item
	tracks                map[string]Item
	otherLib              Library
}

func newBrowseFixture(t *testing.T, st *Store) browseFixture {
	t.Helper()
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Music", MediaMusic, []string{"/a"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateLibrary(ctx, "More", MediaMusic, []string{"/b"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := st.CreateUser(ctx, "gary", "", "h", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	f := browseFixture{lib: lib, user: user, otherLib: other, tracks: map[string]Item{}}
	artist := func(name string) Item {
		t.Helper()
		it, err := st.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindArtist, Title: name, Artist: name, Path: "artist://" + name})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	f.arctic, f.blur, f.various = artist("Arctic Monkeys"), artist("Blur"), artist("Various Artists")
	album := func(parent Item, title string, year int, comp bool, genres []string, tracks ...string) Item {
		t.Helper()
		var saves []TrackSave
		for i, tt := range tracks {
			saves = append(saves, TrackSave{
				Item: Item{LibraryID: lib.ID, Kind: KindTrack, Title: tt, Artist: parent.Title, TrackNum: i + 1,
					Path: "/a/" + parent.Title + "/" + title + "/" + tt + ".flac", RuntimeSecs: 200, Year: year},
				Genres: genres,
			})
		}
		saved, savedTracks, err := st.SaveAlbum(ctx, Item{LibraryID: lib.ID, Kind: KindAlbum, ParentID: parent.ID,
			Title: title, Artist: parent.Title, Year: year, Compilation: comp, Path: "/a/" + parent.Title + "/" + title,
			Poster: "/artwork/" + title + ".jpg"}, saves)
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range savedTracks {
			f.tracks[tr.Title] = tr
		}
		return saved
	}
	f.am = album(f.arctic, "AM", 2013, false, []string{"Rock"}, "Do I Wanna Know?", "R U Mine?", "Arabella")
	f.humbug = album(f.arctic, "Humbug", 2009, false, []string{"Rock", "Psychedelic"}, "My Propeller", "Crying Lightning")
	f.leisure = album(f.blur, "Leisure", 1991, false, []string{"Indie"}, "She's So High", "There's No Other Way")
	f.nowThatsWhat = album(f.various, "Now That's What I Call Music", 1999, true, []string{"Pop"}, "Baby One More Time")
	// A track credited to someone else on a compilation.
	tr := f.tracks["Baby One More Time"]
	tr.Artist = "Britney Spears"
	if _, err := st.UpsertItem(ctx, tr); err != nil {
		t.Fatal(err)
	}
	// Plays and a rating.
	for _, title := range []string{"Arabella", "Arabella", "Do I Wanna Know?", "She's So High"} {
		if _, err := st.db.ExecContext(ctx, `INSERT INTO play_counts (user_id, item_id, plays, last_played_at)
			VALUES (?, ?, 1, ?) ON CONFLICT (user_id, item_id) DO UPDATE SET plays = plays + 1, last_played_at = excluded.last_played_at`,
			user.ID, f.tracks[title].ID, time.Now().UTC().Format(time.DateTime)); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetRating(ctx, user.ID, f.leisure.ID, 5); err != nil {
		t.Fatal(err)
	}
	return f
}

func itemTitles(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}

func TestArtistsListing(t *testing.T) {
	st := newTestStore(t)
	f := newBrowseFixture(t, st)
	ctx := context.Background()

	page, err := st.Artists(ctx, f.user.ID, f.lib.ID, MusicQuery{Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(page.Items); !slices.Equal(got, []string{"Arctic Monkeys", "Blur", "Various Artists"}) {
		t.Errorf("by name: %v", got)
	}
	if page.Total != 3 || page.Index["A"] != 1 || page.Index["B"] != 1 || page.Index["V"] != 1 {
		t.Errorf("total %d index %v", page.Total, page.Index)
	}
	if page.Items[0].ChildCount != 2 || page.Items[0].PlayCount != 3 {
		t.Errorf("Arctic Monkeys: %d albums, %d plays", page.Items[0].ChildCount, page.Items[0].PlayCount)
	}

	// Paging is stable and the letter index ignores the letter filter.
	page, err = st.Artists(ctx, f.user.ID, f.lib.ID, MusicQuery{Page: 2, Size: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(page.Items); !slices.Equal(got, []string{"Various Artists"}) || page.Total != 3 {
		t.Errorf("page 2: %v total %d", got, page.Total)
	}
	page, err = st.Artists(ctx, f.user.ID, f.lib.ID, MusicQuery{Letter: "b", Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(page.Items); !slices.Equal(got, []string{"Blur"}) || page.Total != 1 || len(page.Index) != 3 {
		t.Errorf("letter B: %v total %d index %v", got, page.Total, page.Index)
	}

	// Words match as typed, names starting with the query first.
	page, err = st.Artists(ctx, f.user.ID, f.lib.ID, MusicQuery{Query: "arc mon", Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(page.Items); !slices.Equal(got, []string{"Arctic Monkeys"}) {
		t.Errorf("words: %v", got)
	}
	// Most played first; a genre narrows to artists with an album in it.
	page, err = st.Artists(ctx, f.user.ID, f.lib.ID, MusicQuery{Sort: "plays", Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(page.Items); got[0] != "Arctic Monkeys" || got[1] != "Blur" || page.Index != nil {
		t.Errorf("by plays: %v index %v", got, page.Index)
	}
	page, err = st.Artists(ctx, f.user.ID, f.lib.ID, MusicQuery{Genre: "Pop", Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(page.Items); !slices.Equal(got, []string{"Various Artists"}) {
		t.Errorf("genre: %v", got)
	}
	// Another library's artists stay out.
	page, err = st.Artists(ctx, f.user.ID, f.otherLib.ID, MusicQuery{Page: 1, Size: 10})
	if err != nil || page.Total != 0 {
		t.Errorf("other library: %v total %d", err, page.Total)
	}
}

func TestAlbumsListing(t *testing.T) {
	st := newTestStore(t)
	f := newBrowseFixture(t, st)
	ctx := context.Background()

	page, err := st.Albums(ctx, f.user.ID, f.lib.ID, MusicQuery{Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(page.Items); !slices.Equal(got, []string{"AM", "Humbug", "Leisure", "Now That's What I Call Music"}) {
		t.Errorf("by name: %v", got)
	}
	for _, it := range page.Items {
		switch it.Title {
		case "AM":
			if it.ChildCount != 3 || it.PlayCount != 3 {
				t.Errorf("AM: %d tracks %d plays", it.ChildCount, it.PlayCount)
			}
		case "Leisure":
			if it.UserRating != 5 {
				t.Errorf("Leisure rating %d", it.UserRating)
			}
		}
	}
	cases := []struct {
		name string
		q    MusicQuery
		want []string
	}{
		{"artist", MusicQuery{Artist: f.arctic.ID, Sort: "year"}, []string{"AM", "Humbug"}},
		{"genre", MusicQuery{Genre: "Psychedelic"}, []string{"Humbug"}},
		{"decade", MusicQuery{Decade: 1990, Sort: "year"}, []string{"Now That's What I Call Music", "Leisure"}},
		{"year", MusicQuery{Year: 2013}, []string{"AM"}},
		{"compilations", MusicQuery{Compilations: true}, []string{"Now That's What I Call Music"}},
		{"rating", MusicQuery{Sort: "rating"}, []string{"Leisure", "AM", "Humbug", "Now That's What I Call Music"}},
		{"plays", MusicQuery{Sort: "plays"}, []string{"AM", "Leisure", "Humbug", "Now That's What I Call Music"}},
		{"by artist", MusicQuery{Sort: "artist"}, []string{"Humbug", "AM", "Leisure", "Now That's What I Call Music"}},
		{"words in artist", MusicQuery{Query: "blur"}, []string{"Leisure"}},
		{"letter #", MusicQuery{Letter: "#"}, []string{}},
		{"letter n", MusicQuery{Letter: "N"}, []string{"Now That's What I Call Music"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.q.Page, tc.q.Size = 1, 10
			page, err := st.Albums(ctx, f.user.ID, f.lib.ID, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			if got := itemTitles(page.Items); !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTracksListing(t *testing.T) {
	st := newTestStore(t)
	f := newBrowseFixture(t, st)
	ctx := context.Background()

	page, err := st.Tracks(ctx, f.user.ID, f.lib.ID, MusicQuery{Sort: "album", Page: 1, Size: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 8 || len(page.Items) != 8 {
		t.Fatalf("total %d, %d items", page.Total, len(page.Items))
	}
	if got := itemTitles(page.Items[:3]); !slices.Equal(got, []string{"Do I Wanna Know?", "R U Mine?", "Arabella"}) {
		t.Errorf("by album: %v", got)
	}
	// Tracks carry their album and album artist, and the viewer's plays.
	for _, it := range page.Items {
		if it.Title == "Arabella" && (it.Album != "AM" || it.AlbumArtist != "Arctic Monkeys" || it.PlayCount != 2) {
			t.Errorf("Arabella: album %q by %q, %d plays", it.Album, it.AlbumArtist, it.PlayCount)
		}
		if it.Title == "Baby One More Time" && (it.Artist != "Britney Spears" || it.AlbumArtist != "Various Artists") {
			t.Errorf("compilation track: artist %q album artist %q", it.Artist, it.AlbumArtist)
		}
	}
	page, err = st.Tracks(ctx, f.user.ID, f.lib.ID, MusicQuery{Query: "baby", Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(page.Items); !slices.Equal(got, []string{"Baby One More Time"}) {
		t.Errorf("words: %v", got)
	}
	// A word may match the album.
	page, err = st.Tracks(ctx, f.user.ID, f.lib.ID, MusicQuery{Query: "humbug", Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Errorf("album word: %v", itemTitles(page.Items))
	}
	page, err = st.Tracks(ctx, f.user.ID, f.lib.ID, MusicQuery{Sort: "plays", Page: 1, Size: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(page.Items); !slices.Equal(got, []string{"Arabella", "Do I Wanna Know?"}) {
		t.Errorf("by plays: %v", got)
	}
	page, err = st.Tracks(ctx, f.user.ID, f.lib.ID, MusicQuery{Sort: "random", Page: 1, Size: 3})
	if err != nil || len(page.Items) != 3 || page.Total != 8 {
		t.Errorf("random: %v %d/%d", err, len(page.Items), page.Total)
	}
}

func TestTracksUnderAndSearch(t *testing.T) {
	st := newTestStore(t)
	f := newBrowseFixture(t, st)
	ctx := context.Background()

	tracks, err := st.TracksUnder(ctx, f.user.ID, f.arctic)
	if err != nil {
		t.Fatal(err)
	}
	// Oldest album first, then track order.
	if got := itemTitles(tracks); !slices.Equal(got, []string{"My Propeller", "Crying Lightning", "Do I Wanna Know?", "R U Mine?", "Arabella"}) {
		t.Errorf("artist's tracks: %v", got)
	}
	tracks, err = st.TracksUnder(ctx, f.user.ID, f.am)
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(tracks); !slices.Equal(got, []string{"Do I Wanna Know?", "R U Mine?", "Arabella"}) {
		t.Errorf("album's tracks: %v", got)
	}
	if tracks[2].PlayCount != 2 {
		t.Errorf("Arabella plays %d", tracks[2].PlayCount)
	}
	if _, err := st.TracksUnder(ctx, f.user.ID, f.tracks["Arabella"]); err == nil {
		t.Error("a track has nothing under it")
	}

	res, err := st.SearchLibrary(ctx, f.user.ID, f.lib.ID, "ar", 5, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(res.Artists); !slices.Equal(got, []string{"Arctic Monkeys", "Various Artists"}) {
		t.Errorf("artists: %v", got)
	}
	if got := itemTitles(res.Tracks); got[0] != "Arabella" {
		t.Errorf("tracks, prefix first: %v", got)
	}
}

func TestSaveAlbumKeepsTracksInStep(t *testing.T) {
	st := newTestStore(t)
	f := newBrowseFixture(t, st)
	ctx := context.Background()

	// Album genres are the union of the tracks'; the album's cover reached
	// its tracks.
	genres, err := st.GenresForItem(ctx, f.humbug.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(genres, []string{"Psychedelic", "Rock"}) {
		t.Errorf("album genres %v", genres)
	}
	if f.tracks["My Propeller"].Poster != "/artwork/Humbug.jpg" {
		t.Errorf("track poster %q", f.tracks["My Propeller"].Poster)
	}
	counts, err := st.LibraryGenres(ctx, f.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, gc := range counts {
		if gc.Genre == "Rock" && (gc.Count != 2 || gc.Tracks != 5) {
			t.Errorf("Rock: %d albums, %d tracks", gc.Count, gc.Tracks)
		}
	}
	byGenre, err := st.ItemsByGenre(ctx, f.lib.ID, "Rock")
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(byGenre); !slices.Equal(got, []string{"AM", "Humbug"}) {
		t.Errorf("by genre lists albums, not tracks: %v", got)
	}

	// Saving the album again with a corrected title and one changed track
	// renames the album on every track and keeps the others' lyrics.
	humbug := f.humbug
	humbug.Title = "Humbug (Deluxe)"
	changed := f.tracks["My Propeller"]
	changed.Title = "My Propeller (Remastered)"
	_, saved, err := st.SaveAlbum(ctx, humbug, []TrackSave{{Item: changed, Genres: []string{"Rock"},
		Lyrics: &Lyrics{Synced: true, Text: "[00:01.00] round and round"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].ID != changed.ID || saved[0].Album != "Humbug (Deluxe)" {
		t.Errorf("saved %+v", saved)
	}
	other, err := st.Item(ctx, f.tracks["Crying Lightning"].ID)
	if err != nil {
		t.Fatal(err)
	}
	if other.Album != "Humbug (Deluxe)" {
		t.Errorf("unchanged track's album %q", other.Album)
	}
	l, err := st.LyricsFor(ctx, changed.ID)
	if err != nil || l == nil || l.Text != "[00:01.00] round and round" || !l.Synced {
		t.Errorf("lyrics %v %+v", err, l)
	}
	if l, err := st.LyricsFor(ctx, other.ID); err != nil || l != nil {
		t.Errorf("no lyrics expected: %v %+v", err, l)
	}
	// The album's genres are the union over all its tracks, the unchanged
	// one included, so Psychedelic stays.
	genres, _ = st.GenresForItem(ctx, f.humbug.ID)
	if !slices.Equal(genres, []string{"Psychedelic", "Rock"}) {
		t.Errorf("album genres after %v", genres)
	}
	page, err := st.Albums(ctx, f.user.ID, f.lib.ID, MusicQuery{Letter: "h", Page: 1, Size: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].SortTitle != "humbug (deluxe)" {
		t.Errorf("sort title follows the title: %v %+v", err, page.Items)
	}
}

func TestAlbumLookup(t *testing.T) {
	st := newTestStore(t)
	f := newBrowseFixture(t, st)
	ctx := context.Background()

	// Albums missing a cover or a year want a lookup until one has run;
	// one missing only its id does not, and Various Artists never does.
	for _, set := range []struct{ id, col string }{{f.am.ID, "poster = ''"}, {f.leisure.ID, "year = 0"}, {f.nowThatsWhat.ID, "poster = ''"}} {
		if _, err := st.db.ExecContext(ctx, `UPDATE items SET `+set.col+` WHERE id = ?`, set.id); err != nil {
			t.Fatal(err)
		}
	}
	due, err := st.AlbumsForLookup(ctx, f.lib.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(due); !slices.Equal(got, []string{"Leisure", "AM"}) && !slices.Equal(got, []string{"AM", "Leisure"}) {
		t.Errorf("due: %v", got)
	}
	// A lookup fills the gaps and only the gaps, names the untitled
	// tracks it was asked about, and is not repeated.
	// Leisure's year is locked by an edit (to 1990), so the lookup's 1991
	// is ignored; its cover is kept; the id is new.
	leisure := f.leisure
	if _, err := st.UpdateItem(ctx, leisure.ID, ItemEdit{Year: ptr(1990)}); err != nil {
		t.Fatal(err)
	}
	track := f.tracks["She's So High"]
	if err := st.SetAlbumLookup(ctx, leisure.ID, "mb-leisure", 1991, "/artwork/other.jpg",
		map[string]string{track.ID: "She's So High (canonical)"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := st.Item(ctx, leisure.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MBID != "mb-leisure" || got.Year != 1990 || got.Poster != "/artwork/Leisure.jpg" {
		t.Errorf("after lookup: %+v", got)
	}
	// AM had no cover: the lookup's fills it, and its tracks'.
	if err := st.SetAlbumLookup(ctx, f.am.ID, "mb-am", 2013, "/artwork/mb-am.jpg", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if am, _ := st.Item(ctx, f.am.ID); am.Poster != "/artwork/mb-am.jpg" {
		t.Errorf("AM cover %q", am.Poster)
	}
	tr, _ := st.Item(ctx, track.ID)
	if tr.Title != "She's So High (canonical)" || tr.SortTitle != "she's so high (canonical)" {
		t.Errorf("track after lookup: %q %q", tr.Title, tr.SortTitle)
	}
	due, err = st.AlbumsForLookup(ctx, f.lib.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTitles(due); slices.Contains(got, "Leisure") {
		t.Errorf("Leisure looked up again: %v", got)
	}
	// A lookup that found nothing is recorded too, and retried only after
	// the given time.
	if _, err := st.db.ExecContext(ctx, `UPDATE items SET year = 0 WHERE id = ?`, f.humbug.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAlbumLookup(ctx, f.humbug.ID, "", 0, "", nil, time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	due, _ = st.AlbumsForLookup(ctx, f.lib.ID, time.Now().Add(-72*time.Hour))
	if got := itemTitles(due); slices.Contains(got, "Humbug") {
		t.Errorf("Humbug retried too soon: %v", got)
	}
	due, _ = st.AlbumsForLookup(ctx, f.lib.ID, time.Now())
	if got := itemTitles(due); !slices.Contains(got, "Humbug") {
		t.Errorf("Humbug not retried: %v", got)
	}
}
