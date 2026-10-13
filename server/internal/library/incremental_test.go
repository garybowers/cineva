package library

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// countingVideo counts series lookups, which is what an incremental scan
// saves on and what a running series still costs.
type countingVideo struct {
	stubVideo
	searches *int
}

func (c countingVideo) SearchTV(ctx context.Context, title string, year int) (metadata.VideoResult, error) {
	*c.searches++
	return c.stubVideo.SearchTV(ctx, title, year)
}

// A rescan reads only the files that changed, keeps the rest (and the
// series and season above them) without touching them, and still removes
// what has gone from disk. A full scan reads everything again.
func TestIncrementalScan(t *testing.T) {
	tests := []struct {
		name   string
		status string
		// Series lookups on a rescan with nothing changed: none for an
		// ended series, one for a series still on air, whose next episode
		// date moves without any file changing.
		idleSearches int
	}{
		{"ended series", "Ended", 0},
		{"running series", "Returning Series", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "Show", "Season 1")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			ep1 := filepath.Join(dir, "Show.S01E01.mkv")
			ep2 := filepath.Join(dir, "Show.S01E02.mkv")
			for _, p := range []string{ep1, ep2} {
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			st := newLibTestStore(t)
			ctx := context.Background()
			lib, err := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{root})
			if err != nil {
				t.Fatal(err)
			}
			// Probes run on the scanner's worker pool, so the count is atomic.
			var probes atomic.Int32
			searches := 0
			m := NewManager(st, countingVideo{
				stubVideo: stubVideo{tv: metadata.VideoResult{TmdbID: 1, Title: "Show", Status: tc.status}},
				searches:  &searches,
			}, "")
			m.Probe = func(string) (media.Info, error) {
				probes.Add(1)
				return media.Info{RuntimeSecs: 1800}, nil
			}

			scan := func(full bool) {
				t.Helper()
				probes.Store(0)
				searches = 0
				res, err := m.RunScanSync(ctx, lib, full)
				if err != nil || res.Status != "completed" {
					t.Fatalf("scan: %v %+v", err, res)
				}
			}
			count := func() int {
				t.Helper()
				items, err := st.ItemsByLibrary(ctx, lib.ID)
				if err != nil {
					t.Fatal(err)
				}
				return len(items)
			}

			scan(false)
			if probes.Load() != 2 || count() != 4 {
				t.Fatalf("first scan: %d probes, %d items; want 2 probes, 4 items", probes.Load(), count())
			}

			scan(false)
			if probes.Load() != 0 || searches != tc.idleSearches || count() != 4 {
				t.Fatalf("unchanged rescan: %d probes, %d searches, %d items; want 0, %d, 4",
					probes.Load(), searches, count(), tc.idleSearches)
			}

			// A changed file is read again, and only that one. The mtime is
			// set explicitly: a rewrite within the same second would
			// otherwise leave it equal.
			if err := os.WriteFile(ep1, []byte("xx"), 0o644); err != nil {
				t.Fatal(err)
			}
			later := time.Now().Add(time.Minute)
			if err := os.Chtimes(ep1, later, later); err != nil {
				t.Fatal(err)
			}
			scan(false)
			if probes.Load() != 1 || count() != 4 {
				t.Fatalf("one file changed: %d probes, %d items; want 1, 4", probes.Load(), count())
			}

			if err := os.Remove(ep2); err != nil {
				t.Fatal(err)
			}
			scan(false)
			if probes.Load() != 0 || count() != 3 {
				t.Fatalf("one file removed: %d probes, %d items; want 0, 3", probes.Load(), count())
			}

			scan(true)
			if probes.Load() != 1 || count() != 3 {
				t.Fatalf("full scan: %d probes, %d items; want 1, 3", probes.Load(), count())
			}
		})
	}
}
