package media

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// stallManager is a session manager whose transcoder is fake, with a short
// stall timeout so the watchdog can be watched in milliseconds.
func stallManager(t *testing.T, stallAfter time.Duration, run func(context.Context, HLSJob) error) *SessionManager {
	t.Helper()
	sm := NewSessionManager(t.TempDir(), 6)
	sm.StallAfter = stallAfter
	sm.run = run
	t.Cleanup(sm.Close)
	return sm
}

func startStallSession(t *testing.T, sm *SessionManager) *Session {
	t.Helper()
	s, err := sm.Start(StartOpts{ItemID: "item", Title: "Episode", InputPath: "/mnt/share/e1.mkv", RuntimeSecs: 600})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// blockedSource stands in for a transcoder whose read is stuck in the kernel
// on a share that has stopped answering: it opens, reads once, then blocks
// until released, ignoring cancellation as a blocked read does.
func blockedSource(release <-chan struct{}) func(context.Context, HLSJob) error {
	return func(_ context.Context, job HLSJob) error {
		job.OnInput()
		<-release
		return nil
	}
}

// A share that stops answering mid-transcode ends the session within the
// stall timeout, with a storage error rather than a wait that never ends;
// the storage side is told, and every later request fails at once.
func TestStalledSourceEndsSession(t *testing.T) {
	release := make(chan struct{})
	const stallAfter = 150 * time.Millisecond
	sm := stallManager(t, stallAfter, blockedSource(release))
	t.Cleanup(func() { close(release) }) // before the manager closes: cleanups run last first
	var stalled atomic.Int32
	var stuck atomic.Value
	sm.OnStall = func(s *Session, ch <-chan struct{}) {
		stalled.Add(1)
		stuck.Store(ch)
	}
	s := startStallSession(t, sm)

	start := time.Now()
	_, err := s.EnsureFile(context.Background(), "seg00000.m4s")
	if !errors.Is(err, ErrInputStalled) {
		t.Fatalf("EnsureFile err = %v, want ErrInputStalled", err)
	}
	if took := time.Since(start); took < stallAfter || took > stallAfter+time.Second {
		t.Errorf("stall reported after %v, want shortly after %v", took, stallAfter)
	}
	if n := stalled.Load(); n != 1 {
		t.Errorf("OnStall called %d times, want 1", n)
	}
	// The channel handed over is the blocked transcoder's: still open.
	if ch, _ := stuck.Load().(<-chan struct{}); ch == nil || isClosed(ch) {
		t.Error("OnStall should be handed the still-running transcoder's done channel")
	}

	// Every later request answers at once with the same reason.
	for _, name := range []string{"seg00001.m4s", "init.mp4"} {
		start := time.Now()
		if name == "init.mp4" {
			_, err = s.Init(context.Background())
		} else {
			_, err = s.EnsureFile(context.Background(), name)
		}
		if !errors.Is(err, ErrInputStalled) {
			t.Errorf("%s after the stall: err = %v, want ErrInputStalled", name, err)
		}
		if took := time.Since(start); took > 50*time.Millisecond {
			t.Errorf("%s after the stall took %v; it should not wait again", name, took)
		}
	}
	if !errors.Is(s.Err(), ErrInputStalled) {
		t.Errorf("Err() = %v, want ErrInputStalled", s.Err())
	}
	if n := stalled.Load(); n != 1 {
		t.Errorf("OnStall called %d times after more requests, want still 1", n)
	}

	// Stopping it does not wait on the blocked read.
	start = time.Now()
	sm.Stop(s.ID)
	if took := time.Since(start); took > time.Second {
		t.Errorf("stopping a stalled session took %v; it must not wait for the stuck read", took)
	}
}

// Nothing is judged while no one is waiting: a viewer who has paused is not
// waiting on the source, however long the pause.
func TestNoStallWithoutAWaitingRequest(t *testing.T) {
	release := make(chan struct{})
	sm := stallManager(t, 50*time.Millisecond, blockedSource(release))
	t.Cleanup(func() { close(release) }) // before the manager closes: cleanups run last first
	sm.OnStall = func(*Session, <-chan struct{}) { t.Error("OnStall called with no request waiting") }
	s := startStallSession(t, sm)
	time.Sleep(200 * time.Millisecond)
	if err := s.Err(); err != nil {
		t.Errorf("Err() = %v with no request waiting, want nil", err)
	}
}

func TestSlowButMovingIsNotAStall(t *testing.T) {
	tests := []struct {
		name       string
		stallAfter time.Duration
		run        func(context.Context, HLSJob) error
	}{
		{
			// A slow source — a busy share, a 4K decode — that keeps
			// reading is slow, not stalled, however long the segment takes.
			name:       "reading slowly",
			stallAfter: 100 * time.Millisecond,
			run: func(ctx context.Context, job HLSJob) error {
				for {
					job.OnInput()
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(20 * time.Millisecond):
					}
				}
			},
		},
		{
			// A transcoder far ahead of the viewer waits in its throttle by
			// choice; that counts as progress. The throttle looks every
			// 500 ms, so the timeout here has to be longer than that.
			name:       "throttled ahead",
			stallAfter: 800 * time.Millisecond,
			run: func(ctx context.Context, job HLSJob) error {
				job.OnInput()
				return job.Throttle(ctx, 1_000_000)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sm := stallManager(t, tc.stallAfter, tc.run)
			sm.OnStall = func(*Session, <-chan struct{}) { t.Error("OnStall called for a session that is moving") }
			s := startStallSession(t, sm)
			ctx, cancel := context.WithTimeout(context.Background(), 3*tc.stallAfter)
			defer cancel()
			_, err := s.EnsureFile(ctx, "seg00001.m4s")
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("EnsureFile err = %v, want the request's own deadline", err)
			}
			if err := s.Err(); err != nil {
				t.Errorf("Err() = %v, want nil", err)
			}
		})
	}
}

// A transcoder that stops with an error is reported to the waiting request
// at once, not after the full wait for a segment that will never come.
func TestFailedTranscodeReportedAtOnce(t *testing.T) {
	sm := stallManager(t, time.Minute, func(context.Context, HLSJob) error {
		return errors.New("hls open: Input/output error")
	})
	s := startStallSession(t, sm)
	start := time.Now()
	_, err := s.EnsureFile(context.Background(), "seg00000.m4s")
	if !errors.Is(err, ErrTranscodeFailed) {
		t.Fatalf("EnsureFile err = %v, want ErrTranscodeFailed", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %v to report a transcoder that had already failed", took)
	}
	// Not a stall, and not final: a seek starts a new transcode.
	if err := s.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
}
