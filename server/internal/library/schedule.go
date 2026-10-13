package library

import (
	"context"
	"log"
	"time"

	"github.com/garybowers/cineva/server/internal/store"
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
	log.Printf("libraries: rescanning every %s", s.Interval)
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
		log.Printf("scheduled rescan: %v", err)
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
			log.Printf("scheduled rescan %s: %v", lib.Name, err)
		}
	}
	if n, err := s.Store.PruneSessions(ctx); err != nil {
		log.Printf("session pruning: %v", err)
	} else if n > 0 {
		log.Printf("session pruning: removed %d expired sessions", n)
	}
	if audit, plays, err := s.Store.PruneHistory(ctx, s.Retain); err != nil {
		log.Printf("history retention: %v", err)
	} else if audit > 0 || plays > 0 {
		log.Printf("history retention: removed %d audit and %d play rows older than %d days", audit, plays, s.Retain)
	}
}
