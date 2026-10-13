package library

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/store"
)

// A rescan waits while something plays: until it stops, or until the
// ceiling, whichever comes first.
func TestWaitForPlayback(t *testing.T) {
	const poll, ceiling = 10 * time.Millisecond, 200 * time.Millisecond
	for _, tc := range []struct {
		name      string
		stopAfter time.Duration // playback stops this long in; 0 = nothing playing; -1 = never
		cancel    bool
		minWait   time.Duration
		maxWait   time.Duration
		timedOut  bool
		wantErr   error
	}{
		{name: "nothing playing", stopAfter: 0, maxWait: 5 * time.Millisecond},
		{name: "playback stops", stopAfter: 60 * time.Millisecond, minWait: 50 * time.Millisecond, maxWait: 150 * time.Millisecond},
		{name: "playback outlasts the ceiling", stopAfter: -1, minWait: ceiling, maxWait: ceiling + 100*time.Millisecond, timedOut: true},
		{name: "server stopping", stopAfter: -1, cancel: true, maxWait: 100 * time.Millisecond, wantErr: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			playing := func() bool {
				switch {
				case tc.stopAfter == 0:
					return false
				case tc.stopAfter < 0:
					return true
				}
				return time.Since(start) < tc.stopAfter
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				go func() { time.Sleep(30 * time.Millisecond); cancel() }()
			}
			waited, timedOut, err := waitForPlayback(ctx, playing, poll, ceiling)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if timedOut != tc.timedOut {
				t.Errorf("timed out = %v, want %v", timedOut, tc.timedOut)
			}
			if waited < tc.minWait || waited > tc.maxWait {
				t.Errorf("waited %v, want between %v and %v", waited, tc.minWait, tc.maxWait)
			}
		})
	}
}

// A due scheduled rescan holds while something plays and goes ahead once
// it stops.
func TestSchedulerYieldsToPlayback(t *testing.T) {
	var playing atomic.Bool
	playing.Store(true)
	s := &Scheduler{Playing: playing.Load, poll: 10 * time.Millisecond, ceiling: time.Minute}
	done := make(chan error, 1)
	go func() { done <- s.yield(context.Background()) }()
	select {
	case <-done:
		t.Fatal("rescan went ahead while something was playing")
	case <-time.After(80 * time.Millisecond):
	}
	playing.Store(false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("rescan never went ahead after playback stopped")
	}
}

// A webhook's rescan waits for playback, repeated requests while it waits
// collapse into one scan, and a scan an admin starts runs regardless.
func TestRequestScanWaitsForPlayback(t *testing.T) {
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, nil, t.TempDir())
	m.poll, m.ceiling = 10*time.Millisecond, time.Minute
	var playing atomic.Bool
	playing.Store(true)
	m.Playing = playing.Load

	scans := func() int {
		t.Helper()
		list, err := st.Scans(ctx, 50)
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	waitIdle := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for m.AnyScanning() {
			if time.Now().After(deadline) {
				t.Fatal("scan never finished")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	for range 3 {
		if err := m.RequestScan(ctx, lib); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(80 * time.Millisecond)
	if n := scans(); n != 0 {
		t.Fatalf("%d scans while playing, want none yet", n)
	}

	playing.Store(false)
	deadline := time.Now().Add(5 * time.Second)
	for scans() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("requested rescan never started after playback stopped")
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitIdle()
	time.Sleep(50 * time.Millisecond)
	if n := scans(); n != 1 {
		t.Fatalf("%d scans, want the three requests collapsed into one", n)
	}

	// By hand: straight away, playing or not.
	playing.Store(true)
	if _, err := m.StartScan(ctx, lib, false); err != nil {
		t.Fatal(err)
	}
	waitIdle()
	if n := scans(); n != 2 {
		t.Fatalf("%d scans, want the manual one to have run while playing", n)
	}
}

// A webhook's rescan stops waiting at its ceiling.
func TestRequestScanCeiling(t *testing.T) {
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, nil, t.TempDir())
	m.poll, m.ceiling = 10*time.Millisecond, 100*time.Millisecond
	m.Playing = func() bool { return true }
	if err := m.RequestScan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		list, err := st.Scans(ctx, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rescan never ran despite the ceiling")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for m.AnyScanning() {
		time.Sleep(5 * time.Millisecond)
	}
}
