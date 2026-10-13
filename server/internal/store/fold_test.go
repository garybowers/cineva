package store

import (
	"context"
	"slices"
	"testing"
)

// Two spellings that differ only by case match each other under LIKE once
// both are folded, in any script; ones that differ otherwise do not.
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
		{"σ", "ς", true}, // final sigma folds with the others
		{"Ångström", "ångström", true},
		{"ПРИВЕТ", "привет", true},
		{"kelvin", "\u212aELVIN", true}, // the Kelvin sign is a K
		{"plain ascii", "PLAIN ASCII", true},
		{"Amelie", "Amélie", false}, // case, not accents
		{"Ωmega", "ωMEGa", true},
		{"Straße", "STRASSE", false}, // simple folding: ß is one letter, not two
	}
	for _, tc := range tests {
		var same bool
		if err := st.db.QueryRow(`SELECT unifold(?) LIKE ?`, tc.a, Fold(tc.b)).Scan(&same); err != nil {
			t.Fatal(err)
		}
		if same != tc.same {
			t.Errorf("%q LIKE %q after folding is %v, want %v (%q, %q)", tc.a, tc.b, same, tc.same, Fold(tc.a), Fold(tc.b))
		}
	}
}

// The Manage filter matches regardless of case in any script, not only in
// ASCII as SQLite's LIKE does, and still treats % and _ literally.
func TestManageItemsUnicodeCase(t *testing.T) {
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
		{"Amelie", []string{}},                    // case-insensitive, not accent-insensitive
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

// Search folds case across Unicode too — titles, artists and people — and
// keeps a title that starts with the query ahead of one that contains it.
func TestSearchItemsUnicodeCase(t *testing.T) {
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
	if err := st.ReplaceCredits(ctx, stalker.ID,
		nil, []Person{{TmdbID: 8452, Name: "Андрей Тарковский", Role: "Director"}}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		query string
		want  []string
	}{
		{"AMÉLIE", []string{"Amélie", "Le Fabuleux Destin d'Amélie Poulain"}},
		{"BJÖRK", []string{"Homogenic"}},
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
