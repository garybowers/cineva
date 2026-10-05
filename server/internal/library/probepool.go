package library

import (
	"context"
	"os"
	"runtime"
	"sync"

	"github.com/garybowers/cineva/server/internal/media"
)

// probed is one file's technical info, or the error from reading it.
type probed struct {
	Path      string
	SizeBytes int64
	Info      media.Info
	Err       error
}

// probeWorkers is how many files are inspected at once. Probing opens a file
// and reads its header — mostly waiting on disk — so more workers than cores
// still helps, but not without limit: each one holds an open file and an
// ffmpeg format context, and a spinning disk gets slower under heavy
// parallel seeking, not faster.
func probeWorkers() int {
	n := runtime.NumCPU()
	if n < 2 {
		return 2
	}
	if n > 8 {
		return 8
	}
	return n
}

// probeBatch inspects a batch of files in parallel and returns the results in
// the same order as the input.
//
// Batched rather than a continuous pipeline because the rest of the scan is
// deliberately serial: metadata lookups are rate-limited by the provider and
// SQLite takes one writer. Parallelising the one part that is neither — the
// per-file header read, which dominates a first scan of a large library —
// gets most of the win for a fraction of the complexity, and leaves the
// scan's ordering, progress reporting and cancellation exactly as they were.
func (m *Manager) probeBatch(ctx context.Context, paths []string) []probed {
	out := make([]probed, len(paths))
	sem := make(chan struct{}, probeWorkers())
	var wg sync.WaitGroup
	for i, p := range paths {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// Cancellation is checked again here: a batch queued before a
			// cancel should not keep probing after it.
			if ctx.Err() != nil {
				out[i] = probed{Path: p, Err: ctx.Err()}
				return
			}
			r := probed{Path: p}
			if st, err := os.Stat(p); err == nil {
				r.SizeBytes = st.Size()
			}
			r.Info, r.Err = m.Probe(p)
			out[i] = r
		}(i, p)
	}
	wg.Wait()
	return out
}

// probeBatchSize is how many files are probed before the scan goes back to
// its serial work. Large enough to keep every worker busy, small enough that
// results are applied — and progress reported — while the scan runs rather
// than in one jump at the end.
const probeBatchSize = 32
