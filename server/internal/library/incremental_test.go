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

// Editing, adding or removing a sidecar a scanner reads — an .nfo, local
// artwork, an external subtitle — rereads the video beside it on an
// incremental scan, though the video itself has not changed. Files no
// scanner reads, and another film's sidecars, do not.
func TestIncrementalScanSidecars(t *testing.T) {
	type layout struct {
		media store.MediaType
		video string // relative to the library root
		files []string
	}
	movie := layout{store.MediaMovies, "Film (2001)/Film (2001).mkv",
		[]string{"Film (2001)/poster.jpg", "Film (2001)/Film (2001).en.srt"}}
	episode := layout{store.MediaTV, "Show/Season 1/Show.S01E01.mkv",
		[]string{"Show/Season 1/Show.S01E01.en.srt", "Show/Season 1/Show.S01E01-thumb.jpg"}}
	tests := []struct {
		name   string
		layout layout
		change func(t *testing.T, dir string)
		probes int32
	}{
		{"nfo added", movie, write("Film (2001)/Film (2001).nfo"), 1},
		{"folder nfo added", movie, write("Film (2001)/movie.nfo"), 1},
		{"poster edited", movie, write("Film (2001)/poster.jpg"), 1},
		{"named fanart added", movie, write("Film (2001)/Film (2001)-fanart.png"), 1},
		{"subtitle removed", movie, remove("Film (2001)/Film (2001).en.srt"), 1},
		{"unrelated file", movie, write("Film (2001)/notes.txt"), 0},
		{"another film's nfo", movie, write("Film (2001)/Other.nfo"), 0},
		{"nothing changed", movie, func(*testing.T, string) {}, 0},
		{"episode subtitle added", episode, write("Show/Season 1/Show.S01E01.fr.srt"), 1},
		{"episode subtitle edited", episode, write("Show/Season 1/Show.S01E01.en.srt"), 1},
		{"episode thumbnail edited", episode, write("Show/Season 1/Show.S01E01-thumb.jpg"), 0},
		{"episode nfo added", episode, write("Show/Season 1/Show.S01E01.nfo"), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, rel := range append([]string{tc.layout.video}, tc.layout.files...) {
				writeAt(t, filepath.Join(root, rel), time.Now().Add(-time.Hour))
			}
			m, lib, probes := sidecarScanner(t, root, tc.layout.media)
			scanOnce(t, m, lib, false)
			probes.Store(0)
			tc.change(t, root)
			scanOnce(t, m, lib, false)
			if got := probes.Load(); got != tc.probes {
				t.Errorf("rescan probed %d files, want %d", got, tc.probes)
			}
			// And the new fingerprint was stored: a second rescan is idle.
			probes.Store(0)
			scanOnce(t, m, lib, false)
			if got := probes.Load(); got != 0 {
				t.Errorf("second rescan probed %d files, want 0", got)
			}
		})
	}
}

// The first scan after migration 0032 finds no fingerprint stored. It
// records the current one rather than rereading every video with sidecars.
func TestIncrementalScanAdoptsSidecarStamp(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "Film", "Film.mkv")
	writeAt(t, video, time.Now().Add(-time.Hour))
	writeAt(t, filepath.Join(root, "Film", "Film.nfo"), time.Now().Add(-time.Hour))
	m, lib, probes := sidecarScanner(t, root, store.MediaMovies)
	scanOnce(t, m, lib, false)

	ctx := context.Background()
	stamps, err := m.Store.FileStamps(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := stamps[video].SidecarStamp
	if want == 0 {
		t.Fatal("first scan stored no sidecar fingerprint")
	}
	if err := m.Store.SetSidecarStamps(ctx, map[string]int64{stamps[video].ID: 0}); err != nil {
		t.Fatal(err)
	}
	probes.Store(0)
	scanOnce(t, m, lib, false)
	if probes.Load() != 0 {
		t.Errorf("rescan probed %d files, want 0: a missing fingerprint is adopted, not a change", probes.Load())
	}
	stamps, err = m.Store.FileStamps(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := stamps[video].SidecarStamp; got != want {
		t.Errorf("adopted fingerprint %d, want %d", got, want)
	}
}

func sidecarScanner(t *testing.T, root string, kind store.MediaType) (*Manager, store.Library, *atomic.Int32) {
	t.Helper()
	st := newLibTestStore(t)
	lib, err := st.CreateLibrary(context.Background(), "Lib", kind, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	m := NewManager(st, nil, "")
	m.Probe = func(string) (media.Info, error) {
		probes.Add(1)
		return media.Info{RuntimeSecs: 1800}, nil
	}
	return m, lib, &probes
}

func scanOnce(t *testing.T, m *Manager, lib store.Library, full bool) {
	t.Helper()
	res, err := m.RunScanSync(context.Background(), lib, full)
	if err != nil || res.Status != "completed" {
		t.Fatalf("scan: %v %+v", err, res)
	}
}

// writeAt writes a small file and sets its mtime, so a rewrite within the
// same instant still reads as a change.
func writeAt(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func write(rel string) func(*testing.T, string) {
	return func(t *testing.T, root string) { writeAt(t, filepath.Join(root, rel), time.Now()) }
}

func remove(rel string) func(*testing.T, string) {
	return func(t *testing.T, root string) {
		if err := os.Remove(filepath.Join(root, rel)); err != nil {
			t.Fatal(err)
		}
	}
}

// A scan reports the titles it added, for notifications: nothing on a
// library's first scan, only the new episode on a later one, and nothing
// on a full rescan of what was already there.
func TestScanReportsNewTitles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Show", "Season 1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Show.S01E01.mkv")
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, stubVideo{tv: metadata.VideoResult{TmdbID: 1, Title: "Show", Status: "Ended"}}, "")
	m.Probe = func(string) (media.Info, error) { return media.Info{RuntimeSecs: 1800}, nil }
	var added []store.Item
	var scans int
	m.OnScanFinished = func(_ store.Library, sc store.Scan, a []store.Item) {
		scans++
		added = a
		if sc.Status != "completed" {
			t.Errorf("scan %s: %s", sc.Status, sc.Error)
		}
	}
	scan := func(full bool) []store.Item {
		t.Helper()
		if _, err := m.RunScanSync(ctx, lib, full); err != nil {
			t.Fatal(err)
		}
		return added
	}
	if got := scan(false); len(got) != 0 {
		t.Fatalf("first scan announced %d titles", len(got))
	}
	write("Show.S01E02.mkv")
	got := scan(false)
	if len(got) != 1 || got[0].Kind != store.KindEpisode || got[0].EpisodeNum != 2 {
		t.Fatalf("second scan added %+v, want episode 2 alone", got)
	}
	if got := scan(true); len(got) != 0 {
		t.Fatalf("a full rescan announced %d titles", len(got))
	}
	if scans != 3 {
		t.Fatalf("OnScanFinished called %d times, want 3", scans)
	}
}
