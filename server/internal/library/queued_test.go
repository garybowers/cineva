package library

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

// A full rescan queued for a quiet time waits while Quiet gives a reason,
// starts (in full) once it gives none, and leaves the queue as it starts.
// The queue outlives the process, and a library deleted meanwhile is
// dropped from it rather than retried for ever.
func TestQueuedFullScan(t *testing.T) {
	root := t.TempDir()
	writeAt(t, filepath.Join(root, "Film", "Film.mkv"), time.Now().Add(-time.Hour))
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := st.CreateLibrary(ctx, "Gone", store.MediaMovies, []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	newManager := func() *Manager {
		m := NewManager(st, nil, "")
		m.Probe = func(string) (media.Info, error) { return media.Info{RuntimeSecs: 5400}, nil }
		return m
	}
	m := newManager()
	scanOnce(t, m, lib, false) // the file is now unchanged: only a full scan reads it

	if err := m.QueueFullScan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	if err := m.QueueFullScan(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLibrary(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}

	// A new process finds the queue where the old one left it.
	m = newManager()
	reason := "schedule"
	m.Quiet = func() string { return reason }
	if err := m.loadQueued(ctx); err != nil {
		t.Fatal(err)
	}
	if !m.FullScanQueued(lib.ID) || !m.FullScanQueued(gone.ID) {
		t.Fatal("queue not restored from the saved setting")
	}

	m.startQueued(ctx)
	if !m.FullScanQueued(lib.ID) || m.AnyScanning() {
		t.Fatal("started outside a quiet time")
	}

	reason = ""
	// The deleted library sorts first or last by id; step until both have
	// left the queue, which takes one step each.
	for range 2 {
		m.startQueued(ctx)
		waitScans(t, m)
	}
	if m.FullScanQueued(lib.ID) || m.FullScanQueued(gone.ID) {
		t.Fatal("queue not emptied")
	}
	scans, err := st.Scans(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scans) != 2 || scans[0].Status != "completed" || scans[0].FilesSeen != 1 || scans[0].ItemsCreated != 1 {
		t.Errorf("scans %+v, want the queued one completed, rereading the unchanged file", scans)
	}
	saved, err := st.Setting(ctx, settingQueuedFull, "missing")
	if err != nil || saved != "" {
		t.Errorf("saved queue %q (%v), want empty", saved, err)
	}

	// Cancelling takes it off again.
	if err := m.QueueFullScan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelQueuedFullScan(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	if m.FullScanQueued(lib.ID) {
		t.Error("still queued after cancelling")
	}
}

// waitScans waits for the manager's scans to finish.
func waitScans(t *testing.T, m *Manager) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for m.AnyScanning() {
		if time.Now().After(deadline) {
			t.Fatal("scan did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
