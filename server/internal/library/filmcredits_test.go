package library

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

// A scanned film's end credits come from its chapter names, from the probe
// the scan makes anyway; credits set by hand survive a rescan.
func TestScanRecordsFilmCredits(t *testing.T) {
	root := t.TempDir()
	writeAt(t, filepath.Join(root, "Named (2001)", "Named (2001).mkv"), time.Now().Add(-time.Hour))
	writeAt(t, filepath.Join(root, "Plain (2002)", "Plain (2002).mkv"), time.Now().Add(-time.Hour))
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, nil, "")
	m.FilmCredits = true
	m.Probe = func(path string) (media.Info, error) {
		last := "Chapter 3"
		if filepath.Base(path) == "Named (2001).mkv" {
			last = "End Credits"
		}
		return media.Info{RuntimeSecs: 6000, Chapters: []media.Chapter{
			{StartSecs: 0, EndSecs: 3000, Title: "Chapter 1"},
			{StartSecs: 3000, EndSecs: 5700, Title: "Chapter 2"},
			{StartSecs: 5700, EndSecs: 6000, Title: last},
		}}, nil
	}
	scanOnce(t, m, lib, false)

	items, err := st.ItemsByLibrary(ctx, lib.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	byTitle := map[string]store.Item{}
	for _, it := range items {
		byTitle[it.Title] = it
	}
	segs, err := st.Segments(ctx, byTitle["Named"].ID)
	if err != nil || len(segs) != 1 || segs[0].StartSecs != 5700 || segs[0].EndSecs != 6000 {
		t.Fatalf("Named credits = %+v, %v; want 5700-6000", segs, err)
	}
	if segs, _ := st.Segments(ctx, byTitle["Plain"].ID); len(segs) != 0 {
		t.Fatalf("Plain has %+v, want nothing", segs)
	}
	// Both were looked at, so detection has nothing left to do.
	if due, err := st.FilmsNeedingCredits(ctx, lib.ID); err != nil || len(due) != 0 {
		t.Fatalf("films still due = %+v, %v", due, err)
	}

	// Set by hand, then rescanned in full: the hand-set value stays.
	manual := &store.Segment{Kind: store.SegmentCredits, StartSecs: 5600, EndSecs: 6000}
	if err := st.SetManualSegment(ctx, byTitle["Named"].ID, store.SegmentCredits, manual); err != nil {
		t.Fatal(err)
	}
	scanOnce(t, m, lib, true)
	if segs, _ := st.Segments(ctx, byTitle["Named"].ID); len(segs) != 1 || segs[0].StartSecs != 5600 {
		t.Fatalf("after a rescan = %+v, want the hand-set 5600", segs)
	}
}
