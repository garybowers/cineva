package segments

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/garybowers/cineva/server/internal/background"
	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

var logger = logging.For("segments")

// spec is what distinguishes one kind of segment from another: where in an
// episode it is looked for, and how long it may be.
type spec struct {
	kind string
	// A shared stretch outside these bounds is something else.
	minSecs, maxSecs float64
	// The window searched is a quarter of the episode, at most this long,
	// counted from the start or — fromEnd — back from the end. The window
	// is most of the cost: decoding reads the file.
	maxWindow float64
	fromEnd   bool
	// minAgree is how many comparisons must find the same stretch before it
	// is believed. One will do for an intro, where a mistake costs a button
	// nobody presses; credits start autoplay, so a mistake there skips the
	// end of the story. On Succession, whose end music changes every
	// episode, single comparisons found "credits" six minutes before the
	// end of the finale.
	minAgree int
}

var specs = []spec{
	// Under 15 seconds is a studio logo or a sting, which nobody needs to
	// skip; over two minutes, something else two episodes share — the same
	// episode twice, or a long recap. Fifteen minutes of opening, not ten:
	// a premiere's cold open runs long, and Succession's second-season
	// premiere reaches its titles at 10:44.
	{kind: store.SegmentIntro, minSecs: 15, maxSecs: 120, maxWindow: 900, minAgree: 1},
	// End credits run longer than titles, and the shared part is the end
	// theme, which can play over all of them. Ten minutes back from the end
	// covers credits plus a "next time" trailer or a post-credits scene.
	{kind: store.SegmentCredits, minSecs: 15, maxSecs: 360, maxWindow: 600, fromEnd: true, minAgree: 2},
}

// decodeTimeout bounds one file's fingerprinting. Reading fifteen minutes of
// audio takes a few seconds from local disk and under ten from a NAS.
const decodeTimeout = 3 * time.Minute

// errStorageGone ends a run whose share has stopped answering.
var errStorageGone = background.ErrStorageGone

// How many other episodes each one is compared with. More than one, so a
// stretch only two episodes happen to share is outvoted (see pick); a few,
// because each comparison needs another file decoded.
const partners = 4

// window is the stretch of an episode searched for this kind: its start and
// length in seconds. ok is false when it cannot be placed — the end of a
// file whose length is unknown.
func (sp spec) window(runtimeSecs int) (start, dur float64, ok bool) {
	dur = sp.maxWindow
	if runtimeSecs > 0 {
		dur = min(dur, float64(runtimeSecs)/4)
	}
	if !sp.fromEnd {
		return 0, dur, true
	}
	if runtimeSecs <= 0 {
		return 0, 0, false
	}
	return float64(runtimeSecs) - dur, dur, true
}

// Status is what the detector is doing, for the admin's activity page.
type Status struct {
	Running      bool      `json:"running"`
	LibraryID    string    `json:"libraryId,omitempty"`
	StartedAt    time.Time `json:"startedAt,omitzero"`
	SeasonsDone  int       `json:"seasonsDone"`
	SeasonsTotal int       `json:"seasonsTotal"`
	// Episodes analysed so far against those waiting when the run began;
	// seasons vary too much in length to estimate time remaining by.
	EpisodesDone  int `json:"episodesDone"`
	EpisodesTotal int `json:"episodesTotal"`
	// IntrosFound and CreditsFound count the episodes given each in this
	// run.
	IntrosFound  int `json:"introsFound"`
	CreditsFound int `json:"creditsFound"`
	// Unreadable counts files whose audio could not be decoded.
	Unreadable int `json:"unreadable"`
	// Paused says the run is waiting, and PauseReason why: "manual" (an
	// admin paused it), "playing" or "scanning" (it gives way to both, and
	// carries on a minute after they stop).
	Paused      bool   `json:"paused"`
	PauseReason string `json:"pauseReason,omitempty"`
	// FilmsDone and FilmsTotal count a library's films looked at for end
	// credits (by their chapter names) against those waiting when the run
	// began.
	FilmsDone  int `json:"filmsDone"`
	FilmsTotal int `json:"filmsTotal"`
	// Current is the season — or film — being worked on.
	Current *SeasonProgress `json:"current,omitempty"`
	// Targets are the seasons and films someone asked for by name ("Detect
	// intro & credits" on a series), analysed ahead of any library, in
	// order; the first is being worked on when its Working is set. The
	// library run, if there is one, carries on after them.
	Targets []Target `json:"targets,omitempty"`
	// Recent is what the last episodes analysed came to, newest first —
	// which also gives anyone testing an episode to try.
	Recent []Result `json:"recent,omitempty"`
	// Last summarises the most recent finished run, so the page has
	// something to say once the work is done.
	Last *RunSummary `json:"last,omitempty"`
}

// Reading reports whether detection is reading media right now — a library
// run or a targeted one, not held — which other background work gives way
// to.
func (s Status) Reading() bool {
	if s.Paused {
		return false
	}
	return s.Running || slices.ContainsFunc(s.Targets, func(t Target) bool { return t.Working })
}

// Target is a season or film asked for by name.
type Target struct {
	// ItemID is the season's or the film's.
	ItemID    string `json:"itemId"`
	Kind      string `json:"kind"` // "season" or "film"
	LibraryID string `json:"libraryId"`
	// SeriesID is the season's series, so a series page can show progress
	// for any of its seasons.
	SeriesID string `json:"seriesId,omitempty"`
	// Title is the series' or the film's.
	Title   string `json:"title"`
	Season  int    `json:"season,omitempty"`
	Working bool   `json:"working"`
}

// Target kinds.
const (
	TargetSeason = "season"
	TargetFilm   = "film"
)

// SeasonProgress is where the detector is within one season, or one film.
type SeasonProgress struct {
	Series string `json:"series"`
	Season int    `json:"season"`
	// Film is set when Series names a film rather than a series.
	Film bool   `json:"film,omitempty"`
	Kind string `json:"kind"`
	// Phase is "reading" (decoding audio), "comparing" or "saving".
	Phase string `json:"phase"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// Result is one episode's outcome.
type Result struct {
	ItemID  string `json:"itemId"`
	Kind    string `json:"kind"`
	Series  string `json:"series"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	// Film is set when Series names a film rather than a series.
	Film      bool    `json:"film,omitempty"`
	Found     bool    `json:"found"`
	StartSecs float64 `json:"startSecs,omitempty"`
	EndSecs   float64 `json:"endSecs,omitempty"`
}

// RunSummary describes a finished run.
type RunSummary struct {
	LibraryID    string    `json:"libraryId"`
	FinishedAt   time.Time `json:"finishedAt"`
	Seconds      int       `json:"seconds"`
	Seasons      int       `json:"seasons"`
	Episodes     int       `json:"episodes"`
	Films        int       `json:"films"`
	IntrosFound  int       `json:"introsFound"`
	CreditsFound int       `json:"creditsFound"`
	Unreadable   int       `json:"unreadable"`
	// Cancelled is true when the run was stopped (the server shutting
	// down) before it finished.
	Cancelled bool `json:"cancelled,omitempty"`
}

// recentResults is how many outcomes Status.Recent keeps.
const recentResults = 12

// progressEvery throttles progress notifications: files finish several a
// second on a fast disk, and each notification is an event to every open
// admin page.
const progressEvery = 500 * time.Millisecond

// Detector finds intros and end credits in the background: in TV libraries
// by comparing episodes, in film libraries from the films' chapter names.
// It works one library at a time, except that seasons and films asked for
// by name go first (Prioritise). Work is incremental: only episodes and
// films whose current file has not been analysed are, so after the first
// pass over a library a run costs the new episodes and the neighbours they
// are compared with.
type Detector struct {
	Store *store.Store
	// Fingerprint reads a stretch of a file's audio; media.FingerprintFile
	// outside tests.
	Fingerprint func(ctx context.Context, path string, start, dur float64) (media.Fingerprint, error)
	// Probe reads a film's chapter marks (and the rest of its technical
	// details); media.Probe outside tests.
	Probe func(path string) (media.Info, error)
	// OnProgress, when set, is called as the status changes.
	OnProgress func(Status)
	// Available, when set, says whether a library's storage can be reached;
	// a library that cannot is skipped rather than left to hang the run.
	Available func(libraryID string) bool
	// Gate decides when detection may read: the admin's schedule, pause and
	// read-speed cap (its Policy, shared with other background work), and
	// what detection gives way to (its Busy). Reading a quarter of every
	// episode is enough traffic to starve playback from the same share, so
	// detection waits while anything else needs it.
	Gate *background.Gate

	// DecodeTimeout bounds one file's fingerprinting; decodeTimeout
	// outside tests.
	DecodeTimeout time.Duration

	mu       sync.Mutex
	queue    []string
	targets  []Target
	status   Status
	wake     chan struct{}
	notified time.Time
	// reading is the library whose storage is being read, which the gate
	// checks is still there.
	reading string
	// preempt, while a library's season or film is being worked on, stops
	// it so targeted work can go first (see maybePreempt).
	preempt context.CancelFunc
}

func NewDetector(st *store.Store) *Detector {
	return &Detector{Store: st, Fingerprint: media.FingerprintFile, Probe: media.Probe,
		Gate:          background.NewGate(background.NewPolicy()),
		DecodeTimeout: decodeTimeout, wake: make(chan struct{}, 1)}
}

// SetSchedule changes when detection — and anything else sharing its
// Policy — may read. A run in progress obeys it from the next file.
func (d *Detector) SetSchedule(sc background.Schedule) { d.Gate.Policy.SetSchedule(sc) }

// RunNow lets detection read straight away, whatever the schedule says,
// until everything queued is done. A manual pause still holds it.
func (d *Detector) RunNow() { d.Gate.RunNow() }

// SetPaused pauses or resumes detection, and anything else sharing its
// Policy. A pause holds before the next file; the one being read finishes
// first.
func (d *Detector) SetPaused(p bool) {
	d.Gate.Policy.SetPaused(p)
	d.setStatus(true, func(s *Status) {
		if p {
			s.Paused, s.PauseReason = true, "manual"
		} else if s.PauseReason == "manual" {
			s.Paused, s.PauseReason = false, ""
		}
	})
}

// SetRateLimit caps how fast detection — and anything else sharing its
// Policy — reads media, in bytes per second; 0 removes the cap. Takes effect
// from the next file.
func (d *Detector) SetRateLimit(bytesPerSec int64) { d.Gate.Policy.SetRateLimit(bytesPerSec) }

// gate holds before each file while detection is paused, outside its
// schedule, or while something else needs the storage, and returns when it
// may read — or errStorageGone once the library's share stops answering.
// Why it waits goes into the status.
func (d *Detector) gate(ctx context.Context) error { return d.gateFor(ctx, false) }

// gateFor is gate for targeted work too, which ignores the schedule.
func (d *Detector) gateFor(ctx context.Context, targeted bool) error {
	var available func() bool
	if d.Available != nil {
		available = func() bool {
			d.mu.Lock()
			lib := d.reading
			if lib == "" {
				lib = d.status.LibraryID
			}
			d.mu.Unlock()
			return lib == "" || d.Available(lib)
		}
	}
	report := func(reason string) {
		if st := d.Status(); st.Paused != (reason != "") || st.PauseReason != reason {
			d.setStatus(true, func(s *Status) { s.Paused, s.PauseReason = reason != "", reason })
		}
		d.mu.Lock()
		d.maybePreempt()
		d.mu.Unlock()
	}
	if targeted {
		return d.Gate.WaitNow(ctx, available, report)
	}
	return d.Gate.Wait(ctx, available, report)
}

// maybePreempt stops the library work in hand when targeted work is waiting
// and the library work is only being held by the schedule — which could be
// hours. Work that is reading, or held by playback or a pause (which would
// hold the targeted work as well), finishes its season first. d.mu must be
// held.
func (d *Detector) maybePreempt() {
	if d.preempt == nil || len(d.targets) == 0 {
		return
	}
	if r := d.status.PauseReason; r == "schedule" || r == "on-demand" {
		d.preempt()
		d.preempt = nil
	}
}

// pace sleeps after reading a file for long enough that detection averages
// no more than the rate limit. What was read is estimated from the file's
// size and the share of its running time the window covered: the demuxer
// reads the video around the audio, so that is close to the truth.
func (d *Detector) pace(ctx context.Context, ep store.SegmentEpisode, dur float64, took time.Duration) {
	if ep.SizeBytes <= 0 || ep.RuntimeSecs <= 0 {
		return
	}
	read := float64(ep.SizeBytes) * min(1, dur/float64(ep.RuntimeSecs))
	d.Gate.Policy.Pace(ctx, int64(read), took)
}

// Enqueue asks for a library to be analysed. A library already queued is not
// queued twice; one being analysed right now is queued again, so episodes
// added while it runs are not missed.
func (d *Detector) Enqueue(libraryID string) {
	d.mu.Lock()
	for _, id := range d.queue {
		if id == libraryID {
			d.mu.Unlock()
			return
		}
	}
	d.queue = append(d.queue, libraryID)
	d.mu.Unlock()
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Prioritise asks for seasons and films to be analysed ahead of any library,
// in the order given, whatever the schedule says (a manual pause, playback
// and scans still hold it). Each is analysed as far as its analyses are due:
// the caller reopens them first (store.RedetectItemSegments). One already
// waiting is not queued twice.
func (d *Detector) Prioritise(ctx context.Context, seasonIDs, filmIDs []string) {
	var add []Target
	for _, id := range seasonIDs {
		add = append(add, d.describe(ctx, id, TargetSeason))
	}
	for _, id := range filmIDs {
		add = append(add, d.describe(ctx, id, TargetFilm))
	}
	if len(add) == 0 {
		return
	}
	d.mu.Lock()
	for _, t := range add {
		if !slices.ContainsFunc(d.targets, func(q Target) bool { return q.ItemID == t.ItemID && !q.Working }) {
			d.targets = append(d.targets, t)
		}
	}
	d.maybePreempt()
	d.mu.Unlock()
	d.setStatus(true, func(*Status) {})
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// describe labels a target for the status. Best effort: a lookup failure
// leaves the label blank rather than the request refused.
func (d *Detector) describe(ctx context.Context, id, kind string) Target {
	t := Target{ItemID: id, Kind: kind}
	it, err := d.Store.Item(ctx, id)
	if err != nil {
		return t
	}
	t.LibraryID, t.Title, t.Season = it.LibraryID, it.Title, it.SeasonNum
	if kind == TargetSeason {
		t.SeriesID = it.ParentID
		if series, err := d.Store.Item(ctx, it.ParentID); err == nil {
			t.Title = series.Title
		}
	}
	return t
}

func (d *Detector) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshot()
}

// snapshot copies the status, slices and all; d.mu must be held.
func (d *Detector) snapshot() Status {
	s := d.status
	if s.Current != nil {
		c := *s.Current
		s.Current = &c
	}
	s.Recent = append([]Result(nil), s.Recent...)
	s.Targets = append([]Target(nil), d.targets...)
	return s
}

// Run works through the queue until ctx is cancelled.
func (d *Detector) Run(ctx context.Context) {
	for {
		if err := d.runTargets(ctx); err != nil {
			return
		}
		d.mu.Lock()
		var lib string
		if len(d.queue) > 0 {
			lib, d.queue = d.queue[0], d.queue[1:]
		}
		pending := len(d.targets) > 0
		d.mu.Unlock()
		if lib == "" {
			if pending {
				continue // asked for while the last target was being worked on
			}
			select {
			case <-ctx.Done():
				return
			case <-d.wake:
				continue
			}
		}
		if err := d.analyseLibrary(ctx, lib); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("detection failed", "library_id", lib, "err", err)
		}
		if ctx.Err() != nil {
			return
		}
		d.mu.Lock()
		if len(d.queue) == 0 {
			d.Gate.EndRunNow() // "Analyse now" lasts until the work it asked for is done
		}
		d.mu.Unlock()
	}
}

// setStatus changes the status and notifies, at most every progressEvery
// unless force — a run starting or ending must always be seen.
func (d *Detector) setStatus(force bool, fn func(*Status)) {
	d.mu.Lock()
	fn(&d.status)
	if !force && time.Since(d.notified) < progressEvery {
		d.mu.Unlock()
		return
	}
	d.notified = time.Now()
	s := d.snapshot()
	d.mu.Unlock()
	if d.OnProgress != nil {
		d.OnProgress(s)
	}
}

// unit is one piece of a library run: a season (both kinds) or a film.
type unit struct {
	season string
	film   *store.SegmentFilm
}

func (d *Detector) analyseLibrary(ctx context.Context, libraryID string) error {
	if d.Available != nil && !d.Available(libraryID) {
		// Debug: the storage monitor has already warned once that the share is down.
		logger.Debug("storage unreachable, detection skipped until it is back", "library_id", libraryID)
		return nil
	}
	// Every season needing either kind, each once and in the order first
	// seen, so a season is finished — intro and credits — before the next.
	var units []unit
	seen := map[string]bool{}
	total := 0
	for _, sp := range specs {
		ids, err := d.Store.SeasonsNeedingSegments(ctx, libraryID, sp.kind)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				units = append(units, unit{season: id})
			}
		}
		n, err := d.Store.CountEpisodesNeedingSegments(ctx, libraryID, sp.kind)
		if err != nil {
			return err
		}
		total += n
	}
	seasons := len(units)
	// Then the films, whose credits come from their chapter names.
	films, err := d.Store.FilmsNeedingCredits(ctx, libraryID)
	if err != nil {
		return err
	}
	for i := range films {
		units = append(units, unit{film: &films[i]})
	}
	if len(units) == 0 {
		return nil
	}
	started := time.Now()
	d.mu.Lock()
	d.reading = libraryID
	d.mu.Unlock()
	d.setStatus(true, func(s *Status) {
		*s = Status{Running: true, LibraryID: libraryID, StartedAt: started,
			SeasonsTotal: seasons, EpisodesTotal: total, FilmsTotal: len(films), Last: s.Last,
			Paused: s.Paused, PauseReason: s.PauseReason}
	})
	var runErr error
	for i := 0; i < len(units); {
		// Anything asked for by name goes first.
		if runErr = d.runTargets(ctx); runErr != nil {
			break
		}
		// Each unit can be stopped for targeted work while the schedule
		// holds it (see maybePreempt); it is then done again afterwards,
		// having saved nothing.
		uctx, cancel := context.WithCancel(ctx)
		d.mu.Lock()
		d.preempt = cancel
		d.mu.Unlock()
		err := d.analyseUnit(uctx, units[i], false)
		preempted := uctx.Err() != nil && ctx.Err() == nil
		d.mu.Lock()
		d.preempt = nil
		d.mu.Unlock()
		cancel()
		if err != nil && preempted {
			continue // stopped for targeted work
		}
		if runErr = err; runErr != nil {
			break
		}
		d.setStatus(true, func(s *Status) {
			if units[i].film != nil {
				s.FilmsDone++
			} else {
				s.SeasonsDone++
			}
			s.Current = nil
		})
		i++
	}
	d.mu.Lock()
	d.reading = ""
	d.mu.Unlock()
	d.setStatus(true, func(s *Status) {
		s.Last = &RunSummary{
			LibraryID: libraryID, FinishedAt: time.Now(), Seconds: int(time.Since(started).Seconds()),
			Seasons: s.SeasonsDone, Episodes: s.EpisodesDone, Films: s.FilmsDone,
			IntrosFound: s.IntrosFound, CreditsFound: s.CreditsFound, Unreadable: s.Unreadable,
			Cancelled: runErr != nil,
		}
		// A manual pause outlives the run, so the page still says so.
		manual := d.Gate.Policy.Paused()
		*s = Status{Last: s.Last, Recent: s.Recent, Paused: manual}
		if manual {
			s.PauseReason = "manual"
		}
	})
	if runErr != nil {
		return runErr
	}
	logger.Info("detection finished", "library_id", libraryID, "seasons", seasons, "films", len(films),
		"took", time.Since(started).Round(time.Second))
	return nil
}

// analyseUnit works through one season, both kinds, or one film.
func (d *Detector) analyseUnit(ctx context.Context, u unit, targeted bool) error {
	if u.film != nil {
		return d.analyseFilm(ctx, *u.film, targeted)
	}
	for _, sp := range specs {
		if err := d.analyseSeason(ctx, u.season, sp, targeted); err != nil {
			return err
		}
	}
	return nil
}

// runTargets works through the seasons and films asked for by name, until
// none is left. Only the context ending stops it: a target that cannot be
// finished — its share gone — is dropped, and its analyses stay due for the
// library queue to take up.
func (d *Detector) runTargets(ctx context.Context) error {
	for {
		d.mu.Lock()
		if len(d.targets) == 0 {
			d.mu.Unlock()
			return nil
		}
		d.targets[0].Working = true
		t := d.targets[0]
		prev := d.reading
		d.reading = t.LibraryID
		d.mu.Unlock()
		d.setStatus(true, func(*Status) {})

		err := d.analyseTarget(ctx, t)

		d.mu.Lock()
		d.reading = prev
		if len(d.targets) > 0 && d.targets[0].ItemID == t.ItemID && d.targets[0].Working {
			d.targets = d.targets[1:]
		}
		d.mu.Unlock()
		d.setStatus(true, func(s *Status) { s.Current = nil })
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			logger.Warn("detection of a title asked for stopped", "item_id", t.ItemID, "title", t.Title, "err", err)
		}
	}
}

// analyseTarget analyses one season or film asked for by name.
func (d *Detector) analyseTarget(ctx context.Context, t Target) error {
	if d.Available != nil && t.LibraryID != "" && !d.Available(t.LibraryID) {
		return errStorageGone
	}
	if t.Kind == TargetFilm {
		f, err := d.Store.FilmCreditsState(ctx, t.ItemID)
		if errors.Is(err, store.ErrNotFound) {
			return nil // deleted since
		}
		if err != nil {
			return err
		}
		return d.analyseFilm(ctx, f, true)
	}
	return d.analyseUnit(ctx, unit{season: t.ItemID}, true)
}

// analyseFilm finds a film's end credits from its chapter names (see
// media.CreditsChapter). Opening the file reads only its header, so this
// costs a fraction of what an episode's comparison does.
func (d *Detector) analyseFilm(ctx context.Context, f store.SegmentFilm, targeted bool) error {
	if f.Analysed {
		return nil
	}
	d.setStatus(true, func(s *Status) {
		s.Current = &SeasonProgress{Series: f.Title, Film: true, Kind: store.SegmentCredits, Phase: "reading", Total: 1}
	})
	if err := d.gateFor(ctx, targeted); err != nil {
		return err
	}
	timeout := d.DecodeTimeout
	if timeout <= 0 {
		timeout = decodeTimeout
	}
	info, err := library.Within(timeout, func() (media.Info, error) { return d.Probe(f.Path) })
	if errors.Is(err, library.ErrNotResponding) {
		logger.Warn("file not responding, stopping until the share is back", "path", f.Path)
		return errStorageGone
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Left unanalysed, to be tried again on the next pass.
		logger.Warn("reading chapters failed", "path", f.Path, "err", err)
		d.setStatus(false, func(s *Status) { s.Unreadable++ })
		return nil
	}
	runtime := f.RuntimeSecs
	if runtime <= 0 {
		runtime = info.RuntimeSecs
	}
	var seg *store.Segment
	if start, end, ok := media.CreditsChapter(info.Chapters, runtime); ok {
		seg = &store.Segment{Kind: store.SegmentCredits, StartSecs: start, EndSecs: end}
	}
	if err := d.Store.SaveSegmentAnalysis(ctx, f.ID, store.SegmentCredits, f.FileMtime, seg); err != nil {
		return err
	}
	r := Result{ItemID: f.ID, Kind: store.SegmentCredits, Series: f.Title, Film: true, Found: seg != nil}
	if seg != nil {
		r.StartSecs, r.EndSecs = seg.StartSecs, seg.EndSecs
	}
	d.record(r, targeted)
	return nil
}

// record adds an outcome to the status. Targeted work is not part of the
// library run whose counts the status carries, so it only joins the list
// of recent outcomes.
func (d *Detector) record(r Result, targeted bool) {
	d.setStatus(false, func(s *Status) {
		if !targeted {
			if !r.Film {
				s.EpisodesDone++
			}
			switch {
			case r.Found && r.Kind == store.SegmentIntro:
				s.IntrosFound++
			case r.Found:
				s.CreditsFound++
			}
		}
		s.Recent = append([]Result{r}, s.Recent...)
		if len(s.Recent) > recentResults {
			s.Recent = s.Recent[:recentResults]
		}
		if s.Current != nil {
			s.Current.Done++
		}
	})
}

// analyseSeason finds one kind of segment for a season's unanalysed
// episodes, recording what it found in the status as it goes.
//
// Three phases: every file the season needs is fingerprinted in parallel
// (each once, however many episodes it is compared with), then each
// unanalysed episode is compared with its neighbours in parallel, then the
// results are saved one by one, SQLite taking a single writer.
//
// targeted is set for a season asked for by name, which goes ahead whatever
// the schedule says and is not counted in a library run's progress.
func (d *Detector) analyseSeason(ctx context.Context, seasonID string, sp spec, targeted bool) error {
	eps, err := d.Store.SeasonSegmentState(ctx, seasonID, sp.kind)
	if err != nil {
		return err
	}
	// A lone episode has nothing in its own season to be compared with, so
	// it is compared with episodes of the seasons either side: a series'
	// titles rarely change between seasons. They are partners only — each
	// is analysed with its own season — so they count as analysed here.
	// With no other season either, the episode is left unrecorded rather
	// than marked analysed, so that it is looked at again once it has
	// company.
	if len(eps) == 1 && !eps[0].Analysed {
		others, err := d.Store.AdjacentSeasonEpisodes(ctx, seasonID, partners)
		if err != nil {
			return err
		}
		for _, ep := range others {
			ep.Analysed = true
			eps = append(eps, ep)
		}
	}
	if len(eps) < 2 {
		return nil
	}
	var todo []int
	need := map[int]bool{}
	for i, ep := range eps {
		if ep.Analysed {
			continue
		}
		todo = append(todo, i)
		need[i] = true
		for _, j := range neighbours(i, len(eps), partners) {
			need[j] = true
		}
	}
	if len(todo) == 0 {
		return nil
	}
	series, seasonNum := d.seasonLabel(ctx, seasonID)
	phase := func(name string, total int) {
		d.setStatus(true, func(s *Status) {
			s.Current = &SeasonProgress{Series: series, Season: seasonNum, Kind: sp.kind, Phase: name, Total: total}
		})
	}

	// A file that cannot be read stays nil, and is simply not compared.
	fps := make([]*media.Fingerprint, len(eps))
	var idx []int
	for i := range need {
		idx = append(idx, i)
	}
	phase("reading", len(idx))
	// With a rate limit files are read one at a time, so the limit is the
	// whole of detection's traffic rather than per reader.
	readers := workers()
	if d.Gate.Policy.RateLimit() > 0 {
		readers = 1
	}
	// The first file to find the share gone, or to stall on it, stops the
	// rest: no other reader starts, and nothing from this season is saved.
	var stopMu sync.Mutex
	var stopErr error
	stop := func(err error) {
		stopMu.Lock()
		if stopErr == nil {
			stopErr = err
		}
		stopMu.Unlock()
	}
	stopped := func() error {
		stopMu.Lock()
		defer stopMu.Unlock()
		return stopErr
	}
	timeout := d.DecodeTimeout
	if timeout <= 0 {
		timeout = decodeTimeout
	}
	parallelN(len(idx), readers, func(k int) {
		if stopped() != nil {
			return
		}
		if err := d.gateFor(ctx, targeted); err != nil {
			stop(err)
			return
		}
		i := idx[k]
		unreadable := 0
		// A file with no known length has no end to count back from, and
		// is simply not compared.
		if start, dur, ok := sp.window(eps[i].RuntimeSecs); ok {
			// A decode that stalls is a share gone quiet: the run stops
			// there rather than moving on to the next file, which would
			// only stall alongside it. The file is tried again next time.
			began := time.Now()
			fp, err := library.Within(timeout, func() (media.Fingerprint, error) {
				return d.Fingerprint(ctx, eps[i].Path, start, dur)
			})
			if errors.Is(err, library.ErrNotResponding) {
				logger.Warn("file not responding, stopping until the share is back", "path", eps[i].Path)
				stop(errStorageGone)
				return
			}
			d.pace(ctx, eps[i], dur, time.Since(began))
			if err == nil {
				fps[i] = &fp
			} else if ctx.Err() == nil {
				logger.Warn("fingerprint failed", "path", eps[i].Path, "err", err)
				unreadable = 1
			}
		}
		d.setStatus(false, func(s *Status) {
			s.Unreadable += unreadable
			if s.Current != nil {
				s.Current.Done++
			}
		})
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := stopped(); err != nil {
		return err
	}

	phase("comparing", len(todo))
	segs := make([]*store.Segment, len(todo))
	parallel(len(todo), func(k int) {
		i := todo[k]
		if fps[i] != nil {
			var cands []Match
			for _, j := range neighbours(i, len(eps), partners) {
				if fps[j] == nil {
					continue
				}
				if m, ok := LongestShared(*fps[i], *fps[j], sp.minSecs, sp.maxSecs); ok {
					cands = append(cands, m)
				}
			}
			if m, ok := pick(cands, sp.minAgree); ok {
				segs[k] = &store.Segment{Kind: sp.kind, StartSecs: m.AStart, EndSecs: m.AEnd}
			}
		}
		d.setStatus(false, func(s *Status) {
			if s.Current != nil {
				s.Current.Done++
			}
		})
	})

	phase("saving", len(todo))
	for k, i := range todo {
		// An episode whose own file could not be read is left unanalysed,
		// so the next pass — after a share comes back, say — tries again
		// instead of recording "no intro" for a file never looked at.
		if fps[i] == nil {
			continue
		}
		if err := d.Store.SaveSegmentAnalysis(ctx, eps[i].ID, sp.kind, eps[i].FileMtime, segs[k]); err != nil {
			return err
		}
		r := Result{ItemID: eps[i].ID, Kind: sp.kind, Series: series, Season: seasonNum, Episode: eps[i].EpisodeNum}
		if seg := segs[k]; seg != nil {
			r.Found, r.StartSecs, r.EndSecs = true, seg.StartSecs, seg.EndSecs
		}
		d.record(r, targeted)
	}
	return nil
}

// seasonLabel names a season for the status: its series' title and number.
// Best effort — a lookup failure leaves the label blank, not the run failed.
func (d *Detector) seasonLabel(ctx context.Context, seasonID string) (string, int) {
	season, err := d.Store.Item(ctx, seasonID)
	if err != nil {
		return "", 0
	}
	if series, err := d.Store.Item(ctx, season.ParentID); err == nil {
		return series.Title, season.SeasonNum
	}
	return "", season.SeasonNum
}

// workers is how many files are decoded, or episodes compared, at once.
// Decoding is mostly reading the file, so a few at a time keeps a disk or a
// network share busy without thrashing it; comparing is pure CPU, but the
// transcoder needs the cores more than this background job does.
func workers() int {
	return max(1, min(runtime.NumCPU()/2, 4))
}

// parallel runs fn(0..n-1) on up to workers() goroutines and waits for all.
func parallel(n int, fn func(int)) { parallelN(n, workers(), fn) }

// parallelN is parallel with a given number of goroutines.
func parallelN(n, w int, fn func(int)) {
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(max(w, 1), n) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range next {
				fn(k)
			}
		}()
	}
	for k := 0; k < n; k++ {
		next <- k
	}
	close(next)
	wg.Wait()
}

// neighbours returns up to n indices other than i, nearest first and
// alternating after and before: an episode's intro is likeliest to match
// the episodes either side of it, which were made alongside it.
func neighbours(i, total, n int) []int {
	var out []int
	for d := 1; len(out) < n && (i+d < total || i-d >= 0); d++ {
		if i+d < total {
			out = append(out, i+d)
		}
		if i-d >= 0 && len(out) < n {
			out = append(out, i-d)
		}
	}
	return out
}
