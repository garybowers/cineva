package library

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/store"
)

// A full rescan rereads and rematches every file: hours of reading on a
// large library, from the share playback streams from. Rather than start
// one in the evening, an admin can queue it for a quiet time — the
// background work window, once nothing is playing — and the queue survives
// a restart, since "tonight" may well come after one.

// settingQueuedFull holds the queued libraries' ids, comma separated.
const settingQueuedFull = "scan.queued_full"

// queuedPoll is how often the queue looks for a quiet moment.
const queuedPoll = 30 * time.Second

// QueueFullScan queues a full rescan of the library for the next quiet
// time. Queuing one already queued is not an error.
func (m *Manager) QueueFullScan(ctx context.Context, lib store.Library) error {
	m.mu.Lock()
	if m.queued == nil {
		m.queued = map[string]bool{}
	}
	m.queued[lib.ID] = true
	err := m.saveQueuedLocked(ctx)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	libraryLog.Info("full rescan queued for a quiet time", "library", lib.Name)
	m.Events.Publish(events.Event{Type: "libraries"})
	return nil
}

// CancelQueuedFullScan takes the library off the queue, if it is on it.
func (m *Manager) CancelQueuedFullScan(ctx context.Context, libraryID string) error {
	m.mu.Lock()
	if !m.queued[libraryID] {
		m.mu.Unlock()
		return nil
	}
	delete(m.queued, libraryID)
	err := m.saveQueuedLocked(ctx)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.Events.Publish(events.Event{Type: "libraries"})
	return nil
}

// FullScanQueued reports whether a full rescan of the library is waiting
// for a quiet time.
func (m *Manager) FullScanQueued(libraryID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.queued[libraryID]
}

func (m *Manager) saveQueuedLocked(ctx context.Context) error {
	ids := make([]string, 0, len(m.queued))
	for id := range m.queued {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if err := m.Store.SetSetting(context.WithoutCancel(ctx), settingQueuedFull, strings.Join(ids, ",")); err != nil {
		return fmt.Errorf("save queued rescans: %w", err)
	}
	return nil
}

// RunQueuedScans starts queued full rescans when Quiet says the time is
// right, one library at a time, until ctx ends. The queue saved before a
// restart is picked up first.
func (m *Manager) RunQueuedScans(ctx context.Context) {
	if err := m.loadQueued(ctx); err != nil {
		libraryLog.Warn("reading queued rescans failed", "err", err)
	}
	t := time.NewTicker(queuedPoll)
	defer t.Stop()
	for {
		m.startQueued(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// loadQueued adds the queue saved by an earlier process to this one's.
func (m *Manager) loadQueued(ctx context.Context) error {
	saved, err := m.Store.Setting(ctx, settingQueuedFull, "")
	if err != nil {
		return fmt.Errorf("load queued rescans: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.queued == nil {
		m.queued = map[string]bool{}
	}
	for _, id := range strings.Split(saved, ",") {
		if id != "" {
			m.queued[id] = true
		}
	}
	return nil
}

// startQueued starts the next queued full rescan, if it is quiet and no
// other scan is running. A library deleted since it was queued is dropped.
func (m *Manager) startQueued(ctx context.Context) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.queued))
	for id := range m.queued {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	if len(ids) == 0 || ctx.Err() != nil || m.AnyScanning() {
		return
	}
	if m.Quiet != nil {
		if reason := m.Quiet(); reason != "" {
			return
		}
	}
	slices.Sort(ids)
	id := ids[0]
	libs, err := m.Store.Libraries(ctx)
	if err != nil {
		libraryLog.Warn("queued full rescan: listing libraries failed", "err", err)
		return
	}
	i := slices.IndexFunc(libs, func(l store.Library) bool { return l.ID == id })
	switch {
	case i < 0:
		libraryLog.Info("queued full rescan dropped: library gone", "library_id", id)
	default:
		lib := libs[i]
		if _, err := m.StartScan(ctx, lib, true); err != nil {
			// Another process holds the scan lock; try again next time.
			libraryLog.Debug("queued full rescan not started", "library", lib.Name, "err", err)
			return
		}
		libraryLog.Info("queued full rescan started", "library", lib.Name)
	}
	if err := m.CancelQueuedFullScan(ctx, id); err != nil {
		libraryLog.Warn("updating queued rescans failed", "err", err)
	}
}
