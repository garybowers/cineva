package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/media"
)

// Measures the parallel probe against the serial path on real media. Opt-in
// via CINEVA_TEST_LIBRARY, and read-only — it probes headers and writes
// nothing.
func TestProbeSpeedup(t *testing.T) {
	// Pointed at a real library with CINEVA_TEST_LIBRARY — synthetic files
	// would not exercise the header reads this is measuring.
	root := os.Getenv("CINEVA_TEST_LIBRARY")
	if root == "" {
		t.Skip("CINEVA_TEST_LIBRARY not set")
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("CINEVA_TEST_LIBRARY %q is not readable", root)
	}
	var files []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !IsVideoFile(p) {
			return nil //nolint:nilerr
		}
		files = append(files, p)
		if len(files) >= 24 {
			return filepath.SkipAll
		}
		return nil
	})
	if len(files) < 4 {
		t.Skipf("only %d files found", len(files))
	}
	m := &Manager{Probe: media.Probe}

	start := time.Now()
	for _, p := range files {
		_, _ = m.Probe(p)
	}
	serial := time.Since(start)

	start = time.Now()
	got := m.probeBatch(context.Background(), files)
	parallel := time.Since(start)

	if len(got) != len(files) {
		t.Fatalf("got %d results for %d files", len(got), len(files))
	}
	t.Logf("%d files · serial %v · parallel %v (%d workers) · %.1fx",
		len(files), serial.Round(time.Millisecond), parallel.Round(time.Millisecond),
		probeWorkers(), float64(serial)/float64(parallel))
}
