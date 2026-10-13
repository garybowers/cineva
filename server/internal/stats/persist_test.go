package stats

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory stand-in: these tests are about the sampler's
// use of a store, not about SQL.
type fakeStore struct {
	mu      sync.Mutex
	rows    []StoredSample
	pruned  []time.Time
	failApp bool
}

func (f *fakeStore) AppendStatsSample(_ context.Context, s StoredSample) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failApp {
		return context.DeadlineExceeded
	}
	f.rows = append(f.rows, s)
	return nil
}

func (f *fakeStore) StatsSamplesSince(_ context.Context, since time.Time) ([]StoredSample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []StoredSample{}
	for _, r := range f.rows {
		if !r.At.Before(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) PruneStatsSamples(_ context.Context, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruned = append(f.pruned, before)
	return 0, nil
}

// The whole point of persisting: a restart must not empty the charts.
func TestRestoreSeedsCoarseRing(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Minute)
	fs := &fakeStore{rows: []StoredSample{
		{At: base.Add(-3 * time.Minute), CPUPct: 11, NumCPU: 4},
		{At: base.Add(-2 * time.Minute), CPUPct: 22, NumCPU: 4},
		{At: base.Add(-1 * time.Minute), CPUPct: 33, NumCPU: 4},
	}}
	s := New(time.Second)
	s.Store = fs
	s.restore(context.Background())

	day := s.History(true)
	if len(day) != 3 {
		t.Fatalf("restored %d coarse samples, want 3", len(day))
	}
	if day[0].CPUPct != 11 || day[2].CPUPct != 33 {
		t.Errorf("restored out of order: %v", []float64{day[0].CPUPct, day[2].CPUPct})
	}
	// The fine ring covers the last hour at sampling resolution and refills
	// by itself; minute-spaced rows must not be passed off as fine samples.
	if hour := s.History(false); len(hour) != 0 {
		t.Errorf("restore filled the fine ring with %d samples; it should stay empty", len(hour))
	}
	// Resuming must not immediately re-emit the newest restored minute.
	if !s.lastMinute.Equal(base.Add(-time.Minute)) {
		t.Errorf("lastMinute = %v, want the newest restored sample %v", s.lastMinute, base.Add(-time.Minute))
	}
}

// Samples older than the retention window are not restored.
func TestRestoreHonoursRetention(t *testing.T) {
	now := time.Now().UTC()
	fs := &fakeStore{rows: []StoredSample{
		{At: now.Add(-48 * time.Hour), CPUPct: 1},
		{At: now.Add(-30 * time.Minute), CPUPct: 2},
	}}
	s := New(time.Second)
	s.Store, s.Retain = fs, time.Hour
	s.restore(context.Background())
	got := s.History(true)
	if len(got) != 1 || got[0].CPUPct != 2 {
		t.Fatalf("restored %d samples %v, want only the recent one", len(got), got)
	}
}

// A store that cannot be written must not stop sampling: losing a minute of
// chart is not worth losing the dashboard.
func TestAppendFailureDoesNotStopSampling(t *testing.T) {
	s := New(time.Millisecond)
	s.Store = &fakeStore{failApp: true}
	now := time.Now().UTC()
	s.take(now)
	s.take(now.Add(2 * time.Minute)) // forces a coarse tick, which fails to persist
	if n := len(s.History(false)); n != 2 {
		t.Fatalf("fine ring has %d samples, want 2 — sampling stopped on a write error", n)
	}
	if n := len(s.History(true)); n == 0 {
		t.Fatal("coarse ring empty — the in-memory ring should fill even when the store fails")
	}
}

// A nil Store is the old behaviour, which `scan` and the tests rely on.
func TestNilStoreIsInMemoryOnly(t *testing.T) {
	s := New(time.Millisecond)
	s.restore(context.Background()) // must not panic
	s.take(time.Now().UTC())
	if n := len(s.History(false)); n != 1 {
		t.Fatalf("fine ring has %d samples, want 1", n)
	}
}
