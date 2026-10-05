package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/media"
)

func TestProbeBatchPreservesOrder(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 50; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%02d.mkv", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	m := &Manager{Probe: func(p string) (media.Info, error) {
		// Deliberately uneven: a fast file must not overtake a slow one in
		// the results, or items get another file's codecs.
		if filepath.Base(p) == "f00.mkv" {
			time.Sleep(60 * time.Millisecond)
		}
		return media.Info{Container: filepath.Base(p)}, nil
	}}

	got := m.probeBatch(context.Background(), paths)
	if len(got) != len(paths) {
		t.Fatalf("got %d results, want %d", len(got), len(paths))
	}
	for i, r := range got {
		if r.Path != paths[i] {
			t.Fatalf("result %d is %s, want %s — results are out of order", i, r.Path, paths[i])
		}
		if r.Info.Container != filepath.Base(paths[i]) {
			t.Errorf("result %d carries another file's info: %q", i, r.Info.Container)
		}
		if r.SizeBytes != 1 {
			t.Errorf("result %d size = %d, want 1", i, r.SizeBytes)
		}
	}
}

func TestProbeBatchBoundsConcurrency(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 40; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%02d.mkv", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	var live, peak int64
	var mu sync.Mutex
	m := &Manager{Probe: func(string) (media.Info, error) {
		n := atomic.AddInt64(&live, 1)
		mu.Lock()
		if n > peak {
			peak = n
		}
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt64(&live, -1)
		return media.Info{}, nil
	}}
	m.probeBatch(context.Background(), paths)
	// Every file holds an open handle and an ffmpeg context while probing;
	// unbounded parallelism would exhaust descriptors on a large library.
	if peak > int64(probeWorkers()) {
		t.Errorf("peak concurrency %d exceeded the %d-worker limit", peak, probeWorkers())
	}
	if peak < 2 {
		t.Errorf("peak concurrency %d — the pool is not running in parallel", peak)
	}
}

func TestProbeBatchHonoursCancellation(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 40; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%02d.mkv", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var probes int64
	m := &Manager{Probe: func(string) (media.Info, error) {
		if atomic.AddInt64(&probes, 1) == 3 {
			cancel()
		}
		time.Sleep(2 * time.Millisecond)
		return media.Info{}, nil
	}}
	m.probeBatch(ctx, paths)
	// Cancelling mid-batch must stop the work, not merely be noticed after
	// every file has been read.
	if n := atomic.LoadInt64(&probes); n >= int64(len(paths)) {
		t.Errorf("probed %d of %d files after cancellation — cancel was ignored", n, len(paths))
	}
}

func TestProbeErrorsDoNotLoseTheFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.mkv")
	bad := filepath.Join(dir, "bad.mkv")
	for _, p := range []string{good, bad} {
		if err := os.WriteFile(p, []byte("xx"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := &Manager{Probe: func(p string) (media.Info, error) {
		if filepath.Base(p) == "bad.mkv" {
			return media.Info{}, errors.New("corrupt")
		}
		return media.Info{Container: "mkv"}, nil
	}}
	got := m.probeBatch(context.Background(), []string{good, bad})
	if got[1].Err == nil {
		t.Error("the failing probe lost its error")
	}
	// A file that cannot be probed is still a file: its size must survive so
	// the scan records it rather than dropping it from the library.
	if got[1].SizeBytes != 2 {
		t.Errorf("failed probe lost the file size: %d", got[1].SizeBytes)
	}
}
