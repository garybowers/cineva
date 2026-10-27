package library

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// Scanning in passes (server/docs/scanning.md). A scan that registers more
// than enrichThreshold new films or series saves them from their paths and
// leaves the provider alone; this job then looks every unmatched title up,
// newest first, as the retry pass would, with progress on the scan's row
// and an "items" event per title so tiles fill in as it runs. It starts a
// little after the register pass begins and runs alongside it, in rounds,
// until the pass has ended and a round finds nothing left; the readers
// (intro detection, previews) start when it ends.

// enrichThreshold is how many new titles one scan looks up inline before
// deferring them all to the job: a new episode or a film appears with its
// details in one go; a first scan or a bulk import appears first and fills
// in behind.
const enrichThreshold = 25

const (
	// enrichStartDelay is how long after a scan decides to defer that the
	// job starts, so the first titles are on screen before the lookups
	// begin and the share's first reads are not competing with them.
	enrichStartDelay = 20 * time.Second
	// enrichWorkers is how many lookups run at once; each keeps the retry
	// pass's pacing between its own requests, so four of them stay under
	// TMDB's limit of about fifty requests a second.
	enrichWorkers = 4
	// enrichRoundWait is how long a round that found nothing waits before
	// asking again while the register pass is still running.
	enrichRoundWait = 5 * time.Second
	// enrichRetryWait is how long a worker waits when the provider cannot
	// be reached before trying the same title again, and enrichRetries how
	// many such outages the job survives before it gives up and leaves the
	// rest to the slow retry cadence.
	enrichRetryWait = time.Minute
	enrichRetries   = 3
)

// closedChan is a register pass that has already ended, for a job started
// after the pass rather than alongside it.
var closedChan = func() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

// enrichJob is one library's running enrichment.
type enrichJob struct {
	cancel context.CancelFunc
	done   chan struct{}

	// mu guards the fields below and serialises the writes to the scan
	// row's phase, so the pass ending and the job ending cannot cross:
	// whichever writes last writes the phase that is true.
	mu sync.Mutex
	// scanID is the scan row the progress is written to: the latest scan
	// whose titles the job is covering.
	scanID string
	// registerDone is closed when that scan's register pass ends. A later
	// scan that defers while the job runs replaces it, so the job keeps
	// going until the newest pass has ended too.
	registerDone <-chan struct{}
	// readers says the readers (detection, previews) start when the job
	// ends: not after a register pass that was cancelled or failed.
	readers bool
	// ended is set once the job has closed the phase.
	ended bool

	enriched atomic.Int64
	toEnrich atomic.Int64
}

// registeringLocked reports whether the register pass the job covers is
// still running. Called with j.mu held.
func (j *enrichJob) registeringLocked() bool {
	select {
	case <-j.registerDone:
		return false
	default:
		return true
	}
}

func (j *enrichJob) registering() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.registeringLocked()
}

// registerDoneChan is the channel closed when the register pass the job
// currently covers ends.
func (j *enrichJob) registerDoneChan() <-chan struct{} {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.registerDone
}

// enrichProgress is how far a library's job has got, for a scan's own
// progress events while the two run alongside: zeros when there is none.
func (m *Manager) enrichProgress(libID string) (enriched, toEnrich int) {
	m.mu.Lock()
	job := m.enrichJobs[libID]
	m.mu.Unlock()
	if job == nil {
		return 0, 0
	}
	return int(job.enriched.Load()), int(job.toEnrich.Load())
}

// providerFor is the provider a scan's register pass looks titles up with:
// the real one, or none when the plan defers the lookups to the job, which
// then starts now, alongside the pass, after enrichDelay. Without an event
// bus (the CLI) the job is started at the end of the pass instead
// (runScan), so `cineva scan` ends with the details fetched as it always
// did.
func (m *Manager) providerFor(lib store.Library, plan *scanPlan) metadata.VideoProvider {
	if !plan.deferLookups {
		return m.Video
	}
	if m.Events != nil {
		plan.enriching = m.startEnrich(lib, plan.scanID, plan.registerDone, m.enrichDelay)
	}
	return nil
}

// registerEnded tells the job a scan's register pass is over. When the job
// is still fetching that scan's details (enriching) it reports the counts
// so far, and the scan row says "enriching" from here: runScan has just
// closed the row, and the job closes the phase when it ends. The scan
// event for the row is published here, under the job's lock, so it cannot
// overtake the job's own "done". readers says the scan starts the readers
// itself: no job took it on, or the job ended during the pass (stopped,
// or the provider unreachable) and left them to the pass.
func (m *Manager) registerEnded(ctx context.Context, lib store.Library, plan *scanPlan, done store.Scan, status string) (enriched, toEnrich int, enriching, readers bool) {
	m.mu.Lock()
	job := m.enrichJobs[lib.ID]
	m.setOpenSeriesLocked(lib.ID, "")
	m.mu.Unlock()
	if job == nil || !plan.enriching {
		close(plan.registerDone)
		return 0, 0, false, true
	}
	// Closed under the job's lock: the job decides whether to start the
	// readers by whether the pass had ended when it did.
	job.mu.Lock()
	defer job.mu.Unlock()
	close(plan.registerDone)
	if job.ended {
		return 0, 0, false, true
	}
	if job.scanID != plan.scanID {
		return 0, 0, false, false // a later scan's pass took the job on
	}
	job.readers = status == "completed"
	enriched, toEnrich = int(job.enriched.Load()), int(job.toEnrich.Load())
	if err := m.Store.UpdateScanPhase(ctx, plan.scanID, store.PhaseEnriching, enriched, toEnrich); err != nil {
		metadataLog.Warn("saving enrichment progress failed", "scan", plan.scanID, "err", err)
	}
	done.Phase, done.Enriched, done.ToEnrich = store.PhaseEnriching, enriched, toEnrich
	m.Events.Publish(events.Event{Type: "scan", Data: scanEvent{Scan: done}})
	return enriched, toEnrich, true, false
}

// setOpenSeriesLocked records the series folder the TV register pass is
// still writing episodes into ("" for none), with m.mu held. The job
// leaves that series for a later round: matching it now would fill in the
// episodes registered so far and miss the rest.
func (m *Manager) setOpenSeriesLocked(libID, showDir string) {
	if m.openSeries == nil {
		m.openSeries = map[string]string{}
	}
	if showDir == "" {
		delete(m.openSeries, libID)
		return
	}
	m.openSeries[libID] = showDir
}

func (m *Manager) setOpenSeries(libID, showDir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setOpenSeriesLocked(libID, showDir)
}

func (m *Manager) openSeriesFor(libID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.openSeries[libID]
}

// startEnrich starts a library's job, after the delay, unless one is
// already running, in which case that job takes the new scan on (its row
// for progress, its register pass to wait for) and keeps going. It reports
// whether a job is now covering the scan. registerDone is closed when the
// register pass ends; a scan whose pass has already ended passes a closed
// channel. Without an event bus (the CLI) the job runs in line, so
// `cineva scan` ends with the details fetched as it always did; it is
// then started at the end of the pass, never alongside it.
func (m *Manager) startEnrich(lib store.Library, scanID string, registerDone <-chan struct{}, delay time.Duration) bool {
	if m.Video == nil || (lib.MediaType != store.MediaMovies && lib.MediaType != store.MediaTV) {
		return false
	}
	m.mu.Lock()
	if m.enrichJobs == nil {
		m.enrichJobs = map[string]*enrichJob{}
	}
	if job, busy := m.enrichJobs[lib.ID]; busy {
		m.mu.Unlock()
		job.mu.Lock()
		defer job.mu.Unlock()
		if job.ended {
			return false
		}
		// The earlier scan's row is closed; the progress continues on
		// the new one.
		if err := m.Store.UpdateScanPhase(context.Background(), job.scanID, store.PhaseDone,
			int(job.enriched.Load()), int(job.toEnrich.Load())); err != nil {
			metadataLog.Warn("closing enrichment phase failed", "scan", job.scanID, "err", err)
		}
		job.scanID, job.registerDone, job.readers = scanID, registerDone, true
		return true
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &enrichJob{cancel: cancel, done: make(chan struct{}), scanID: scanID, registerDone: registerDone, readers: true}
	m.enrichJobs[lib.ID] = job
	m.mu.Unlock()
	run := func() {
		defer func() {
			cancel()
			m.mu.Lock()
			delete(m.enrichJobs, lib.ID)
			m.mu.Unlock()
			close(job.done)
		}()
		if delay > 0 {
			select {
			case <-ctx.Done():
				m.endEnrich(context.Background(), lib, job)
				return
			case <-time.After(delay):
			}
		}
		m.enrichLibrary(ctx, lib, job)
	}
	if m.Events == nil {
		run()
		return true
	}
	go run()
	return true
}

// enrichRunning reports whether a library's job is running.
func (m *Manager) enrichRunning(libID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.enrichJobs[libID]
	return ok
}

// cancelEnrich stops a library's job, reporting whether there was one.
func (m *Manager) cancelEnrich(libID string) bool {
	m.mu.Lock()
	job, ok := m.enrichJobs[libID]
	m.mu.Unlock()
	if ok {
		job.cancel()
	}
	return ok
}

// WaitEnrich blocks until a library's job, if any, has ended. For tests.
func (m *Manager) WaitEnrich(libID string) {
	m.mu.Lock()
	job, ok := m.enrichJobs[libID]
	m.mu.Unlock()
	if ok {
		<-job.done
	}
}

// enrichLibrary works through the unmatched titles in rounds, newest first,
// through a pool of workers that pace the provider as the retry pass does,
// and keeps the scan row and the clients told. A round that finds nothing
// ends the job once the register pass has ended; while the pass runs it
// waits and asks again, since the pass is still adding titles. A title the
// provider does not know is recorded as the retry pass records it, so it
// is next tried on the slow cadence; a provider that cannot be reached is
// waited for, a few times, then the job gives up.
func (m *Manager) enrichLibrary(ctx context.Context, lib store.Library, job *enrichJob) {
	started := time.Now()
	bg := context.WithoutCancel(ctx)
	matched := atomic.Int64{}
	outages := atomic.Int64{}
	tried := map[string]bool{} // titles this job has already looked up
	metadataLog.Info("fetching details", "library", lib.Name)

	for ctx.Err() == nil {
		items, err := m.Store.UnmatchedItemsNewest(ctx, lib.ID)
		if err != nil {
			if ctx.Err() == nil {
				metadataLog.Error("listing titles to enrich failed", "library", lib.Name, "err", err)
			}
			break
		}
		now := m.now()
		registering := job.registering()
		open := m.openSeriesFor(lib.ID)
		var round []store.UnmatchedItem
		for _, it := range items {
			if tried[it.ID] || !retryDue(it, now) {
				continue
			}
			if registering && it.Kind == store.KindSeries && it.Path == open {
				continue // its episodes are still being registered
			}
			tried[it.ID] = true
			round = append(round, it)
		}
		if len(round) == 0 {
			if !registering {
				break
			}
			// The pass is still adding titles: ask again in a while, or
			// as soon as the pass ends.
			select {
			case <-ctx.Done():
			case <-time.After(m.roundWait):
			case <-job.registerDoneChan():
			}
			continue
		}
		job.toEnrich.Add(int64(len(round)))
		m.reportEnrich(bg, lib, job)
		m.enrichRound(ctx, lib, job, round, &matched, &outages)
	}

	enriched, total := job.enriched.Load(), job.toEnrich.Load()
	switch {
	case ctx.Err() != nil && outages.Load() > enrichRetries:
		metadataLog.Warn("fetching details stopped: provider unreachable", "library", lib.Name, "done", enriched, "of", total)
	case ctx.Err() != nil:
		metadataLog.Info("fetching details cancelled", "library", lib.Name, "done", enriched, "of", total)
	default:
		metadataLog.Info("details fetched", "library", lib.Name, "titles", total, "matched", matched.Load(), "took", time.Since(started))
	}
	m.endEnrich(bg, lib, job)
}

// enrichRound looks one round's titles up, enrichWorkers at a time, the
// workers taking from the ordered list so newest-first holds near enough.
func (m *Manager) enrichRound(ctx context.Context, lib store.Library, job *enrichJob, round []store.UnmatchedItem, matched, outages *atomic.Int64) {
	queue := make(chan store.UnmatchedItem)
	var wg sync.WaitGroup
	for w := 0; w < enrichWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			first := true
			for it := range queue {
				if ctx.Err() != nil {
					continue // drain
				}
				if !first && m.retryPace > 0 {
					select {
					case <-ctx.Done():
						continue
					case <-time.After(m.retryPace):
					}
				}
				first = false
				m.enrichOne(ctx, lib, job, it, matched, outages)
			}
		}()
	}
	for _, it := range round {
		if ctx.Err() != nil {
			break
		}
		queue <- it
	}
	close(queue)
	wg.Wait()
}

// enrichOne looks one title up and counts it, waiting out a provider
// outage a few times before the job gives up.
func (m *Manager) enrichOne(ctx context.Context, lib store.Library, job *enrichJob, it store.UnmatchedItem, matched, outages *atomic.Int64) {
	for {
		ok, err := m.retryOne(ctx, lib, it)
		switch {
		case ok:
			matched.Add(1)
		case errors.Is(err, metadata.ErrNotFound):
			// Recorded against the title; tried again on the slow cadence.
		case err != nil && ctx.Err() == nil:
			var lookup lookupError
			if errors.As(err, &lookup) {
				if outages.Add(1) > enrichRetries {
					metadataLog.Warn("provider unreachable, giving up", "library", lib.Name, "err", err)
					job.cancel()
					return
				}
				metadataLog.Warn("provider unreachable, waiting", "library", lib.Name, "err", err, "wait", enrichRetryWait)
				select {
				case <-ctx.Done():
					return
				case <-time.After(enrichRetryWait):
				}
				continue
			}
			metadataLog.Error("fetching details failed", "library", lib.Name, "title", it.Title, "err", err)
		}
		if ctx.Err() != nil {
			return
		}
		job.enriched.Add(1)
		m.reportEnrich(context.WithoutCancel(ctx), lib, job)
		return
	}
}

// reportEnrich writes the job's progress to the scan row and tells the
// admin (scan event) and everyone (items event, with the counts). The
// phase is registering while the register pass still runs alongside.
func (m *Manager) reportEnrich(ctx context.Context, lib store.Library, job *enrichJob) {
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.ended {
		return
	}
	registering := job.registeringLocked()
	phase := store.PhaseEnriching
	if registering {
		phase = store.PhaseRegistering
	}
	enriched, total := int(job.enriched.Load()), int(job.toEnrich.Load())
	if err := m.Store.UpdateScanPhase(ctx, job.scanID, phase, enriched, total); err != nil {
		metadataLog.Warn("saving enrichment progress failed", "scan", job.scanID, "err", err)
	}
	if m.Events == nil {
		return
	}
	if sc, err := m.Store.ScanByID(ctx, job.scanID); err == nil {
		m.Events.Publish(events.Event{Type: "scan", Data: scanEvent{Scan: sc}})
	}
	m.Events.Publish(events.Event{Type: "items", Data: itemsEvent{LibraryID: lib.ID, Scanning: registering, Enriched: enriched, ToEnrich: total}})
}

// endEnrich closes the phase and starts the readers, whether the job ran
// to the end, was cancelled or gave up: the readers do not depend on the
// details, and the titles still unmatched are the retry pass's.
func (m *Manager) endEnrich(ctx context.Context, lib store.Library, job *enrichJob) {
	job.mu.Lock()
	job.ended = true
	// A job that ends while the pass is still registering (stopped, or
	// the provider unreachable) leaves the readers to the pass.
	scanID, readers := job.scanID, job.readers && !job.registeringLocked()
	enriched, total := int(job.enriched.Load()), int(job.toEnrich.Load())
	if err := m.Store.UpdateScanPhase(ctx, scanID, store.PhaseDone, enriched, total); err != nil {
		metadataLog.Warn("closing enrichment phase failed", "scan", scanID, "err", err)
	}
	if m.Events != nil {
		if sc, err := m.Store.ScanByID(ctx, scanID); err == nil {
			m.Events.Publish(events.Event{Type: "scan", Data: scanEvent{Scan: sc}})
		}
	}
	job.mu.Unlock()
	if m.Events != nil {
		m.Events.Publish(events.Event{Type: "items", Data: itemsEvent{LibraryID: lib.ID}})
		m.Events.Publish(events.Event{Type: "libraries"})
	}
	if readers && m.OnScanDone != nil {
		m.OnScanDone(lib)
	}
}
