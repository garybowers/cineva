package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// Analysis is recorded per file: an episode is analysed once, found intro
// or not, and again only when its file changes.
func TestSegmentAnalysis(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	upsert := func(it Item) Item {
		t.Helper()
		it.LibraryID = lib.ID
		saved, err := s.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	series := upsert(Item{Kind: KindSeries, Title: "Show", Path: "/tv/Show"})
	season := upsert(Item{Kind: KindSeason, ParentID: series.ID, Path: "/tv/Show/S1"})
	ep1 := upsert(Item{Kind: KindEpisode, ParentID: season.ID, EpisodeNum: 1, Path: "/tv/Show/S1/e1.mkv", FileMtime: 100})
	ep2 := upsert(Item{Kind: KindEpisode, ParentID: season.ID, EpisodeNum: 2, Path: "/tv/Show/S1/e2.mkv", FileMtime: 100})
	// Not yet stamped by a scan, so not offered for analysis.
	upsert(Item{Kind: KindEpisode, ParentID: season.ID, EpisodeNum: 3, Path: "/tv/Show/S1/e3.mkv"})

	needing := func() []string {
		t.Helper()
		ids, err := s.SeasonsNeedingSegments(ctx, lib.ID, SegmentIntro)
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	if got := needing(); len(got) != 1 || got[0] != season.ID {
		t.Fatalf("before analysis: %v, want [%s]", got, season.ID)
	}
	state, err := s.SeasonSegmentState(ctx, season.ID, SegmentIntro)
	if err != nil || len(state) != 2 || state[0].ID != ep1.ID || state[0].Analysed {
		t.Fatalf("state = %+v, %v; want ep1 and ep2, unanalysed", state, err)
	}

	intro := &Segment{Kind: SegmentIntro, StartSecs: 62.5, EndSecs: 112.5}
	if err := s.SaveSegmentAnalysis(ctx, ep1.ID, SegmentIntro, 100, intro); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSegmentAnalysis(ctx, ep2.ID, SegmentIntro, 100, nil); err != nil {
		t.Fatal(err)
	}
	if got := needing(); len(got) != 0 {
		t.Fatalf("after analysis: %v, want none", got)
	}
	segs, err := s.Segments(ctx, ep1.ID)
	if err != nil || len(segs) != 1 || segs[0] != *intro {
		t.Fatalf("segments = %+v, %v; want [%+v]", segs, err, *intro)
	}
	if segs, _ := s.Segments(ctx, ep2.ID); len(segs) != 0 {
		t.Fatalf("ep2 segments = %+v, want none", segs)
	}

	// A replaced file is analysed again; the stale segment is replaced on
	// the next save, not kept alongside it.
	upsert(Item{Kind: KindEpisode, ParentID: season.ID, EpisodeNum: 1, Path: "/tv/Show/S1/e1.mkv", FileMtime: 200})
	if got := needing(); len(got) != 1 {
		t.Fatalf("after file change: %v, want the season again", got)
	}
	if err := s.SaveSegmentAnalysis(ctx, ep1.ID, SegmentIntro, 200, nil); err != nil {
		t.Fatal(err)
	}
	if segs, _ := s.Segments(ctx, ep1.ID); len(segs) != 0 {
		t.Fatalf("ep1 segments after reanalysis = %+v, want none", segs)
	}
}

// Reaching detected credits marks an episode watched, even short of the 90%
// a file without them needs.
func TestCreditsCountAsWatched(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindEpisode, Path: "/tv/e1.mkv", RuntimeSecs: 1300})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindEpisode, Path: "/tv/e2.mkv", RuntimeSecs: 1300})
	if err != nil {
		t.Fatal(err)
	}
	// Credits from 1100 s of 1300: 85%, under the plain threshold.
	if err := s.SaveSegmentAnalysis(ctx, ep.ID, SegmentCredits, 1, &Segment{Kind: SegmentCredits, StartSecs: 1100, EndSecs: 1290}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		id   string
		pos  int
		want bool
	}{
		{"before credits", ep.ID, 1050, false},
		{"at credits", ep.ID, 1100, true},
		{"same position, no credits", other.ID, 1100, false},
		{"90% without credits", other.ID, 1170, true},
	}
	for _, tc := range tests {
		ps, err := s.SetProgress(ctx, "u1", tc.id, tc.pos, 1300)
		if err != nil {
			t.Fatal(err)
		}
		if ps.Watched != tc.want {
			t.Errorf("%s: watched = %v, want %v", tc.name, ps.Watched, tc.want)
		}
	}
}

// A segment set by hand is locked: detection cannot replace it, a season's
// re-detection and a replaced file leave it alone, and clearing it hands
// the episode back to detection.
func TestManualSegments(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	upsert := func(it Item) Item {
		t.Helper()
		it.LibraryID = lib.ID
		saved, err := s.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	series := upsert(Item{Kind: KindSeries, Title: "Show", Path: "/tv/Show"})
	season := upsert(Item{Kind: KindSeason, ParentID: series.ID, SeasonNum: 1, Path: "/tv/Show/S1"})
	ep1 := upsert(Item{Kind: KindEpisode, ParentID: season.ID, EpisodeNum: 1, Path: "/tv/Show/S1/e1.mkv", FileMtime: 100})
	ep2 := upsert(Item{Kind: KindEpisode, ParentID: season.ID, EpisodeNum: 2, Path: "/tv/Show/S1/e2.mkv", FileMtime: 100})

	state := func(id, kind string) SegmentState {
		t.Helper()
		states, err := s.SegmentStates(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(states) != len(SegmentKinds) {
			t.Fatalf("states = %+v, want one per kind", states)
		}
		for _, st := range states {
			if st.Kind == kind {
				return st
			}
		}
		t.Fatalf("no %s state in %+v", kind, states)
		return SegmentState{}
	}
	needing := func() int {
		t.Helper()
		n, err := s.CountEpisodesNeedingSegments(ctx, lib.ID, SegmentIntro)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	detected := &Segment{Kind: SegmentIntro, StartSecs: 10, EndSecs: 50}
	for _, ep := range []Item{ep1, ep2} {
		if err := s.SaveSegmentAnalysis(ctx, ep.ID, SegmentIntro, 100, detected); err != nil {
			t.Fatal(err)
		}
	}
	if got := state(ep1.ID, SegmentIntro); !got.Found || got.Locked || !got.Analysed || got.StartSecs != 10 {
		t.Fatalf("detected state = %+v", got)
	}
	if got := state(ep1.ID, SegmentCredits); got.Found || got.Analysed || got.Locked {
		t.Fatalf("credits state = %+v, want nothing", got)
	}

	// Corrected by hand on ep1; ep2 is said to have none.
	manual := &Segment{Kind: SegmentIntro, StartSecs: 62, EndSecs: 101}
	if err := s.SetManualSegment(ctx, ep1.ID, SegmentIntro, manual); err != nil {
		t.Fatal(err)
	}
	if err := s.SetManualSegment(ctx, ep2.ID, SegmentIntro, nil); err != nil {
		t.Fatal(err)
	}
	if got := state(ep1.ID, SegmentIntro); !got.Found || !got.Locked || got.StartSecs != 62 || got.EndSecs != 101 {
		t.Fatalf("manual state = %+v", got)
	}
	if got := state(ep2.ID, SegmentIntro); got.Found || !got.Locked {
		t.Fatalf("manual none = %+v, want locked with no segment", got)
	}

	// Detection saving over it changes nothing.
	if err := s.SaveSegmentAnalysis(ctx, ep1.ID, SegmentIntro, 100, detected); err != nil {
		t.Fatal(err)
	}
	if got := state(ep1.ID, SegmentIntro); got.StartSecs != 62 || !got.Locked {
		t.Fatalf("after detection saved over it: %+v, want the manual value", got)
	}

	// Re-detecting the season reopens nothing locked, and a replaced file
	// does not unlock it either.
	if r, err := s.RedetectItemSegments(ctx, []string{season.ID}, RedetectOptions{}); err != nil || r.Reopened != 0 || r.Locked != 2 {
		t.Fatalf("redetect = %+v, %v; want 0 reopened and 2 kept (both locked)", r, err)
	}
	upsert(Item{Kind: KindEpisode, ParentID: season.ID, EpisodeNum: 1, Path: "/tv/Show/S1/e1.mkv", FileMtime: 200})
	if n := needing(); n != 0 {
		t.Fatalf("%d episodes need intro detection, want 0 — all locked", n)
	}

	// Cleared: back to detection, segment and all.
	if err := s.ClearManualSegment(ctx, ep1.ID, SegmentIntro); err != nil {
		t.Fatal(err)
	}
	if got := state(ep1.ID, SegmentIntro); got.Found || got.Locked || got.Analysed {
		t.Fatalf("after clearing = %+v, want nothing", got)
	}
	if n := needing(); n != 1 {
		t.Fatalf("%d episodes need detection after clearing, want 1", n)
	}
	// Clearing a detected value is a no-op.
	if err := s.SaveSegmentAnalysis(ctx, ep1.ID, SegmentIntro, 200, detected); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearManualSegment(ctx, ep1.ID, SegmentIntro); err != nil {
		t.Fatal(err)
	}
	if got := state(ep1.ID, SegmentIntro); !got.Found || !got.Analysed {
		t.Fatalf("clearing a detected value removed it: %+v", got)
	}

	// Re-detection reopens the detected one and keeps serving it meanwhile.
	if r, err := s.RedetectItemSegments(ctx, []string{season.ID}, RedetectOptions{}); err != nil || r.Reopened != 1 {
		t.Fatalf("redetect = %+v, %v; want 1 reopened", r, err)
	}
	if got := state(ep1.ID, SegmentIntro); !got.Found || got.Analysed {
		t.Fatalf("after redetect = %+v, want the old segment, due for analysis", got)
	}
	if n := needing(); n != 1 {
		t.Fatalf("%d need detection after redetect, want 1", n)
	}
}

// A season's lone episode is offered partners from the seasons either
// side, nearest first, never from the specials.
func TestAdjacentSeasonEpisodes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	upsert := func(it Item) Item {
		t.Helper()
		it.LibraryID = lib.ID
		saved, err := s.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	series := upsert(Item{Kind: KindSeries, Title: "Show", Path: "/tv/Show"})
	other := upsert(Item{Kind: KindSeries, Title: "Other", Path: "/tv/Other"})
	seasons := map[int]Item{}
	for _, n := range []int{0, 1, 2, 3, 5} {
		seasons[n] = upsert(Item{Kind: KindSeason, ParentID: series.ID, SeasonNum: n, Path: fmt.Sprintf("/tv/Show/S%d", n)})
	}
	otherS2 := upsert(Item{Kind: KindSeason, ParentID: other.ID, SeasonNum: 2, Path: "/tv/Other/S2"})
	ep := func(season Item, num int) string {
		path := fmt.Sprintf("%s/e%d.mkv", season.Path, num)
		upsert(Item{Kind: KindEpisode, ParentID: season.ID, EpisodeNum: num, Path: path, FileMtime: 1})
		return path
	}
	ep(seasons[0], 1)
	s1 := []string{ep(seasons[1], 1), ep(seasons[1], 2), ep(seasons[1], 3)}
	ep(seasons[2], 1)
	s3 := []string{ep(seasons[3], 1), ep(seasons[3], 2)}
	s5 := ep(seasons[5], 1)
	ep(otherS2, 1)
	// Unstamped: never offered.
	upsert(Item{Kind: KindEpisode, ParentID: seasons[3].ID, EpisodeNum: 9, Path: "/tv/Show/S3/e9.mkv"})

	tests := []struct {
		name   string
		season Item
		n      int
		want   []string
	}{
		{"either side, alternating, nearest episodes first", seasons[2], 4, []string{s3[0], s1[2], s3[1], s1[1]}},
		{"all there is", seasons[2], 10, []string{s3[0], s1[2], s3[1], s1[1], s1[0], s5}},
		{"one side", seasons[1], 3, []string{seasons[2].Path + "/e1.mkv", s3[0], s3[1]}},
		{"specials compare with the series", seasons[0], 1, []string{s1[0]}},
		{"none asked for", seasons[2], 0, nil},
	}
	for _, tc := range tests {
		eps, err := s.AdjacentSeasonEpisodes(ctx, tc.season.ID, tc.n)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range eps {
			got = append(got, e.Path)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
