package store

import (
	"context"
	"errors"
	"testing"
)

func TestRatings(t *testing.T) {
	f := newMusicFixture(t)
	ctx := context.Background()
	for _, r := range []struct {
		item  string
		stars int
	}{{f.a1.ID, 5}, {f.a2.ID, 2}, {f.b1.ID, 4}, {f.b1.ID, 3}} { // the last replaces the one before
		if err := f.s.SetRating(ctx, f.user, r.item, r.stars); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []int{-1, 6} {
		if err := f.s.SetRating(ctx, f.user, f.a1.ID, bad); !errors.Is(err, ErrInvalidRating) {
			t.Fatalf("%d stars: %v, want ErrInvalidRating", bad, err)
		}
	}
	got, err := f.s.RatingsByLibrary(ctx, f.user, f.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[f.a1.ID] != 5 || got[f.a2.ID] != 2 || got[f.b1.ID] != 3 {
		t.Fatalf("by library = %v", got)
	}
	// Ratings are the viewer's own.
	if other, _ := f.s.RatingsFor(ctx, "someone-else", []string{f.a1.ID}); len(other) != 0 {
		t.Fatalf("another user's ratings = %v, want none", other)
	}

	tests := []struct {
		name  string
		rule  SmartRule
		want  []string
		valid bool
	}{
		{"at least", SmartRule{Field: "rating", Op: "atLeast", Value: "3"}, []string{"One", "Three"}, true},
		{"at most leaves out the unrated", SmartRule{Field: "rating", Op: "atMost", Value: "2"}, []string{"Two"}, true},
		{"is 0 is not rated", SmartRule{Field: "rating", Op: "is", Value: "0"}, []string{"Duet", "Other"}, true},
		{"over five", SmartRule{Field: "rating", Op: "atLeast", Value: "6"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list, err := f.s.EvaluateSmart(ctx, f.user, SmartRules{Rules: []SmartRule{tt.rule}, Sort: "title"}, nil)
			if !tt.valid {
				if !errors.Is(err, ErrInvalidPlaylist) {
					t.Fatalf("err = %v, want ErrInvalidPlaylist", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !sameTitles(list, tt.want...) {
				t.Fatalf("tracks = %v, want %v", titles(list), tt.want)
			}
		})
	}

	// Tracks carry the rating, and the rating sort puts the best first.
	list, err := f.s.EvaluateSmart(ctx, f.user, SmartRules{Sort: "rating", Limit: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sameTitles(list, "One", "Three") || list[0].UserRating != 5 || list[1].UserRating != 3 {
		t.Fatalf("by rating = %v", list)
	}

	// Clearing removes it.
	if err := f.s.SetRating(ctx, f.user, f.a1.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.RatingsFor(ctx, f.user, []string{f.a1.ID, f.a2.ID}); len(got) != 1 || got[f.a2.ID] != 2 {
		t.Fatalf("after clearing = %v", got)
	}
}
