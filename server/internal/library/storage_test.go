package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/store"
)

func TestWithin(t *testing.T) {
	tests := []struct {
		name    string
		delay   time.Duration
		wantErr error
	}{
		{"answers in time", 0, nil},
		{"hangs", 200 * time.Millisecond, ErrNotResponding},
	}
	for _, tc := range tests {
		start := time.Now()
		v, err := Within(50*time.Millisecond, func() (int, error) {
			time.Sleep(tc.delay)
			return 7, nil
		})
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
		}
		if tc.wantErr == nil && v != 7 {
			t.Errorf("%s: value = %d, want 7", tc.name, v)
		}
		if time.Since(start) > 150*time.Millisecond {
			t.Errorf("%s: took %v, the deadline is 50ms", tc.name, time.Since(start))
		}
	}
}

// A library whose folder goes missing is reported unreachable, once, with
// which folder and why, and reachable again when it returns.
func TestStorageMonitor(t *testing.T) {
	dir := t.TempDir()
	lib := store.Library{ID: "lib1", Name: "TV", Paths: []string{dir}}
	m := NewStorageMonitor(nil)
	var changes []StorageStatus
	m.OnChange = func(_ store.Library, st StorageStatus) { changes = append(changes, st) }

	if st := m.Status(lib.ID); !st.Available {
		t.Fatalf("unchecked library = %+v, want available", st)
	}
	if st := m.Check(lib); !st.Available || len(changes) != 0 {
		t.Fatalf("healthy check = %+v with %d change(s); want available, no news", st, len(changes))
	}

	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	st := m.Check(lib)
	if st.Available || st.Path != dir || st.Error == "" || st.Since.IsZero() {
		t.Fatalf("missing folder = %+v, want unavailable naming the folder", st)
	}
	m.Check(lib) // still missing: no second announcement
	if len(changes) != 1 {
		t.Fatalf("%d change(s) after two failed checks, want 1", len(changes))
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if st := m.Check(lib); !st.Available || len(changes) != 2 {
		t.Fatalf("restored = %+v with %d change(s), want available and announced", st, len(changes))
	}

	// A file where the folder should be is not a library root.
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if st := m.Check(store.Library{ID: "lib2", Paths: []string{file}}); st.Available {
		t.Fatalf("a file as library root = %+v, want unavailable", st)
	}
}
