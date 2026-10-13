package store

import (
	"context"
	"slices"
	"testing"
)

func TestManageItems(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateLibrary(ctx, "Other", MediaMovies, []string{"/o"})
	if err != nil {
		t.Fatal(err)
	}
	add := func(libID, title, path string, tmdb int) Item {
		it, err := st.UpsertItem(ctx, Item{
			LibraryID: libID, Kind: KindMovie, Title: title, Path: path, TmdbID: tmdb,
		})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	alien := add(lib.ID, "Alien", "/m/Alien.mkv", 1)
	add(lib.ID, "Aliens", "/m/Aliens.mkv", 2)
	add(lib.ID, "Brazil", "/m/Brazil.mkv", 0)
	add(lib.ID, "Casablanca", "/m/Casablanca.mkv", 3)
	add(lib.ID, "Dune", "/m/Dune.mkv", 0)
	add(lib.ID, "Thing", "/m/the_thing.mkv", 0)
	add(lib.ID, "Xenomorph", "/m/alien-cut/Xenomorph.mkv", 0)
	// Neither another library's items nor an alternative version belong in
	// the list or its counts.
	add(other.ID, "Alien", "/o/Alien.mkv", 1)
	v := add(lib.ID, "Alien", "/m/Alien-4k.mkv", 1)
	if _, err := st.db.ExecContext(ctx,
		`UPDATE items SET version_of = ? WHERE id = ?`, alien.ID, v.ID); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		filter        ManageFilter
		offset, limit int
		want          []string
		total         int
	}{
		{"first page", ManageFilter{}, 0, 3, []string{"Alien", "Aliens", "Brazil"}, 7},
		{"second page", ManageFilter{}, 3, 3, []string{"Casablanca", "Dune", "Thing"}, 7},
		{"last page is short", ManageFilter{}, 6, 3, []string{"Xenomorph"}, 7},
		{"past the end", ManageFilter{}, 9, 3, []string{}, 7},
		{"title or path, any case", ManageFilter{Query: "ALIEN"}, 0, 10, []string{"Alien", "Aliens", "Xenomorph"}, 3},
		{"query is trimmed", ManageFilter{Query: "  dune "}, 0, 10, []string{"Dune"}, 1},
		{"underscore is literal", ManageFilter{Query: "_"}, 0, 10, []string{"Thing"}, 1},
		{"percent is literal", ManageFilter{Query: "%"}, 0, 10, []string{}, 0},
		{"only unmatched", ManageFilter{Unmatched: true}, 0, 10, []string{"Brazil", "Dune", "Thing", "Xenomorph"}, 4},
		{"unmatched and query", ManageFilter{Query: "alien", Unmatched: true}, 0, 10, []string{"Xenomorph"}, 1},
		{"filtered and paged", ManageFilter{Unmatched: true}, 2, 2, []string{"Thing", "Xenomorph"}, 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := st.ManageItems(ctx, lib.ID, tc.filter, tc.offset, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, it := range page.Items {
				got = append(got, it.Title)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("items = %v, want %v", got, tc.want)
			}
			if page.Total != tc.total {
				t.Errorf("total = %d, want %d", page.Total, tc.total)
			}
			// The library-wide counts ignore the filter.
			if page.All != 7 || page.Matched != 3 {
				t.Errorf("all/matched = %d/%d, want 7/3", page.All, page.Matched)
			}

			// A filtered refresh covers exactly what the filter matches.
			ids, err := st.ManageItemIDs(ctx, lib.ID, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(ids) != tc.total {
				t.Errorf("ids = %d, want %d", len(ids), tc.total)
			}
		})
	}
}
