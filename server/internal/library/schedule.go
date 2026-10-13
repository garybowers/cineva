package library

import (
	"context"
	"time"

	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/store"
)

var (
	libraryLog      = logging.For("library")
	housekeepingLog = logging.For("housekeeping")
)

// Scheduler rescans libraries on an interval, so files added outside Cineva
// turn up without anyone pressing a button.
//
// A timer rather than a filesystem watch: a watch needs a descriptor per
// directory (a large library exhausts the inotify limit), misses changes
// made while the server was down, and does not see anything on a network
// mount — which is where most media lives. A periodic rescan catches all
// three, at the cost of latency nobody is waiting on.
type Scheduler struct {
	Manager  *Manager
	Store    *store.Store
	Interval time.Duration
	// Retain is how many days of audit and play history to keep. The sweep
	// rides along with the rescan because both are housekeeping that wants
	// to happen occasionally and never at a precise moment.
	Retain int
}

// Run rescans every library on the interval until the context is cancelled.
// A zero or negative interval disables it and returns immediately.
func (s *Scheduler) Run(ctx context.Context) {
	if s.Interval <= 0 {
		return
	}
	libraryLog.Info("scheduled rescans enabled", "interval", s.Interval)
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweep(ctx)
		}
	}
}

func (s *Scheduler) sweep(ctx context.Context) {
	libs, err := s.Store.Libraries(ctx)
	if err != nil {
		libraryLog.Error("scheduled rescan failed", "err", err)
		return
	}
	for _, lib := range libs {
		if ctx.Err() != nil {
			return
		}
		// A library already scanning is skipped rather than queued: the
		// scan in flight is about to produce the same result, and stacking
		// them would have two scanners writing the same rows.
		if s.Manager.Scanning(lib.ID) {
			continue
		}
		if _, err := s.Manager.StartScan(ctx, lib, false); err != nil {
			libraryLog.Error("scheduled rescan failed", "library", lib.Name, "err", err)
		}
	}
	if n, err := s.Store.PruneSessions(ctx); err != nil {
		housekeepingLog.Error("session pruning failed", "err", err)
	} else if n > 0 {
		housekeepingLog.Info("expired sessions removed", "count", n)
	}
	if audit, plays, err := s.Store.PruneHistory(ctx, s.Retain); err != nil {
		housekeepingLog.Error("history retention failed", "err", err)
	} else if audit > 0 || plays > 0 {
		housekeepingLog.Info("old history removed", "audit_rows", audit, "play_rows", plays, "retain_days", s.Retain)
	}
}
