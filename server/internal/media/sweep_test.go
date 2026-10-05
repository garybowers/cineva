package media

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mkSession builds a fake session directory of roughly size bytes with a
// given modification time.
func mkSession(t *testing.T, root, name string, size int, mod time.Time) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "seg0.m4s")
	if err := os.WriteFile(f, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, mod, mod); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSweepOrphans(t *testing.T) {
	root := t.TempDir()
	mkSession(t, root, "aaa", 1000, time.Now())
	mkSession(t, root, "bbb", 2000, time.Now())
	// Sibling caches that are not session directories must survive.
	for _, keep := range []string{"subtitles", "dlcache"} {
		if err := os.MkdirAll(filepath.Join(root, keep), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, keep, "x"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	n, freed, err := SweepOrphans(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 2 {
		t.Errorf("removed %d directories, want 2", n)
	}
	if freed != 3000 {
		t.Errorf("freed %d bytes, want 3000", freed)
	}
	for _, keep := range []string{"subtitles", "dlcache"} {
		if _, err := os.Stat(filepath.Join(root, keep, "x")); err != nil {
			t.Errorf("%s was swept away: %v", keep, err)
		}
	}
}

func TestSweepOrphansMissingRoot(t *testing.T) {
	// A server starting for the first time has no transcode dir yet; that
	// must not be an error.
	n, freed, err := SweepOrphans(filepath.Join(t.TempDir(), "nope"))
	if err != nil || n != 0 || freed != 0 {
		t.Fatalf("got (%d, %d, %v), want (0, 0, nil)", n, freed, err)
	}
}

func TestEnforceCap(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		maxBytes  int64
		live      map[string]bool
		wantGone  []string
		wantThere []string
	}{
		{
			name:      "under the cap evicts nothing",
			maxBytes:  10_000,
			wantThere: []string{"old", "mid", "new"},
		},
		{
			// 3 x 1000 bytes against a 2500 cap: evicting the oldest alone
			// brings it under, so the other two must survive.
			name:      "evicts least recently used first",
			maxBytes:  2_500,
			wantGone:  []string{"old"},
			wantThere: []string{"mid", "new"},
		},
		{
			name:      "keeps evicting until under the cap",
			maxBytes:  1_500,
			wantGone:  []string{"old", "mid"},
			wantThere: []string{"new"},
		},
		{
			name:      "a live session is never evicted",
			maxBytes:  1_500,
			live:      map[string]bool{"old": true},
			wantGone:  []string{"mid", "new"},
			wantThere: []string{"old"},
		},
		{
			name:      "zero disables the cap",
			maxBytes:  0,
			wantThere: []string{"old", "mid", "new"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mkSession(t, root, "old", 1000, now.Add(-3*time.Hour))
			mkSession(t, root, "mid", 1000, now.Add(-2*time.Hour))
			mkSession(t, root, "new", 1000, now.Add(-1*time.Hour))

			enforceCap(root, tc.maxBytes, tc.live)

			for _, name := range tc.wantGone {
				if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
					t.Errorf("%s should have been evicted", name)
				}
			}
			for _, name := range tc.wantThere {
				if _, err := os.Stat(filepath.Join(root, name)); err != nil {
					t.Errorf("%s should have survived: %v", name, err)
				}
			}
		})
	}
}
