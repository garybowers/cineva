package trickplay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/background"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

type fixture struct {
	st    *store.Store
	lib   store.Library
	items []store.Item // oldest addition first
	b     *Builder
	busy  atomic.Value

	mu    sync.Mutex
	built []string // item ids, in the order generated
}

// newFixture makes a films library of n titles, added oldest first, and a
// builder whose generator writes the cue file and records the order.
func newFixture(t *testing.T, n int) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f := &fixture{st: st}
	if f.lib, err = st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{"/films"}); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		it, err := st.UpsertItem(ctx, store.Item{LibraryID: f.lib.ID, Kind: store.KindMovie,
			Title: string(rune('A' + i)), Path: "/films/" + string(rune('a'+i)) + ".mkv",
			RuntimeSecs: 6000, FileMtime: int64(1_600_000_000 + i*1000)})
		if err != nil {
			t.Fatal(err)
		}
		f.items = append(f.items, it)
	}
	gate := background.NewGate(background.NewPolicy())
	gate.Poll, gate.Cooldown = 5*time.Millisecond, 0
	f.busy.Store("")
	gate.Busy = func() string { return f.busy.Load().(string) }
	f.b = New(st, t.TempDir(), gate)
	f.b.Generate = func(ctx context.Context, path, dir, itemID string, runtimeSecs int, progress func(int64) error) error {
		f.mu.Lock()
		f.built = append(f.built, itemID)
		f.mu.Unlock()
		return writeCues(dir, itemID)
	}
	return f
}

func writeCues(dir, itemID string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	_, vtt := media.TrickplayPaths(dir, itemID)
	return os.WriteFile(vtt, []byte("WEBVTT\n"), 0o644)
}

func (f *fixture) run(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.b.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

// preview reads an item as a player's request would hand it over.
func (f *fixture) preview(t *testing.T, id string) store.PreviewItem {
	t.Helper()
	p, err := f.st.PreviewItem(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *fixture) builtIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.built...)
}

// waitFor polls cond for up to two seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A library's titles are built newest addition first, skipping those that
// already have previews; a title a player asks for goes to the front.
func TestBuilderOrder(t *testing.T) {
	f := newFixture(t, 4)
	a, b, c, d := f.items[0], f.items[1], f.items[2], f.items[3]
	if err := writeCues(f.b.Dir, c.ID); err != nil { // already has previews
		t.Fatal(err)
	}
	f.b.EnqueueLibrary(f.lib.ID)
	f.b.Request(f.preview(t, a.ID))
	// Swept before anything runs, so the order below is the queue's.
	if err := f.b.sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := f.b.Status(); st.Waiting != 3 {
		t.Fatalf("waiting = %d, want 3 — the requested title once, plus the two without previews", st.Waiting)
	}
	f.run(t)
	waitFor(t, "three titles built", func() bool { return len(f.builtIDs()) == 3 })
	got := f.builtIDs()
	if want := []string{a.ID, d.ID, b.ID}; got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("built %v, want %v: the requested title, then newest first", got, want)
	}
	waitFor(t, "the run summary", func() bool { st := f.b.Status(); return !st.Running && st.Last != nil })
	if st := f.b.Status(); st.Last.Done != 3 || st.Waiting != 0 {
		t.Fatalf("status %+v (last %+v), want 3 done and nothing waiting", st, st.Last)
	}
}

// Nothing is read while something plays, and the reason is shown; building
// carries on once it stops.
func TestBuilderWaitsForPlayback(t *testing.T) {
	f := newFixture(t, 1)
	f.busy.Store("playing")
	f.b.EnqueueLibrary(f.lib.ID)
	f.run(t)
	waitFor(t, "paused for playing", func() bool { st := f.b.Status(); return st.Paused && st.PauseReason == "playing" })
	time.Sleep(50 * time.Millisecond)
	if n := len(f.builtIDs()); n != 0 {
		t.Fatalf("built %d titles while something played", n)
	}
	f.busy.Store("")
	waitFor(t, "built after playback stopped", func() bool { return len(f.builtIDs()) == 1 })
}

// Playback starting part-way through a file abandons it; it is built again,
// whole, once playback stops, and not counted as a failure.
func TestBuilderGivesWayMidFile(t *testing.T) {
	f := newFixture(t, 1)
	var calls atomic.Int32
	f.b.Generate = func(ctx context.Context, path, dir, itemID string, runtimeSecs int, progress func(int64) error) error {
		n := calls.Add(1)
		if n == 1 {
			f.busy.Store("playing") // someone presses play after the first thumbnail
		}
		for i := range 5 {
			if err := progress(int64(i) * 1000); err != nil {
				return err
			}
		}
		return writeCues(dir, itemID)
	}
	f.b.EnqueueLibrary(f.lib.ID)
	f.run(t)
	waitFor(t, "the first attempt to give way", func() bool { return calls.Load() == 1 && f.b.Status().PauseReason == "playing" })
	f.busy.Store("")
	waitFor(t, "a second, complete attempt", func() bool { return f.b.Has(f.items[0].ID, 0) })
	waitFor(t, "the run summary", func() bool { return f.b.Status().Last != nil })
	if l := f.b.Status().Last; l.Done != 1 || l.Failed != 0 || calls.Load() != 2 {
		t.Fatalf("last %+v after %d attempts, want 1 done, 0 failed, 2 attempts", l, calls.Load())
	}
}

// A read that stalls is abandoned and nothing more from that library is
// opened; a library whose storage is known to be down is not touched.
func TestBuilderStopsOnStall(t *testing.T) {
	f := newFixture(t, 3)
	f.b.StallTimeout = 50 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	var opened atomic.Int32
	f.b.Generate = func(context.Context, string, string, string, int, func(int64) error) error {
		opened.Add(1)
		<-release // a share that never answers
		return nil
	}
	f.b.EnqueueLibrary(f.lib.ID)
	f.run(t)
	waitFor(t, "the queue to empty", func() bool { st := f.b.Status(); return opened.Load() > 0 && st.Waiting == 0 && !st.Running })
	time.Sleep(100 * time.Millisecond)
	if n := opened.Load(); n != 1 {
		t.Fatalf("%d files opened, want 1: nothing more after the first stalled", n)
	}
	if st := f.b.Status(); st.Last == nil || st.Last.Failed != 0 {
		t.Fatalf("status %+v, want a stall not counted as a failed file", st)
	}

	f.b.Available = func(string) bool { return false }
	f.b.EnqueueLibrary(f.lib.ID)
	time.Sleep(100 * time.Millisecond)
	if n := opened.Load(); n != 1 {
		t.Fatalf("%d files opened with the storage down, want no more", n)
	}
}

// A file that cannot be made into previews is not tried again on every
// scan, only once it changes.
func TestBuilderRemembersFailures(t *testing.T) {
	f := newFixture(t, 1)
	var calls atomic.Int32
	f.b.Generate = func(context.Context, string, string, string, int, func(int64) error) error {
		calls.Add(1)
		return errors.New("no video stream")
	}
	f.b.EnqueueLibrary(f.lib.ID)
	f.run(t)
	waitFor(t, "the failure", func() bool { l := f.b.Status().Last; return l != nil && l.Failed == 1 })
	f.b.EnqueueLibrary(f.lib.ID)
	f.b.Request(f.preview(t, f.items[0].ID))
	time.Sleep(100 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d attempts, want 1: an unchanged file that failed is not retried", n)
	}
}

// Outside the schedule nothing is built until an admin asks, and the ask
// lasts only until the queue is done.
func TestBuilderScheduleAndRunNow(t *testing.T) {
	f := newFixture(t, 1)
	f.b.Gate.Policy.SetSchedule(background.Schedule{Mode: background.ModeManual})
	f.b.EnqueueLibrary(f.lib.ID)
	f.run(t)
	waitFor(t, "waiting on demand", func() bool { return f.b.Status().PauseReason == "on-demand" })
	if n := len(f.builtIDs()); n != 0 {
		t.Fatalf("built %d titles in manual mode before anyone asked", n)
	}
	f.b.RunNow()
	waitFor(t, "built on demand", func() bool { return len(f.builtIDs()) == 1 })
	waitFor(t, "back on the schedule", func() bool { return f.b.Gate.ScheduleWait() == "on-demand" })
}

// Prune removes the previews of items that are gone and nothing else: not a
// kept title's, not a file named otherwise, not anything in a folder below.
func TestPrune(t *testing.T) {
	f := newFixture(t, 2)
	ctx := context.Background()
	dir := f.b.Dir
	gone := "0b6c0a4e-5a1d-4f3e-9b0e-2f6f0e0c1d2a"
	write := func(name string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	kept := f.items[0].ID
	files := map[string]bool{ // path -> should survive
		write(kept + ".jpg"):                     true,
		write(kept + ".vtt"):                     true,
		write(gone + ".jpg"):                     false,
		write(gone + ".vtt"):                     false,
		write("poster.jpg"):                      true,
		write(gone + ".mkv"):                     true,
		write(filepath.Join("sub", gone+".jpg")): true,
	}
	n, err := f.b.Prune(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("pruned %d titles, want 1", n)
	}
	for p, survive := range files {
		_, err := os.Stat(p)
		if exists := err == nil; exists != survive {
			t.Errorf("%s exists = %v, want %v", filepath.Base(p), exists, survive)
		}
	}

	// A directory not made yet is nothing to prune.
	f.b.Dir = filepath.Join(dir, "missing")
	if n, err := f.b.Prune(ctx); err != nil || n != 0 {
		t.Errorf("prune of a missing dir = %d, %v; want 0, nil", n, err)
	}
}
