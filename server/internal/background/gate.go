// Package background holds the rules every piece of background media work
// follows. Cineva's first job is to play media reliably and quickly, so work
// nobody is waiting for — finding intros and credits, building seek-bar
// previews — reads the libraries' storage only when the admin's schedule
// allows it, never while anything is playing or scanning, no faster than the
// admin's read-speed cap, and not at all once the storage stops answering.
//
// A Policy is the admin's side of that, shared by every worker; a Gate is one
// worker's way through it, asked before each file.
package background

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// Schedule modes: when background work may read.
const (
	// ModeWindow reads only between Schedule.Start and Schedule.End.
	ModeWindow = "window"
	// ModeIdle reads whenever nothing else needs the storage.
	ModeIdle = "idle"
	// ModeManual reads only after an admin asks (Gate.RunNow).
	ModeManual = "manual"
)

// Schedule says when background work may read. Start and End are minutes
// after midnight, local time; a window may run past midnight (22:00 to
// 06:00).
type Schedule struct {
	Mode       string
	Start, End int
}

// Allows reports whether the schedule lets work read at now.
func (sc Schedule) Allows(now time.Time) bool {
	switch sc.Mode {
	case ModeManual:
		return false
	case ModeWindow:
		m := now.Hour()*60 + now.Minute()
		switch {
		case sc.Start == sc.End:
			return true // a window of the whole day
		case sc.Start < sc.End:
			return m >= sc.Start && m < sc.End
		default:
			return m >= sc.Start || m < sc.End
		}
	}
	return true
}

// ErrStorageGone ends work whose share has stopped answering. Carrying on
// would only add reads to the ones already stuck on it — and on a network
// share they all queue on one connection, so every other use of that share,
// playback included, stalls behind them.
var ErrStorageGone = errors.New("storage stopped responding")

// Policy is what the admin has said about background reading: when it may
// happen, how fast, and whether it is paused. One Policy is shared by every
// worker, so one schedule and one cap govern them all.
type Policy struct {
	paused    atomic.Bool
	rateLimit atomic.Int64 // bytes per second; 0 = as fast as it can
	schedule  atomic.Pointer[Schedule]
}

func NewPolicy() *Policy { return &Policy{} }

// SetSchedule changes when work may read; a nil schedule (never set) lets
// it read at any time. Work in progress obeys it from the next file.
func (p *Policy) SetSchedule(sc Schedule) { p.schedule.Store(&sc) }

// SetPaused holds every worker before its next file, or lets them carry on.
func (p *Policy) SetPaused(v bool) { p.paused.Store(v) }

// Paused reports whether an admin has paused background work.
func (p *Policy) Paused() bool { return p.paused.Load() }

// SetRateLimit caps how fast work reads media, in bytes per second; 0
// removes the cap.
func (p *Policy) SetRateLimit(bytesPerSec int64) { p.rateLimit.Store(max(0, bytesPerSec)) }

// RateLimit is the cap in bytes per second, 0 for none.
func (p *Policy) RateLimit() int64 { return p.rateLimit.Load() }

// Pace sleeps for long enough that read bytes, taking took so far, average
// no more than the rate limit. Without a limit, or when reading was already
// that slow, it returns at once.
func (p *Policy) Pace(ctx context.Context, read int64, took time.Duration) {
	limit := p.rateLimit.Load()
	if limit <= 0 || read <= 0 {
		return
	}
	wait := time.Duration(float64(read)/float64(limit)*float64(time.Second)) - took
	if wait <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
}

// Gate is one worker's way through the Policy. Each worker has its own, so
// "run now" lasts until that worker's own backlog is done, and each worker
// says what it gives way to.
type Gate struct {
	Policy *Policy
	// Busy, when set, names what the worker should give way to — "playing",
	// "scanning" — or returns "". Background reading is enough traffic to
	// starve playback from the same share, so it waits while anything else
	// needs it.
	Busy func() string
	// Cooldown is how long after the last busy moment work resumes, so it
	// does not dart in between one episode ending and the next starting.
	Cooldown time.Duration
	// Now is the clock the schedule is read against; time.Now outside tests.
	Now func() time.Time
	// Poll is how often a waiting gate looks again; a second outside tests.
	Poll time.Duration

	runNow atomic.Bool
}

// NewGate returns a gate through p with the usual cooldown and polling.
func NewGate(p *Policy) *Gate {
	return &Gate{Policy: p, Cooldown: time.Minute, Now: time.Now, Poll: time.Second}
}

// RunNow lets the worker read straight away, whatever the schedule says,
// until EndRunNow — which the worker calls once its backlog is done. A
// manual pause still holds it.
func (g *Gate) RunNow() { g.runNow.Store(true) }

// EndRunNow hands the worker back to the schedule.
func (g *Gate) EndRunNow() { g.runNow.Store(false) }

// ScheduleWait names why the schedule holds the worker — "schedule" outside
// the window, "on-demand" until an admin starts it — or returns "".
func (g *Gate) ScheduleWait() string {
	sc := g.Policy.schedule.Load()
	if sc == nil || g.runNow.Load() {
		return ""
	}
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	if sc.Allows(now()) {
		return ""
	}
	if sc.Mode == ModeManual {
		return "on-demand"
	}
	return "schedule"
}

// Wait holds before a file while work is paused, outside its schedule, or
// while something else needs the storage, and returns nil when the worker
// may read — or ErrStorageGone once available reports the storage gone, or
// the context's error. report, when set, hears why it is waiting each time
// that changes: "manual", "schedule", "on-demand" or a Busy reason, and ""
// when the gate opens.
func (g *Gate) Wait(ctx context.Context, available func() bool, report func(reason string)) error {
	return g.wait(ctx, available, report, true)
}

// WaitNow is Wait for work someone asked for by name — "Detect" on one
// series — which goes ahead whatever the schedule says, as "Analyse now"
// does, but still holds for a manual pause and gives way to playback and
// scans.
func (g *Gate) WaitNow(ctx context.Context, available func() bool, report func(reason string)) error {
	return g.wait(ctx, available, report, false)
}

func (g *Gate) wait(ctx context.Context, available func() bool, report func(reason string), schedule bool) error {
	poll := g.Poll
	if poll <= 0 {
		poll = time.Second
	}
	var lastBusy time.Time
	busyReason := ""
	reported := "\x00" // nothing reported yet, so the first answer always is
	tell := func(reason string) {
		if report != nil && reason != reported {
			report(reason)
		}
		reported = reason
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if available != nil && !available() {
			return ErrStorageGone
		}
		reason := ""
		if g.Policy.Paused() {
			reason = "manual"
		} else if r := g.ScheduleWait(); schedule && r != "" {
			reason = r
		} else if g.Busy != nil {
			if r := g.Busy(); r != "" {
				reason, busyReason, lastBusy = r, r, time.Now()
			}
		}
		// Busy a moment ago counts as busy until the cooldown has passed.
		if reason == "" && !lastBusy.IsZero() && time.Since(lastBusy) < g.Cooldown {
			reason = busyReason
		}
		tell(reason)
		if reason == "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// Yield reports what the worker should give way to right now — a Busy
// reason, or "" — for work that checks between parts of one long file
// rather than only before it.
func (g *Gate) Yield() string {
	if g.Busy == nil {
		return ""
	}
	return g.Busy()
}
