package segments

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

func TestNeighbours(t *testing.T) {
	tests := []struct {
		i, total, n int
		want        []int
	}{
		{0, 5, 4, []int{1, 2, 3, 4}},
		{2, 5, 4, []int{3, 1, 4, 0}},
		{4, 5, 2, []int{3, 2}},
		{0, 2, 4, []int{1}},
		{0, 1, 4, nil},
	}
	for _, tc := range tests {
		if got := neighbours(tc.i, tc.total, tc.n); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("neighbours(%d, %d, %d) = %v, want %v", tc.i, tc.total, tc.n, got, tc.want)
		}
	}
}

// A season's episodes get their intro and credits wherever each episode
// puts them, and a second pass decodes nothing.
func TestDetectorFindsIntrosAndCredits(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	lib, err := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	save := func(it store.Item) store.Item {
		t.Helper()
		it.LibraryID = lib.ID
		saved, err := st.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	series := save(store.Item{Kind: store.KindSeries, Title: "Show", Path: "/tv/Show"})
	season := save(store.Item{Kind: store.KindSeason, ParentID: series.ID, Path: "/tv/Show/S1"})

	// Each episode opens with the same 45 s theme and closes with the same
	// 60 s end theme, each where that episode happens to put it.
	th, ct := theme(1, 45), theme(9, 60)
	starts := map[string]float64{"/e1.mkv": 20, "/e2.mkv": 75, "/e3.mkv": 5}
	// Seconds into the ten-minute end window; the window opens at 1800 s
	// of a 2400 s episode.
	credits := map[string]float64{"/e1.mkv": 300, "/e2.mkv": 340, "/e3.mkv": 280}
	ids := map[string]string{}
	for i, p := range []string{"/e1.mkv", "/e2.mkv", "/e3.mkv"} {
		ep := save(store.Item{Kind: store.KindEpisode, ParentID: season.ID, EpisodeNum: i + 1,
			Path: p, FileMtime: 1, RuntimeSecs: 2400})
		ids[p] = ep.ID
	}

	var decodes atomic.Int32
	d := NewDetector(st)
	d.Fingerprint = func(_ context.Context, path string, start, dur float64) (media.Fingerprint, error) {
		decodes.Add(1)
		if dur != 600 {
			t.Errorf("window = %v, want 600 — a quarter of a 40-minute episode", dur)
		}
		seed := int64(len(path)) + int64(starts[path])
		switch start {
		case 0:
			return media.FingerprintSamples(episode(seed, 240, starts[path], th)), nil
		case 1800:
			fp := media.FingerprintSamples(episode(seed+100, 420, credits[path], ct))
			fp.Start = start
			return fp, nil
		}
		t.Errorf("window starts at %v, want 0 for intros or 1800 for credits", start)
		return media.Fingerprint{}, nil
	}

	if err := d.analyseLibrary(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	if n := decodes.Load(); n != 6 {
		t.Errorf("%d decodes, want 6 — each file once per end", n)
	}
	near := func(got, want, tol float64) bool { return math.Abs(got-want) <= tol }
	for p, at := range starts {
		segs, err := st.Segments(ctx, ids[p])
		if err != nil || len(segs) != 2 {
			t.Fatalf("%s: segments %+v, %v; want an intro and credits", p, segs, err)
		}
		byKind := map[string]store.Segment{}
		for _, sg := range segs {
			byKind[sg.Kind] = sg
		}
		in, cr := byKind[store.SegmentIntro], byKind[store.SegmentCredits]
		if !near(in.StartSecs, at, 1) || !near(in.EndSecs, at+45, 2) {
			t.Errorf("%s: intro %.1f-%.1f, want %.0f-%.0f", p, in.StartSecs, in.EndSecs, at, at+45)
		}
		if want := 1800 + credits[p]; !near(cr.StartSecs, want, 1) || !near(cr.EndSecs, want+60, 2) {
			t.Errorf("%s: credits %.1f-%.1f, want %.0f-%.0f", p, cr.StartSecs, cr.EndSecs, want, want+60)
		}
	}

	// The run is summarised for the admin, with each episode's outcome.
	st2 := d.Status()
	if st2.Running || st2.Last == nil || st2.Last.Episodes != 6 || st2.Last.IntrosFound != 3 ||
		st2.Last.CreditsFound != 3 || len(st2.Recent) != 6 {
		t.Errorf("status after run = %+v (last %+v), want an idle summary of 6 analyses, 3 intros, 3 credits", st2, st2.Last)
	}
	if r := st2.Recent[0]; r.Series != "Show" || !r.Found || r.Episode == 0 || r.Kind == "" {
		t.Errorf("recent[0] = %+v, want a found segment labelled with its kind, series and episode", r)
	}

	decodes.Store(0)
	if err := d.analyseLibrary(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	if n := decodes.Load(); n != 0 {
		t.Errorf("second pass decoded %d files, want none", n)
	}
}

// A file that cannot be read is not recorded as analysed, so it is tried
// again on the next pass rather than marked "no intro" for good.
func TestDetectorRetriesUnreadableFiles(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	lib, _ := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{"/tv"})
	save := func(it store.Item) store.Item {
		t.Helper()
		it.LibraryID = lib.ID
		saved, err := st.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	series := save(store.Item{Kind: store.KindSeries, Title: "Show", Path: "/tv/Show"})
	season := save(store.Item{Kind: store.KindSeason, ParentID: series.ID, Path: "/tv/Show/S1"})
	for i, p := range []string{"/e1.mkv", "/e2.mkv"} {
		save(store.Item{Kind: store.KindEpisode, ParentID: season.ID, EpisodeNum: i + 1, Path: p, FileMtime: 1, RuntimeSecs: 2400})
	}

	d := NewDetector(st)
	d.Fingerprint = func(context.Context, string, float64, float64) (media.Fingerprint, error) {
		return media.Fingerprint{}, errors.New("host is down")
	}
	if err := d.analyseLibrary(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	for _, sp := range specs {
		seasons, err := st.SeasonsNeedingSegments(ctx, lib.ID, sp.kind)
		if err != nil || len(seasons) != 1 {
			t.Errorf("%s: seasons still needing analysis = %v, %v; want the season, to retry", sp.kind, seasons, err)
		}
	}
	if s := d.Status(); s.Last == nil || s.Last.Unreadable == 0 {
		t.Errorf("status = %+v, want the unreadable files counted", s.Last)
	}

	// Storage known to be down: the library is skipped without decoding.
	calls := 0
	d.Fingerprint = func(context.Context, string, float64, float64) (media.Fingerprint, error) {
		calls++
		return media.Fingerprint{}, nil
	}
	d.Available = func(string) bool { return false }
	if err := d.analyseLibrary(ctx, lib.ID); err != nil || calls != 0 {
		t.Errorf("unavailable storage: %d decode(s), err %v; want none", calls, err)
	}
}

// Detection waits while something else needs the storage, or an admin has
// paused it, and says why; it carries on once the cooldown has passed.
func TestDetectorGate(t *testing.T) {
	d := NewDetector(nil)
	d.Cooldown = 1500 * time.Millisecond
	var busy atomic.Value
	busy.Store("playing")
	d.Busy = func() string { return busy.Load().(string) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan time.Time, 1)
	go func() {
		if err := d.gate(ctx); err == nil {
			done <- time.Now()
		}
	}()

	time.Sleep(1200 * time.Millisecond)
	if st := d.Status(); !st.Paused || st.PauseReason != "playing" {
		t.Fatalf("while playing: status %+v, want paused for playing", st)
	}
	quiet := time.Now()
	busy.Store("")
	select {
	case at := <-done:
		if waited := at.Sub(quiet); waited < time.Second {
			t.Errorf("resumed %v after playback stopped; want the cooldown first", waited)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("never resumed after playback stopped")
	}
	if st := d.Status(); st.Paused {
		t.Errorf("after resuming: status %+v, want not paused", st)
	}

	// A manual pause holds until lifted, whatever else is happening.
	d.SetPaused(true)
	go func() {
		if err := d.gate(ctx); err == nil {
			done <- time.Now()
		}
	}()
	select {
	case <-done:
		t.Fatal("gate opened while paused by an admin")
	case <-time.After(1200 * time.Millisecond):
	}
	if st := d.Status(); st.PauseReason != "manual" {
		t.Errorf("status %+v, want paused manually", st)
	}
	d.SetPaused(false)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("never resumed after the admin unpaused")
	}
}

// With a rate limit, reading waits long enough to average under it.
func TestDetectorPace(t *testing.T) {
	d := NewDetector(nil)
	ep := store.SegmentEpisode{SizeBytes: 4_000_000, RuntimeSecs: 2400}
	tests := []struct {
		name  string
		limit int64
		want  time.Duration // 1 MB read (a quarter of the file) at the limit
	}{
		{"no limit", 0, 0},
		{"10 MB/s", 10_000_000, 100 * time.Millisecond},
	}
	for _, tc := range tests {
		d.SetRateLimit(tc.limit)
		start := time.Now()
		d.pace(context.Background(), ep, 600, 0)
		got := time.Since(start)
		if got < tc.want || got > tc.want+80*time.Millisecond {
			t.Errorf("%s: paced %v, want about %v", tc.name, got, tc.want)
		}
	}
}
