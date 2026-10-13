package store

import (
	"context"
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
