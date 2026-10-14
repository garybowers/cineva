package store

import (
	"context"
	"slices"
	"testing"
)

// Two spellings that differ only by case or accents match each other under
// LIKE once both are folded, in any script and whichever side holds the
// accents; ones that differ otherwise do not. Checked through foldedCol, as
// the queries use it, so the ASCII shortcut is exercised too.
func TestFold(t *testing.T) {
	st := newTestStore(t)
	tests := []struct {
		a, b string
		same bool
	}{
		{"Amélie", "AMÉLIE", true},
		{"amélie", "AMÉLIE", true},
		{"Amélie", "Ame\u0301lie", true}, // decomposed accent, as a Mac writes it
		{"Σταθης", "ΣΤΑΘΗΣ", true},
		{"ὀδυσσεύς", "ὈΔΥΣΣΕΎΣ", true},
		{"ὀδυσσεύς", "οδυσσευς", true}, // Greek breathings and accents too
		{"σ", "ς", true},               // final sigma folds with the others
		{"Ångström", "ångström", true},
		{"Ångström", "Angstrom", true},
		{"ПРИВЕТ", "привет", true},
		{"Ёлка", "елка", true},
		{"kelvin", "\u212aELVIN", true}, // the Kelvin sign is a K
		{"plain ascii", "PLAIN ASCII", true},
		{"Amelie", "Amélie", true},
		{"amélie", "Amelie", true},
		{"Bjork", "Björk", true},
		{"Motorhead", "Motörhead", true},
		{"Sigur Ros", "Sigur Rós", true},
		{"Pokemon", "Pokémon", true},
		{"Tiesto", "Tiësto", true},
		{"Viet Nam", "Việt Nam", true}, // stacked marks
		{"Straße", "STRASSE", true},    // ß is spelled out
		{"strasse", "Straße", true},
		{"Søren", "Soren", true},
		{"Ærø", "aero", true},
		{"Œuvre", "oeuvre", true},
		{"Łódź", "Lodz", true},
		{"Đorđe", "Dorde", true},
		{"Kırık", "Kirik", true}, // Turkish dotless i
		{"İstanbul", "istanbul", true},
		{"\ufb01lm", "film", true}, // the fi ligature
		{"ＡＢＣ", "abc", true},       // full-width letters
		{"Amelie", "Amalie", false},
		{"Bjork", "Bjorn", false},
		{"が", "か", false}, // a kana voicing mark is part of the letter
	}
	for _, tc := range tests {
		for _, pair := range [][2]string{{tc.a, tc.b}, {tc.b, tc.a}} {
			col, query := pair[0], pair[1]
			var same bool
			if err := st.db.QueryRow(`SELECT `+foldedCol("?1")+` LIKE ?2`, col, Fold(query)).Scan(&same); err != nil {
				t.Fatal(err)
			}
			if same != tc.same {
				t.Errorf("%q LIKE %q after folding is %v, want %v (%q, %q)", col, query, same, tc.same, Fold(col), Fold(query))
			}
		}
	}
}

// The Manage filter matches regardless of case and accents in any script,
// not only ASCII case as SQLite's LIKE does, and still treats % and _
// literally.
func TestManageItemsUnicodeFold(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []Item{
		{Title: "Amélie", Path: "/m/Amélie (2001)/Amélie.mkv"},
		{Title: "Ο Θίασος", Path: "/m/o-thiasos.mkv"},
		{Title: "Ivan's Childhood", Path: "/m/Иваново детство/film.mkv"},
		{Title: "Brazil", Path: "/m/brazil_100%.mkv"},
		{Title: "Motorhead Live", Path: "/m/motorhead.mkv"},
	} {
		it.LibraryID, it.Kind = lib.ID, KindMovie
		if _, err := st.UpsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		query string
		want  []string
	}{
		{"AMÉLIE", []string{"Amélie"}},
		{"amélie", []string{"Amélie"}},
		{"ΘΊΑΣΟΣ", []string{"Ο Θίασος"}},
		{"ИВАНОВО", []string{"Ivan's Childhood"}}, // matched on the path
		{"Amelie", []string{"Amélie"}},            // an ASCII query, an accented title
		{"amelie (2001)", []string{"Amélie"}},     // and path
		{"MOTÖRHEAD", []string{"Motorhead Live"}}, // an accented query, an ASCII title
		{"Amalie", []string{}},
		{"0%", []string{"Brazil"}},
		{"l_1", []string{"Brazil"}},
		{"%", []string{"Brazil"}},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			page, err := st.ManageItems(ctx, lib.ID, ManageFilter{Query: tc.query}, 0, 10)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, it := range page.Items {
				got = append(got, it.Title)
			}
			if !slices.Equal(got, tc.want) || page.Total != len(tc.want) {
				t.Errorf("got %v (total %d), want %v", got, page.Total, tc.want)
			}
			ids, err := st.ManageItemIDs(ctx, lib.ID, ManageFilter{Query: tc.query})
			if err != nil {
				t.Fatal(err)
			}
			if len(ids) != len(tc.want) {
				t.Errorf("ManageItemIDs found %d, want %d", len(ids), len(tc.want))
			}
		})
	}
}

// Search folds case and accents across Unicode too — titles, artists and
// people — and keeps a title that starts with the query ahead of one that
// contains it.
func TestSearchItemsUnicodeFold(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	films, err := st.CreateLibrary(ctx, "Films", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	music, err := st.CreateLibrary(ctx, "Music", MediaMusic, []string{"/a"})
	if err != nil {
		t.Fatal(err)
	}
	add := func(it Item) Item {
		t.Helper()
		saved, err := st.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	add(Item{LibraryID: films.ID, Kind: KindMovie, Title: "Amélie", Path: "/m/amelie.mkv"})
	add(Item{LibraryID: films.ID, Kind: KindMovie, Title: "Le Fabuleux Destin d'Amélie Poulain", Path: "/m/destin.mkv"})
	stalker := add(Item{LibraryID: films.ID, Kind: KindMovie, Title: "Stalker", Path: "/m/stalker.mkv"})
	add(Item{LibraryID: music.ID, Kind: KindAlbum, Title: "Homogenic", Artist: "Björk", Path: "/a/Björk/Homogenic"})
	add(Item{LibraryID: films.ID, Kind: KindMovie, Title: "100% Wolf", Path: "/m/wolf.mkv"})
	add(Item{LibraryID: films.ID, Kind: KindMovie, Title: "1000 Ways", Path: "/m/ways.mkv"})
	add(Item{LibraryID: films.ID, Kind: KindMovie, Title: "Amelie and the Ghost", Path: "/m/ghost.mkv"})
	add(Item{LibraryID: music.ID, Kind: KindAlbum, Title: "Ace of Spades", Artist: "Motorhead", Path: "/a/Motorhead/Ace of Spades"})
	if err := st.ReplaceCredits(ctx, stalker.ID,
		nil, []Person{{TmdbID: 8452, Name: "Андрей Тарковский", Role: "Director"}}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		query string
		want  []string
	}{
		{"AMÉLIE", []string{"Amelie and the Ghost", "Amélie", "Le Fabuleux Destin d'Amélie Poulain"}},
		{"amelie", []string{"Amelie and the Ghost", "Amélie", "Le Fabuleux Destin d'Amélie Poulain"}},
		{"BJÖRK", []string{"Homogenic"}},
		{"bjork", []string{"Homogenic"}},
		{"Motörhead", []string{"Ace of Spades"}},
		{"tarkovsky", []string{}}, // folding is not transliteration
		{"ТАРКОВСКИЙ", []string{"Stalker"}},
		{"100%", []string{"100% Wolf"}},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			items, err := st.SearchItems(ctx, tc.query, 20)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, it := range items {
				got = append(got, it.Title)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
