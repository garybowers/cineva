package background

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduleAllows(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 13, h, m, 0, 0, time.Local) }
	overnight := Schedule{Mode: ModeWindow, Start: 2 * 60, End: 6 * 60}
	lateNight := Schedule{Mode: ModeWindow, Start: 22 * 60, End: 6 * 60}
	for _, tc := range []struct {
		name string
		sc   Schedule
		now  time.Time
		want bool
	}{
		{"inside the window", overnight, at(3, 0), true},
		{"window opens", overnight, at(2, 0), true},
		{"window has closed", overnight, at(6, 0), false},
		{"daytime", overnight, at(14, 30), false},
		{"past midnight, before it", lateNight, at(23, 15), true},
		{"past midnight, after it", lateNight, at(4, 0), true},
		{"past midnight, daytime", lateNight, at(12, 0), false},
		{"start equals end: all day", Schedule{Mode: ModeWindow, Start: 60, End: 60}, at(12, 0), true},
		{"idle: any time", Schedule{Mode: ModeIdle}, at(12, 0), true},
		{"manual: never by itself", Schedule{Mode: ModeManual}, at(3, 0), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sc.Allows(tc.now); got != tc.want {
				t.Fatalf("allows = %v, want %v", got, tc.want)
			}
		})
	}
}

// reasons records what a gate reports, in order.
type reasons struct {
	mu  sync.Mutex
	got []string
}

func (r *reasons) add(s string) { r.mu.Lock(); r.got = append(r.got, s); r.mu.Unlock() }
func (r *reasons) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.got) == 0 {
		return "\x00"
	}
	return r.got[len(r.got)-1]
}

// The gate holds for each thing that should hold it, says why, and opens
// once that has gone.
func TestGateWait(t *testing.T) {
	noon := func() time.Time { return time.Date(2026, 10, 13, 12, 0, 0, 0, time.Local) }
	for _, tc := range []struct {
		name   string
		setup  func(*Gate, *atomic.Value)
		reason string
		lift   func(*Gate, *atomic.Value)
	}{
		{"paused by an admin", func(g *Gate, _ *atomic.Value) { g.Policy.SetPaused(true) }, "manual",
			func(g *Gate, _ *atomic.Value) { g.Policy.SetPaused(false) }},
		{"outside the window", func(g *Gate, _ *atomic.Value) {
			g.Policy.SetSchedule(Schedule{Mode: ModeWindow, Start: 2 * 60, End: 6 * 60})
		}, "schedule", func(g *Gate, _ *atomic.Value) { g.RunNow() }},
		{"on demand", func(g *Gate, _ *atomic.Value) { g.Policy.SetSchedule(Schedule{Mode: ModeManual}) }, "on-demand",
			func(g *Gate, _ *atomic.Value) { g.RunNow() }},
		{"playing", func(_ *Gate, b *atomic.Value) { b.Store("playing") }, "playing",
			func(_ *Gate, b *atomic.Value) { b.Store("") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGate(NewPolicy())
			g.Now, g.Poll, g.Cooldown = noon, 10*time.Millisecond, 0
			var busy atomic.Value
			busy.Store("")
			g.Busy = func() string { return busy.Load().(string) }
			tc.setup(g, &busy)

			var r reasons
			done := make(chan error, 1)
			go func() { done <- g.Wait(context.Background(), nil, r.add) }()
			select {
			case err := <-done:
				t.Fatalf("gate opened (%v) while it should hold", err)
			case <-time.After(100 * time.Millisecond):
			}
			if got := r.last(); got != tc.reason {
				t.Fatalf("reported %q, want %q", got, tc.reason)
			}
			tc.lift(g, &busy)
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("gate = %v, want it open", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("gate never opened")
			}
			if got := r.last(); got != "" {
				t.Fatalf("last report %q, want \"\" once open", got)
			}
		})
	}
}

// After something busy stops, the gate waits out the cooldown before it
// opens, still giving the busy reason.
func TestGateCooldown(t *testing.T) {
	g := NewGate(NewPolicy())
	g.Poll, g.Cooldown = 10*time.Millisecond, 300*time.Millisecond
	var busy atomic.Value
	busy.Store("scanning")
	g.Busy = func() string { return busy.Load().(string) }
	var r reasons
	done := make(chan time.Time, 1)
	go func() {
		if err := g.Wait(context.Background(), nil, r.add); err == nil {
			done <- time.Now()
		}
	}()
	time.Sleep(50 * time.Millisecond)
	quiet := time.Now()
	busy.Store("")
	time.Sleep(100 * time.Millisecond)
	if got := r.last(); got != "scanning" {
		t.Fatalf("during the cooldown reported %q, want scanning", got)
	}
	select {
	case at := <-done:
		if waited := at.Sub(quiet); waited < 250*time.Millisecond {
			t.Errorf("opened %v after the scan stopped; want the cooldown first", waited)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("never opened")
	}
}

// Storage gone ends the wait at once, and so does the context.
func TestGateStorageAndCancel(t *testing.T) {
	g := NewGate(NewPolicy())
	g.Poll = 10 * time.Millisecond
	if err := g.Wait(context.Background(), func() bool { return false }, nil); !errors.Is(err, ErrStorageGone) {
		t.Fatalf("storage gone: %v, want ErrStorageGone", err)
	}
	g.Policy.SetPaused(true)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	if err := g.Wait(ctx, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v, want context.Canceled", err)
	}
}

func TestPolicyPace(t *testing.T) {
	p := NewPolicy()
	for _, tc := range []struct {
		name  string
		limit int64
		read  int64
		took  time.Duration
		want  time.Duration
	}{
		{"no limit", 0, 1_000_000, 0, 0},
		{"10 MB/s, 1 MB", 10_000_000, 1_000_000, 0, 100 * time.Millisecond},
		{"already slow enough", 10_000_000, 1_000_000, 200 * time.Millisecond, 0},
		{"part of the time already spent", 10_000_000, 1_000_000, 60 * time.Millisecond, 40 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.SetRateLimit(tc.limit)
			start := time.Now()
			p.Pace(context.Background(), tc.read, tc.took)
			if got := time.Since(start); got < tc.want || got > tc.want+80*time.Millisecond {
				t.Fatalf("paced %v, want about %v", got, tc.want)
			}
		})
	}
}
