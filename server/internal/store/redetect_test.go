package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
)

// redetectFixture is a TV library of two series and a film library, every
// episode and film analysed for both kinds, and one episode's intro set by
// hand.
type redetectFixture struct {
	s                *Store
	tv, films        Library
	show, other      Item
	s1, s2, otherS1  Item
	s1e1, s1e2, s2e1 Item
	otherE1, film    Item
}

func newRedetectFixture(t *testing.T) redetectFixture {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	f := redetectFixture{s: s}
	var err error
	if f.tv, err = s.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"}); err != nil {
		t.Fatal(err)
	}
	if f.films, err = s.CreateLibrary(ctx, "Films", MediaMovies, []string{"/films"}); err != nil {
		t.Fatal(err)
	}
	upsert := func(lib Library, it Item) Item {
		t.Helper()
		it.LibraryID = lib.ID
		saved, err := s.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	f.show = upsert(f.tv, Item{Kind: KindSeries, Title: "Show", Path: "/tv/Show"})
	f.s1 = upsert(f.tv, Item{Kind: KindSeason, ParentID: f.show.ID, SeasonNum: 1, Path: "/tv/Show/S1"})
	f.s2 = upsert(f.tv, Item{Kind: KindSeason, ParentID: f.show.ID, SeasonNum: 2, Path: "/tv/Show/S2"})
	f.s1e1 = upsert(f.tv, Item{Kind: KindEpisode, ParentID: f.s1.ID, SeasonNum: 1, EpisodeNum: 1, Path: "/tv/Show/S1/e1.mkv", FileMtime: 100})
	f.s1e2 = upsert(f.tv, Item{Kind: KindEpisode, ParentID: f.s1.ID, SeasonNum: 1, EpisodeNum: 2, Path: "/tv/Show/S1/e2.mkv", FileMtime: 100})
	f.s2e1 = upsert(f.tv, Item{Kind: KindEpisode, ParentID: f.s2.ID, SeasonNum: 2, EpisodeNum: 1, Path: "/tv/Show/S2/e1.mkv", FileMtime: 100})
	f.other = upsert(f.tv, Item{Kind: KindSeries, Title: "Other", Path: "/tv/Other"})
	f.otherS1 = upsert(f.tv, Item{Kind: KindSeason, ParentID: f.other.ID, SeasonNum: 1, Path: "/tv/Other/S1"})
	f.otherE1 = upsert(f.tv, Item{Kind: KindEpisode, ParentID: f.otherS1.ID, SeasonNum: 1, EpisodeNum: 1, Path: "/tv/Other/S1/e1.mkv", FileMtime: 100})
	f.film = upsert(f.films, Item{Kind: KindMovie, Title: "Film", Path: "/films/Film.mkv", FileMtime: 100})

	seg := func(kind string) *Segment { return &Segment{Kind: kind, StartSecs: 10, EndSecs: 50} }
	for _, ep := range []Item{f.s1e1, f.s1e2, f.s2e1, f.otherE1} {
		for _, kind := range SegmentKinds {
			if err := s.SaveSegmentAnalysis(ctx, ep.ID, kind, 100, seg(kind)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.SaveSegmentAnalysis(ctx, f.film.ID, SegmentCredits, 100, seg(SegmentCredits)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetManualSegment(ctx, f.s1e2.ID, SegmentIntro, &Segment{Kind: SegmentIntro, StartSecs: 5, EndSecs: 40}); err != nil {
		t.Fatal(err)
	}
	return f
}

// due lists the items whose analysis of kind is open, in the order given.
func (f redetectFixture) due(t *testing.T, kind string, items ...Item) []string {
	t.Helper()
	var out []string
	for _, it := range items {
		states, err := f.s.SegmentStates(context.Background(), it.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range states {
			if st.Kind == kind && !st.Analysed {
				out = append(out, it.Path)
			}
		}
	}
	return out
}

// Re-detection reopens exactly what it is pointed at, and names the
// seasons and films detection should take up first.
func TestRedetectScopes(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		items   func(f redetectFixture) []string
		library bool
		opts    RedetectOptions
		// The paths due afterwards, for each kind, across every item.
		intro, credits []string
		res            RedetectResult
		seasons        func(f redetectFixture) []string
		films          func(f redetectFixture) []string
		// seasonsAnyOrder: seasons of different series, which come in
		// no particular order.
		seasonsAnyOrder bool
		// handSet is whether the hand-set intro is still locked after.
		handSet bool
	}{
		{
			name:    "an episode reopens only itself",
			items:   func(f redetectFixture) []string { return []string{f.s1e1.ID} },
			intro:   []string{"/tv/Show/S1/e1.mkv"},
			credits: []string{"/tv/Show/S1/e1.mkv"},
			res:     RedetectResult{Items: 1, Reopened: 2},
			seasons: func(f redetectFixture) []string { return []string{f.s1.ID} },
			handSet: true,
		},
		{
			name:    "a season keeps the hand-set intro",
			items:   func(f redetectFixture) []string { return []string{f.s1.ID} },
			intro:   []string{"/tv/Show/S1/e1.mkv"},
			credits: []string{"/tv/Show/S1/e1.mkv", "/tv/Show/S1/e2.mkv"},
			res:     RedetectResult{Items: 2, Reopened: 3, Locked: 1},
			seasons: func(f redetectFixture) []string { return []string{f.s1.ID} },
			handSet: true,
		},
		{
			name:    "a season, hand-set times included",
			items:   func(f redetectFixture) []string { return []string{f.s1.ID} },
			opts:    RedetectOptions{IncludeLocked: true},
			intro:   []string{"/tv/Show/S1/e1.mkv", "/tv/Show/S1/e2.mkv"},
			credits: []string{"/tv/Show/S1/e1.mkv", "/tv/Show/S1/e2.mkv"},
			res:     RedetectResult{Items: 2, Reopened: 4},
			seasons: func(f redetectFixture) []string { return []string{f.s1.ID} },
		},
		{
			name:    "a series covers every season, in order",
			items:   func(f redetectFixture) []string { return []string{f.show.ID} },
			opts:    RedetectOptions{Kinds: []string{SegmentCredits}},
			credits: []string{"/tv/Show/S1/e1.mkv", "/tv/Show/S1/e2.mkv", "/tv/Show/S2/e1.mkv"},
			res:     RedetectResult{Items: 3, Reopened: 3},
			seasons: func(f redetectFixture) []string { return []string{f.s1.ID, f.s2.ID} },
			handSet: true,
		},
		{
			name:    "a film reopens its credits",
			items:   func(f redetectFixture) []string { return []string{f.film.ID} },
			credits: []string{"/films/Film.mkv"},
			res:     RedetectResult{Items: 1, Reopened: 1},
			films:   func(f redetectFixture) []string { return []string{f.film.ID} },
			handSet: true,
		},
		{
			name:            "a selection across series and films",
			items:           func(f redetectFixture) []string { return []string{f.s2e1.ID, f.other.ID, f.film.ID} },
			opts:            RedetectOptions{Kinds: []string{SegmentIntro}},
			intro:           []string{"/tv/Show/S2/e1.mkv", "/tv/Other/S1/e1.mkv"},
			res:             RedetectResult{Items: 3, Reopened: 2},
			seasons:         func(f redetectFixture) []string { return []string{f.otherS1.ID, f.s2.ID} },
			films:           func(f redetectFixture) []string { return []string{f.film.ID} },
			seasonsAnyOrder: true,
			handSet:         true,
		},
		{
			name:    "a library",
			library: true,
			intro:   []string{"/tv/Show/S1/e1.mkv", "/tv/Show/S2/e1.mkv", "/tv/Other/S1/e1.mkv"},
			credits: []string{"/tv/Show/S1/e1.mkv", "/tv/Show/S1/e2.mkv", "/tv/Show/S2/e1.mkv", "/tv/Other/S1/e1.mkv"},
			res:     RedetectResult{Items: 4, Reopened: 7, Locked: 1},
			handSet: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newRedetectFixture(t)
			var res RedetectResult
			var err error
			if tc.library {
				res, err = f.s.RedetectLibrarySegments(ctx, f.tv.ID, tc.opts)
			} else {
				res, err = f.s.RedetectItemSegments(ctx, tc.items(f), tc.opts)
			}
			if err != nil {
				t.Fatal(err)
			}
			// A film is never looked at for an intro.
			eps := []Item{f.s1e1, f.s1e2, f.s2e1, f.otherE1}
			all := append(slices.Clone(eps), f.film)
			if got := f.due(t, SegmentIntro, eps...); !reflect.DeepEqual(got, tc.intro) {
				t.Errorf("intros due = %v, want %v", got, tc.intro)
			}
			if got := f.due(t, SegmentCredits, all...); !reflect.DeepEqual(got, tc.credits) {
				t.Errorf("credits due = %v, want %v", got, tc.credits)
			}
			if res.Items != tc.res.Items || res.Reopened != tc.res.Reopened || res.Locked != tc.res.Locked {
				t.Errorf("result = %+v, want items %d, reopened %d, locked %d", res, tc.res.Items, tc.res.Reopened, tc.res.Locked)
			}
			var seasons, films []string
			if tc.seasons != nil {
				seasons = tc.seasons(f)
			}
			if tc.films != nil {
				films = tc.films(f)
			}
			gotSeasons := slices.Clone(res.Seasons)
			if tc.seasonsAnyOrder {
				slices.Sort(gotSeasons)
				slices.Sort(seasons)
			}
			if !reflect.DeepEqual(gotSeasons, seasons) || !reflect.DeepEqual(res.Films, films) {
				t.Errorf("targets = seasons %v films %v, want %v %v", res.Seasons, res.Films, seasons, films)
			}
			if st, err := f.s.SegmentStates(ctx, f.s1e2.ID); err != nil || st[0].Locked != tc.handSet {
				t.Errorf("hand-set intro locked = %v (%v), want %v", st[0].Locked, err, tc.handSet)
			}
		})
	}
}

func TestRedetectRejects(t *testing.T) {
	f := newRedetectFixture(t)
	ctx := context.Background()
	if _, err := f.s.RedetectItemSegments(ctx, []string{f.s1.ID}, RedetectOptions{Kinds: []string{"recap"}}); err == nil {
		t.Error("an unknown kind was accepted")
	}
	if _, err := f.s.RedetectItemSegments(ctx, make([]string, MaxRedetectItems+1), RedetectOptions{}); err == nil {
		t.Error("an oversized selection was accepted")
	}
	if r, err := f.s.RedetectItemSegments(ctx, nil, RedetectOptions{}); err != nil || r.Items != 0 {
		t.Errorf("empty selection = %+v, %v", r, err)
	}
}

// A film is due for credits until its current file has been looked at;
// a changed file is due again, a hand-set value never.
func TestFilmsNeedingCredits(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "Films", MediaMovies, []string{"/films"})
	if err != nil {
		t.Fatal(err)
	}
	upsert := func(it Item) Item {
		t.Helper()
		it.LibraryID = lib.ID
		it.Kind = KindMovie
		saved, err := s.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	a := upsert(Item{Title: "Alpha", Path: "/films/a.mkv", FileMtime: 100, RuntimeSecs: 6000})
	b := upsert(Item{Title: "Bravo", Path: "/films/b.mkv", FileMtime: 100})
	upsert(Item{Title: "Charlie", Path: "/films/c.mkv"}) // not stamped by a scan yet

	titles := func() []string {
		t.Helper()
		films, err := s.FilmsNeedingCredits(ctx, lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range films {
			out = append(out, f.Title)
		}
		return out
	}
	if got := titles(); !reflect.DeepEqual(got, []string{"Alpha", "Bravo"}) {
		t.Fatalf("due = %v, want Alpha and Bravo", got)
	}
	if err := s.SaveSegmentAnalysis(ctx, a.ID, SegmentCredits, 100, &Segment{Kind: SegmentCredits, StartSecs: 5700, EndSecs: 6000}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetManualSegment(ctx, b.ID, SegmentCredits, nil); err != nil {
		t.Fatal(err)
	}
	if got := titles(); len(got) != 0 {
		t.Fatalf("due after analysis = %v, want none", got)
	}
	st, err := s.FilmCreditsState(ctx, a.ID)
	if err != nil || !st.Analysed || st.RuntimeSecs != 6000 || st.LibraryID != lib.ID {
		t.Fatalf("state = %+v, %v", st, err)
	}
	upsert(Item{Title: "Alpha", Path: "/films/a.mkv", FileMtime: 200})
	upsert(Item{Title: "Bravo", Path: "/films/b.mkv", FileMtime: 200})
	if got := titles(); !reflect.DeepEqual(got, []string{"Alpha"}) {
		t.Fatalf("due after both files changed = %v, want Alpha only (Bravo was set by hand)", got)
	}
	if _, err := s.FilmCreditsState(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing film = %v, want ErrNotFound", err)
	}
}
