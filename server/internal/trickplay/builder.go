// Package trickplay builds seek-bar previews in the background.
//
// Making a title's previews seeks through the whole file, so it is media
// reading nobody is waiting for, and it follows the same rules as intro
// detection (internal/background): only when the admin's schedule allows,
// never while anything is playing, scanning or being analysed, no faster
// than the read-speed cap, one file at a time, and not at all once the
// library's storage stops answering. A player asking for a title's previews
// only moves it to the front of the queue; titles nobody has played yet are
// queued after each scan, newest additions first, so their previews are
// ready before the first play.
package trickplay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/garybowers/cineva/server/internal/background"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

var logger = logging.For("trickplay")

// stallTimeout is how long one thumbnail may take before the file is taken
// to have stalled — a share gone quiet. A thumbnail is a seek and one
// frame's decode, a fraction of a second even from a NAS; pacing to the
// read-speed cap does not count towards it.
const stallTimeout = 2 * time.Minute

var (
	// errYield abandons a file because something now needs the storage
	// more; the title goes back to the front of the queue.
	errYield = errors.New("giving way")
	// errStalled abandons a file whose reads stopped answering.
	errStalled = errors.New("file not responding")
)

// GenerateFunc builds one title's previews; media.GenerateTrickplay
// outside tests.
type GenerateFunc func(ctx context.Context, path, outDir, itemID string, runtimeSecs int, progress func(bytesRead int64) error) error

// Status is what preview building is doing, for the admin's activity page.
type Status struct {
	Running bool `json:"running"`
	// Current names the title being worked on.
	Current string `json:"current,omitempty"`
	// Done and Failed count titles finished in this run; Waiting is how
	// many are still to do, the one in hand included.
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Waiting int `json:"waiting"`
	// Paused says the builder is waiting, and PauseReason why: "manual",
	// "schedule", "on-demand", "playing", "scanning" or "detecting".
	Paused      bool   `json:"paused"`
	PauseReason string `json:"pauseReason,omitempty"`
	// Last summarises the most recent finished run.
	Last *RunSummary `json:"last,omitempty"`
}

// RunSummary describes a finished run.
type RunSummary struct {
	FinishedAt time.Time `json:"finishedAt"`
	Seconds    int       `json:"seconds"`
	Done       int       `json:"done"`
	Failed     int       `json:"failed"`
}

// Builder makes seek-bar previews from a queue, one title at a time.
type Builder struct {
	Store *store.Store
	// Dir is where previews are written, as media.TrickplayPaths lays out.
	Dir string
	// Generate builds one title's previews; media.GenerateTrickplay outside
	// tests.
	Generate GenerateFunc
	// Gate decides when previews may be read for: the shared background
	// Policy, and what building gives way to (its Busy).
	Gate *background.Gate
	// Available, when set, says whether a library's storage can be reached.
	Available func(libraryID string) bool
	// OnProgress, when set, is called as the status changes.
	OnProgress func(Status)
	// StallTimeout overrides stallTimeout in tests.
	StallTimeout time.Duration

	mu      sync.Mutex
	urgent  []store.PreviewItem // asked for by a player: first
	jobs    []store.PreviewItem // from library sweeps, newest additions first
	libs    []string            // libraries waiting to be swept into jobs
	queued  map[string]bool     // item ids in urgent or jobs
	failed  map[string]int64    // item id -> file mtime that could not be read
	inHand  string              // the title taken off the queue and not yet done
	status  Status
	started time.Time
	wake    chan struct{}
}

// New returns a builder writing into dir, reading through gate.
func New(st *store.Store, dir string, gate *background.Gate) *Builder {
	return &Builder{Store: st, Dir: dir, Generate: media.GenerateTrickplay, Gate: gate,
		queued: map[string]bool{}, failed: map[string]int64{}, wake: make(chan struct{}, 1)}
}

// Has reports whether an item's previews exist and are no older than its
// file — a file replaced in place needs them made again.
func (b *Builder) Has(itemID string, fileMtime int64) bool {
	_, vtt := media.TrickplayPaths(b.Dir, itemID)
	fi, err := os.Stat(vtt)
	return err == nil && fi.ModTime().Unix() >= fileMtime
}

// Request moves a title to the front of the queue: a player wants its
// previews. They are still only built when the gate allows — which is never
// while that player is playing — so they arrive for a later viewing.
func (b *Builder) Request(item store.PreviewItem) {
	if item.RuntimeSecs <= 0 || b.Has(item.ID, item.FileMtime) {
		return
	}
	b.mu.Lock()
	if m, ok := b.failed[item.ID]; ok && m == item.FileMtime {
		b.mu.Unlock()
		return
	}
	if b.inHand == item.ID {
		b.mu.Unlock()
		return // already being built, or waiting for its turn to be
	}
	if b.queued[item.ID] {
		b.jobs = slices.DeleteFunc(b.jobs, func(j store.PreviewItem) bool { return j.ID == item.ID })
		b.urgent = slices.DeleteFunc(b.urgent, func(j store.PreviewItem) bool { return j.ID == item.ID })
	}
	b.queued[item.ID] = true
	b.urgent = append([]store.PreviewItem{item}, b.urgent...)
	b.mu.Unlock()
	b.poke()
}

// EnqueueLibrary queues every title in a library that has no previews yet.
// The library is read when the builder gets to it, not now.
func (b *Builder) EnqueueLibrary(libraryID string) {
	b.mu.Lock()
	if !slices.Contains(b.libs, libraryID) {
		b.libs = append(b.libs, libraryID)
	}
	b.mu.Unlock()
	b.poke()
}

// RunNow lets building start straight away, whatever the schedule says,
// until the queue is empty. A manual pause still holds it. Queue the work
// first: a builder that wakes to an empty queue hands the gate straight
// back to the schedule.
func (b *Builder) RunNow() {
	b.Gate.RunNow()
	b.poke()
}

func (b *Builder) poke() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// Status reports what the builder is doing.
func (b *Builder) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshot()
}

// snapshot copies the status; b.mu must be held. A manual pause is shown
// even while idle, so the page says so before there is work to hold.
func (b *Builder) snapshot() Status {
	s := b.status
	s.Waiting = len(b.urgent) + len(b.jobs)
	if b.inHand != "" {
		s.Waiting++ // still to do, though no longer in the queue
	}
	if !s.Running {
		s.Paused, s.PauseReason = false, ""
		if b.Gate.Policy.Paused() {
			s.Paused, s.PauseReason = true, "manual"
		}
	}
	if s.Last != nil {
		l := *s.Last
		s.Last = &l
	}
	return s
}

// Notify sends the current status to OnProgress — after a change made
// elsewhere, such as an admin pausing background work.
func (b *Builder) Notify() {
	if b.OnProgress != nil {
		b.OnProgress(b.Status())
	}
}

func (b *Builder) setStatus(fn func(*Status)) {
	b.mu.Lock()
	fn(&b.status)
	s := b.snapshot()
	b.mu.Unlock()
	if b.OnProgress != nil {
		b.OnProgress(s)
	}
}

// Run works through the queue until ctx is cancelled.
func (b *Builder) Run(ctx context.Context) {
	for {
		if err := b.sweep(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Error("listing titles for previews failed", "err", err)
		}
		job, ok := b.next()
		if !ok {
			// "Build now" lasts until the work it asked for is done.
			b.Gate.EndRunNow()
			b.finishRun()
			select {
			case <-ctx.Done():
				return
			case <-b.wake:
				continue
			}
		}
		err := b.build(ctx, job)
		b.mu.Lock()
		b.inHand = ""
		b.mu.Unlock()
		if err != nil && ctx.Err() != nil {
			return
		}
	}
}

// sweep turns queued libraries into jobs: each title without previews, and
// not already queued or known to be unreadable, newest additions first.
func (b *Builder) sweep(ctx context.Context) error {
	for {
		b.mu.Lock()
		if len(b.libs) == 0 {
			b.mu.Unlock()
			return nil
		}
		lib := b.libs[0]
		b.libs = b.libs[1:]
		b.mu.Unlock()

		items, err := b.Store.PreviewItems(ctx, lib)
		if err != nil {
			return fmt.Errorf("library %s: %w", lib, err)
		}
		var add []store.PreviewItem
		for _, it := range items {
			if !b.Has(it.ID, it.FileMtime) {
				add = append(add, it)
			}
		}
		b.mu.Lock()
		for _, it := range add {
			if b.queued[it.ID] {
				continue
			}
			if m, ok := b.failed[it.ID]; ok && m == it.FileMtime {
				continue
			}
			b.queued[it.ID] = true
			b.jobs = append(b.jobs, it)
		}
		b.mu.Unlock()
		if len(add) > 0 {
			logger.Debug("titles queued for previews", "library_id", lib, "count", len(add))
		}
	}
}

// next takes the next title off the queue, starting a run if none is going.
func (b *Builder) next() (store.PreviewItem, bool) {
	b.mu.Lock()
	var job store.PreviewItem
	switch {
	case len(b.urgent) > 0:
		job, b.urgent = b.urgent[0], b.urgent[1:]
	case len(b.jobs) > 0:
		job, b.jobs = b.jobs[0], b.jobs[1:]
	default:
		b.mu.Unlock()
		return job, false
	}
	delete(b.queued, job.ID)
	b.inHand = job.ID
	starting := !b.status.Running
	if starting {
		b.started = time.Now()
	}
	b.mu.Unlock()
	if starting {
		b.setStatus(func(s *Status) { *s = Status{Running: true, Last: s.Last} })
	}
	return job, true
}

// finishRun records the end of a run, once the queue is empty.
func (b *Builder) finishRun() {
	b.mu.Lock()
	running := b.status.Running
	started := b.started
	b.mu.Unlock()
	if !running {
		return
	}
	b.setStatus(func(s *Status) {
		s.Last = &RunSummary{FinishedAt: time.Now(), Seconds: int(time.Since(started).Seconds()),
			Done: s.Done, Failed: s.Failed}
		*s = Status{Last: s.Last}
	})
	if st := b.Status(); st.Last != nil && (st.Last.Done > 0 || st.Last.Failed > 0) {
		logger.Info("previews finished", "made", st.Last.Done, "failed", st.Last.Failed,
			"took", time.Duration(st.Last.Seconds)*time.Second)
	}
}

// requeue puts a title back at the front of the queue.
func (b *Builder) requeue(job store.PreviewItem) {
	b.mu.Lock()
	if !b.queued[job.ID] {
		b.queued[job.ID] = true
		b.urgent = append([]store.PreviewItem{job}, b.urgent...)
	}
	b.mu.Unlock()
}

// dropLibrary empties the queue of a library whose storage has gone. Its
// titles come back with the next scan, or when the storage returns.
func (b *Builder) dropLibrary(libraryID string) {
	b.mu.Lock()
	gone := func(j store.PreviewItem) bool {
		if j.LibraryID == libraryID {
			delete(b.queued, j.ID)
			return true
		}
		return false
	}
	b.urgent = slices.DeleteFunc(b.urgent, gone)
	b.jobs = slices.DeleteFunc(b.jobs, gone)
	b.mu.Unlock()
}

// label names a title for the status: "Show S01E02", or a film's title.
func label(it store.PreviewItem) string {
	if it.Series != "" {
		return fmt.Sprintf("%s S%02dE%02d", it.Series, it.SeasonNum, it.EpisodeNum)
	}
	return it.Title
}

// build waits for the gate, then makes one title's previews. The error is
// the context's when the builder is stopping; every other outcome is
// handled here.
func (b *Builder) build(ctx context.Context, job store.PreviewItem) error {
	// The item is read again: it may have been rescanned, or deleted, since
	// it was queued — and previews may have arrived meanwhile.
	fresh, err := b.Store.PreviewItem(ctx, job.ID)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logger.Debug("queued title gone, skipped", "item", job.ID, "err", err)
		return nil
	}
	job = fresh
	if job.RuntimeSecs <= 0 || b.Has(job.ID, job.FileMtime) {
		return nil
	}
	var available func() bool
	if b.Available != nil {
		available = func() bool { return b.Available(job.LibraryID) }
	}
	err = b.Gate.Wait(ctx, available, func(reason string) {
		b.setStatus(func(s *Status) { s.Paused, s.PauseReason = reason != "", reason })
	})
	switch {
	case errors.Is(err, background.ErrStorageGone):
		logger.Debug("storage unreachable, previews skipped until it is back", "library_id", job.LibraryID)
		b.dropLibrary(job.LibraryID)
		return nil
	case err != nil:
		b.requeue(job)
		return err
	}

	b.setStatus(func(s *Status) { s.Current = label(job) })
	began := time.Now()
	err = b.generate(ctx, job)
	b.setStatus(func(s *Status) { s.Current = "" })
	switch {
	case err == nil:
		logger.Debug("previews made", "item", job.ID, "title", label(job), "took", time.Since(began).Round(time.Second))
		b.setStatus(func(s *Status) { s.Done++ })
	case errors.Is(err, errYield):
		// Something started playing or scanning: try again once it is over.
		b.requeue(job)
	case errors.Is(err, errStalled):
		logger.Warn("file not responding, stopping until the share is back", "path", job.Path)
		b.dropLibrary(job.LibraryID)
	case ctx.Err() != nil:
		b.requeue(job)
		return ctx.Err()
	default:
		logger.Warn("preview generation failed", "item", job.ID, "path", job.Path, "err", err)
		b.mu.Lock()
		b.failed[job.ID] = job.FileMtime
		b.mu.Unlock()
		b.setStatus(func(s *Status) { s.Failed++ })
	}
	return nil
}

// generate runs the generator with pacing, giving way and a stall watchdog.
// A generator stuck in a read that never returns is abandoned rather than
// waited for: a call blocked in the kernel on a hung mount cannot be
// interrupted, so its goroutine ends whenever the mount answers.
func (b *Builder) generate(ctx context.Context, job store.PreviewItem) error {
	stall := b.StallTimeout
	if stall <= 0 {
		stall = stallTimeout
	}
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	began := time.Now()
	var last atomic.Int64 // unix nanoseconds of the last sign of life
	var pacing atomic.Bool
	last.Store(began.UnixNano())
	progress := func(read int64) error {
		last.Store(time.Now().UnixNano())
		if r := b.Gate.Yield(); r != "" {
			return errYield
		}
		pacing.Store(true)
		b.Gate.Policy.Pace(jobCtx, read, time.Since(began))
		last.Store(time.Now().UnixNano())
		pacing.Store(false)
		return jobCtx.Err()
	}
	done := make(chan error, 1) // buffered: an abandoned generator must not block on send
	go func() { done <- b.Generate(jobCtx, job.Path, b.Dir, job.ID, job.RuntimeSecs, progress) }()

	tick := time.NewTicker(min(time.Second, stall/4))
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if !pacing.Load() && time.Since(time.Unix(0, last.Load())) > stall {
				return errStalled
			}
		}
	}
}
