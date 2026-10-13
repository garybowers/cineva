package store

import (
	"context"
	"errors"
	"testing"
)

func TestManualPlaylist(t *testing.T) {
	f := newMusicFixture(t)
	ctx := context.Background()
	p, err := f.s.CreatePlaylist(ctx, f.user, "  Road trip ", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Road trip" {
		t.Fatalf("name = %q, want it trimmed", p.Name)
	}
	// An album expands to its tracks in order; an artist to their albums.
	tracks, err := f.s.ExpandTracks(ctx, []string{f.b1.ID, f.first.ID, "gone"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameTitles(tracks, "Three", "One", "Two") {
		t.Fatalf("expanded = %v, want [Three One Two]", titles(tracks))
	}
	trackIDs := make([]string, len(tracks))
	for i, tr := range tracks {
		trackIDs[i] = tr.ID
	}
	// The album id itself is not a track and is dropped, not stored.
	if err := f.s.AppendPlaylistTracks(ctx, f.user, p.ID, append(trackIDs, f.first.ID)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.AppendPlaylistTracks(ctx, f.user, p.ID, []string{f.b1.ID}); err != nil {
		t.Fatal(err)
	}
	got, list, err := f.s.PlaylistWithTracks(ctx, f.user, p.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sameTitles(list, "Three", "One", "Two", "Three") {
		t.Fatalf("tracks = %v, want [Three One Two Three] — a track may repeat", titles(list))
	}
	if got.TrackCount != 4 || got.DurationSecs != 240+200+180+240 {
		t.Fatalf("summary = %d tracks, %d s", got.TrackCount, got.DurationSecs)
	}
	if len(got.Covers) != 1 || got.Covers[0] != "/artwork/first.jpg" {
		t.Fatalf("covers = %v, want First's cover once", got.Covers)
	}

	// Reorder and remove in one replace.
	if err := f.s.SetPlaylistTracks(ctx, f.user, p.ID, []string{f.a2.ID, f.a1.ID}); err != nil {
		t.Fatal(err)
	}
	if _, list, _ = f.s.PlaylistWithTracks(ctx, f.user, p.ID, nil); !sameTitles(list, "Two", "One") {
		t.Fatalf("after reorder = %v, want [Two One]", titles(list))
	}
	// Tracks outside the libraries the reader may see are skipped.
	if _, list, _ = f.s.PlaylistWithTracks(ctx, f.user, p.ID, []string{}); len(list) != 0 {
		t.Fatalf("with no libraries visible = %v, want nothing", titles(list))
	}

	// Another user can neither read nor change it.
	if _, _, err := f.s.PlaylistWithTracks(ctx, "intruder", p.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user's read: %v, want ErrNotFound", err)
	}
	if err := f.s.DeletePlaylist(ctx, "intruder", p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user's delete: %v, want ErrNotFound", err)
	}

	name := "Long drive"
	if err := f.s.UpdatePlaylist(ctx, f.user, p.ID, &name, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.s.UpdatePlaylist(ctx, f.user, p.ID, nil, &SmartRules{}); !errors.Is(err, ErrInvalidPlaylist) {
		t.Fatalf("giving a manual playlist rules: %v, want ErrInvalidPlaylist", err)
	}
	all, err := f.s.Playlists(ctx, f.user, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "Long drive" || all[0].TrackCount != 2 {
		t.Fatalf("playlists = %+v", all)
	}

	// Deleting a track from the library takes it out of the playlist.
	if _, err := f.s.db.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, f.a2.ID); err != nil {
		t.Fatal(err)
	}
	if _, list, _ = f.s.PlaylistWithTracks(ctx, f.user, p.ID, nil); !sameTitles(list, "One") {
		t.Fatalf("after deleting a track = %v, want [One]", titles(list))
	}
	if err := f.s.DeletePlaylist(ctx, f.user, p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSmartRules(t *testing.T) {
	f := newMusicFixture(t)
	ctx := context.Background()
	f.play(t, f.user, f.a1, 3)
	f.play(t, f.user, f.c2, 1)
	if _, err := f.s.db.ExecContext(ctx, `UPDATE play_counts SET last_played_at = '2020-01-01 00:00:00' WHERE item_id = ?`, f.c2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.ExecContext(ctx, `UPDATE items SET added_at = '2020-01-01 00:00:00' WHERE kind = 'track' AND id <> ?`, f.b1.ID); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		rules SmartRules
		want  []string
	}{
		{"artist is, by album artist or track artist", SmartRules{Rules: []SmartRule{{"artist", "is", "alpha"}}, Sort: "title"}, []string{"One", "Two"}},
		{"artist contains", SmartRules{Rules: []SmartRule{{"artist", "contains", "alph"}}, Sort: "title"}, []string{"Duet", "One", "Two"}},
		{"artist is not", SmartRules{Rules: []SmartRule{{"artist", "isNot", "Alpha"}}, Sort: "title"}, []string{"Duet", "Other", "Three"}},
		{"album contains", SmartRules{Rules: []SmartRule{{"album", "contains", "hit"}}, Sort: "title"}, []string{"Duet", "Other"}},
		{"genre is", SmartRules{Rules: []SmartRule{{"genre", "is", "jazz"}}}, []string{"Three"}},
		{"genre is not", SmartRules{Rules: []SmartRule{{"genre", "isNot", "Jazz"}}, Sort: "title"}, []string{"Duet", "One", "Other", "Two"}},
		{"year before", SmartRules{Rules: []SmartRule{{"year", "lt", "1995"}}, Sort: "title"}, []string{"One", "Two"}},
		{"format", SmartRules{Rules: []SmartRule{{"format", "is", "FLAC"}}, Sort: "title"}, []string{"One", "Two"}},
		{"added in the last 30 days", SmartRules{Rules: []SmartRule{{"added", "inLast", "30"}}}, []string{"Three"}},
		{"played more than twice", SmartRules{Rules: []SmartRule{{"plays", "gt", "2"}}}, []string{"One"}},
		{"never played", SmartRules{Rules: []SmartRule{{"plays", "is", "0"}}, Sort: "title"}, []string{"Duet", "Three", "Two"}},
		{"not played in 30 days, never played included", SmartRules{Rules: []SmartRule{{"lastPlayed", "notInLast", "30"}}, Sort: "title"}, []string{"Duet", "Other", "Three", "Two"}},
		{"played in the last week", SmartRules{Rules: []SmartRule{{"lastPlayed", "inLast", "7"}}}, []string{"One"}},
		{"any of", SmartRules{Match: "any", Rules: []SmartRule{{"genre", "is", "Jazz"}, {"plays", "gt", "2"}}, Sort: "title"}, []string{"One", "Three"}},
		{"all of", SmartRules{Rules: []SmartRule{{"artist", "is", "Alpha"}, {"plays", "gt", "0"}}}, []string{"One"}},
		{"most played, limited", SmartRules{Sort: "plays", Limit: 2}, []string{"One", "Other"}},
		{"by album then track", SmartRules{Rules: []SmartRule{{"album", "is", "First"}}, Sort: "album"}, []string{"One", "Two"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.s.EvaluateSmart(ctx, f.user, tc.rules, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !sameTitles(got, tc.want...) {
				t.Fatalf("got %v, want %v", titles(got), tc.want)
			}
		})
	}

	// A smart playlist stores its rules and is evaluated when read.
	p, err := f.s.CreatePlaylist(ctx, f.user, "Heavy rotation", &SmartRules{Rules: []SmartRule{{"plays", "gt", "0"}}, Sort: "plays"})
	if err != nil {
		t.Fatal(err)
	}
	got, list, err := f.s.PlaylistWithTracks(ctx, f.user, p.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Smart == nil || got.Smart.Limit != SmartDefaultLimit || got.Smart.Match != "all" {
		t.Fatalf("stored rules = %+v, want defaults filled", got.Smart)
	}
	if !sameTitles(list, "One", "Other") {
		t.Fatalf("smart tracks = %v", titles(list))
	}
	if err := f.s.AppendPlaylistTracks(ctx, f.user, p.ID, []string{f.b1.ID}); !errors.Is(err, ErrInvalidPlaylist) {
		t.Fatalf("adding to a smart playlist: %v, want ErrInvalidPlaylist", err)
	}
}

func TestSmartRulesInvalid(t *testing.T) {
	tests := []struct {
		name  string
		rules SmartRules
	}{
		{"unknown field", SmartRules{Rules: []SmartRule{{"mood", "is", "happy"}}}},
		{"operator the field lacks", SmartRules{Rules: []SmartRule{{"title", "is", "x"}}}},
		{"number field with text", SmartRules{Rules: []SmartRule{{"plays", "gt", "lots"}}}},
		{"negative number", SmartRules{Rules: []SmartRule{{"added", "inLast", "-3"}}}},
		{"empty text", SmartRules{Rules: []SmartRule{{"artist", "is", "  "}}}},
		{"unknown match", SmartRules{Match: "some"}},
		{"unknown sort", SmartRules{Sort: "loudest"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.rules.Normalise(); !errors.Is(err, ErrInvalidPlaylist) {
				t.Fatalf("Normalise = %v, want ErrInvalidPlaylist", err)
			}
		})
	}
	r := SmartRules{Limit: 50000}
	if err := r.Normalise(); err != nil || r.Limit != SmartMaxLimit {
		t.Fatalf("limit = %d (%v), want capped at %d", r.Limit, err, SmartMaxLimit)
	}
}
