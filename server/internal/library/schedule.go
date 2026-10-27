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
	// Playing, when set, reports whether anything is being played. A rescan
	// walks every folder and probes changed files on the share playback
	// streams from, so a due rescan waits for playback to stop — up to
	// scheduledRescanCeiling.
	Playing func() bool

	// poll and ceiling override playbackPoll and scheduledRescanCeiling in
	// tests.
	poll, ceiling time.Duration
}

// playbackPoll is how often a rescan waiting for playback looks again.
const playbackPoll = 30 * time.Second

// scheduledRescanCeiling is the longest a scheduled rescan waits for
// playback to stop. Without one, a film night — or a screen left playing
// all evening — would postpone picking up new files indefinitely; after two
// hours the rescan runs anyway, which on an unchanged library is a walk of
// the folders and little else.
const scheduledRescanCeiling = 2 * time.Hour

// webhookRescanCeiling is the longest a Sonarr or Radarr webhook's rescan
// waits for playback to stop. Much shorter than the scheduled one: picking
// up a new episode within minutes is the point of the webhook.
const webhookRescanCeiling = 10 * time.Minute

// waitForPlayback holds while playing reports true, looking again every
// poll, for at most ceiling. It returns how long it waited, whether the
// ceiling ended the wait rather than playback stopping, and the context's
// error if it was cancelled.
func waitForPlayback(ctx context.Context, playing func() bool, poll, ceiling time.Duration) (time.Duration, bool, error) {
	if playing == nil || !playing() {
		return 0, false, nil
	}
	start := time.Now()
	deadline := time.NewTimer(ceiling)
	defer deadline.Stop()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return time.Since(start), false, ctx.Err()
		case <-deadline.C:
			return time.Since(start), true, nil
		case <-tick.C:
			if !playing() {
				return time.Since(start), false, nil
			}
		}
	}
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
			if s.yield(ctx) != nil {
				return
			}
			s.sweep(ctx)
		}
	}
}

// yield waits, before a due rescan, for anything playing to stop — up to
// the ceiling, after which the rescan goes ahead. It returns the context's
// error if the server stops while it waits.
func (s *Scheduler) yield(ctx context.Context) error {
	if s.Playing == nil || !s.Playing() {
		return nil
	}
	poll, ceiling := s.poll, s.ceiling
	if poll <= 0 {
		poll = playbackPoll
	}
	if ceiling <= 0 {
		ceiling = scheduledRescanCeiling
	}
	libraryLog.Debug("rescan waiting for playback to finish", "trigger", "schedule", "max_wait", ceiling)
	waited, timedOut, err := waitForPlayback(ctx, s.Playing, poll, ceiling)
	if err != nil {
		return err
	}
	if timedOut {
		libraryLog.Info("scheduled rescan starting while playback continues", "waited", waited.Round(time.Second))
	} else {
		libraryLog.Debug("playback finished, scheduled rescan starting", "waited", waited.Round(time.Second))
	}
	return nil
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
	if n, err := s.Store.PruneDevices(ctx); err != nil {
		housekeepingLog.Error("device pruning failed", "err", err)
	} else if n > 0 {
		housekeepingLog.Info("unused devices forgotten", "count", n, "unused_for_days", store.DeviceUnusedDays)
	}
	if n, err := s.Store.PruneDownloadJobs(ctx, store.FinishedDownloadDays); err != nil {
		housekeepingLog.Error("download job pruning failed", "err", err)
	} else if n > 0 {
		housekeepingLog.Info("finished download jobs removed", "count", n)
	}
	if audit, plays, err := s.Store.PruneHistory(ctx, s.Retain); err != nil {
		housekeepingLog.Error("history retention failed", "err", err)
	} else if audit > 0 || plays > 0 {
		housekeepingLog.Info("old history removed", "audit_rows", audit, "play_rows", plays, "retain_days", s.Retain)
	}
}
