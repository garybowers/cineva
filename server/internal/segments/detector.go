package segments

import (
	"context"
	"errors"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

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
	// Current is the season being worked on.
	Current *SeasonProgress `json:"current,omitempty"`
	// Recent is what the last episodes analysed came to, newest first —
	// which also gives anyone testing an episode to try.
	Recent []Result `json:"recent,omitempty"`
	// Last summarises the most recent finished run, so the page has
	// something to say once the work is done.
	Last *RunSummary `json:"last,omitempty"`
}

// SeasonProgress is where the detector is within one season.
type SeasonProgress struct {
	Series string `json:"series"`
	Season int    `json:"season"`
	Kind   string `json:"kind"`
	// Phase is "reading" (decoding audio), "comparing" or "saving".
	Phase string `json:"phase"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// Result is one episode's outcome.
type Result struct {
	ItemID    string  `json:"itemId"`
	Kind      string  `json:"kind"`
	Series    string  `json:"series"`
	Season    int     `json:"season"`
	Episode   int     `json:"episode"`
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

// Detector finds intros in TV libraries in the background, one library at a
// time. Work is incremental: only episodes whose current file has not been
// analysed are, so after the first pass over a library a run costs the new
// episodes and the neighbours they are compared with.
type Detector struct {
	Store *store.Store
	// Fingerprint reads a stretch of a file's audio; media.FingerprintFile
	// outside tests.
	Fingerprint func(ctx context.Context, path string, start, dur float64) (media.Fingerprint, error)
	// OnProgress, when set, is called as the status changes.
	OnProgress func(Status)
	// Available, when set, says whether a library's storage can be reached;
	// a library that cannot is skipped rather than left to hang the run.
	Available func(libraryID string) bool
	// Busy, when set, names what detection should give way to — "playing",
	// "scanning" — or returns "". Reading a quarter of every episode is
	// enough traffic to starve playback from the same share, so detection
	// waits while anything else needs it.
	Busy func() string
	// Cooldown is how long after the last busy moment detection resumes, so
	// it does not dart in between one episode ending and the next starting.
	Cooldown time.Duration

	paused    atomic.Bool  // by an admin
	rateLimit atomic.Int64 // bytes per second; 0 = as fast as it can

	mu       sync.Mutex
	queue    []string
	status   Status
	wake     chan struct{}
	notified time.Time
}

func NewDetector(st *store.Store) *Detector {
	return &Detector{Store: st, Fingerprint: media.FingerprintFile, Cooldown: time.Minute, wake: make(chan struct{}, 1)}
}

// SetPaused pauses or resumes detection. A pause holds before the next file;
// the one being read finishes first.
func (d *Detector) SetPaused(p bool) {
	d.paused.Store(p)
	d.setStatus(true, func(s *Status) {
		if p {
			s.Paused, s.PauseReason = true, "manual"
		} else if s.PauseReason == "manual" {
			s.Paused, s.PauseReason = false, ""
		}
	})
}

// SetRateLimit caps how fast detection reads media, in bytes per second;
// 0 removes the cap. Takes effect from the next file.
func (d *Detector) SetRateLimit(bytesPerSec int64) { d.rateLimit.Store(max(0, bytesPerSec)) }

// gate holds before each file while detection is paused or something else
// needs the storage, and returns when it may read.
func (d *Detector) gate(ctx context.Context) error {
	var lastBusy time.Time
	for {
		reason := ""
		if d.paused.Load() {
			reason = "manual"
		} else if d.Busy != nil {
			if r := d.Busy(); r != "" {
				reason, lastBusy = r, time.Now()
			}
		}
		// Busy a moment ago counts as busy until the cooldown has passed.
		if reason == "" && !lastBusy.IsZero() && time.Since(lastBusy) < d.Cooldown {
			reason = d.Status().PauseReason
		}
		if reason == "" {
			if d.Status().Paused {
				d.setStatus(true, func(s *Status) { s.Paused, s.PauseReason = false, "" })
			}
			return nil
		}
		if st := d.Status(); !st.Paused || st.PauseReason != reason {
			d.setStatus(true, func(s *Status) { s.Paused, s.PauseReason = true, reason })
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// pace sleeps after reading a file for long enough that detection averages
// no more than the rate limit. What was read is estimated from the file's
// size and the share of its running time the window covered: the demuxer
// reads the video around the audio, so that is close to the truth.
func (d *Detector) pace(ctx context.Context, ep store.SegmentEpisode, dur float64, took time.Duration) {
	limit := d.rateLimit.Load()
	if limit <= 0 || ep.SizeBytes <= 0 || ep.RuntimeSecs <= 0 {
		return
	}
	read := float64(ep.SizeBytes) * min(1, dur/float64(ep.RuntimeSecs))
	wait := time.Duration(read/float64(limit)*float64(time.Second)) - took
	if wait <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
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
	return s
}

// Run works through the queue until ctx is cancelled.
func (d *Detector) Run(ctx context.Context) {
	for {
		d.mu.Lock()
		var lib string
		if len(d.queue) > 0 {
			lib, d.queue = d.queue[0], d.queue[1:]
		}
		d.mu.Unlock()
		if lib == "" {
			select {
			case <-ctx.Done():
				return
			case <-d.wake:
				continue
			}
		}
		if err := d.analyseLibrary(ctx, lib); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("intro detection %s: %v", lib, err)
		}
		if ctx.Err() != nil {
			return
		}
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

func (d *Detector) analyseLibrary(ctx context.Context, libraryID string) error {
	if d.Available != nil && !d.Available(libraryID) {
		log.Printf("segment detection %s: storage unreachable, skipped until it is back", libraryID)
		return nil
	}
	// Every season needing either kind, each once and in the order first
	// seen, so a season is finished — intro and credits — before the next.
	var seasons []string
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
				seasons = append(seasons, id)
			}
		}
		n, err := d.Store.CountEpisodesNeedingSegments(ctx, libraryID, sp.kind)
		if err != nil {
			return err
		}
		total += n
	}
	if len(seasons) == 0 {
		return nil
	}
	started := time.Now()
	d.setStatus(true, func(s *Status) {
		*s = Status{Running: true, LibraryID: libraryID, StartedAt: started,
			SeasonsTotal: len(seasons), EpisodesTotal: total, Last: s.Last,
			Paused: s.Paused, PauseReason: s.PauseReason}
	})
	var runErr error
seasons:
	for _, season := range seasons {
		for _, sp := range specs {
			if runErr = d.analyseSeason(ctx, season, sp); runErr != nil {
				break seasons
			}
		}
		d.setStatus(true, func(s *Status) { s.SeasonsDone++; s.Current = nil })
	}
	d.setStatus(true, func(s *Status) {
		s.Last = &RunSummary{
			LibraryID: libraryID, FinishedAt: time.Now(), Seconds: int(time.Since(started).Seconds()),
			Seasons: s.SeasonsDone, Episodes: s.EpisodesDone,
			IntrosFound: s.IntrosFound, CreditsFound: s.CreditsFound, Unreadable: s.Unreadable,
			Cancelled: runErr != nil,
		}
		// A manual pause outlives the run, so the page still says so.
		manual := d.paused.Load()
		*s = Status{Last: s.Last, Recent: s.Recent, Paused: manual}
		if manual {
			s.PauseReason = "manual"
		}
	})
	if runErr != nil {
		return runErr
	}
	log.Printf("intro detection %s: %d season(s) in %s", libraryID, len(seasons), time.Since(started).Round(time.Second))
	return nil
}

// analyseSeason finds one kind of segment for a season's unanalysed
// episodes, recording what it found in the status as it goes.
//
// Three phases: every file the season needs is fingerprinted in parallel
// (each once, however many episodes it is compared with), then each
// unanalysed episode is compared with its neighbours in parallel, then the
// results are saved one by one, SQLite taking a single writer.
func (d *Detector) analyseSeason(ctx context.Context, seasonID string, sp spec) error {
	eps, err := d.Store.SeasonSegmentState(ctx, seasonID, sp.kind)
	if err != nil {
		return err
	}
	// A lone episode has nothing to be compared with. It is left
	// unrecorded rather than marked analysed, so that it is looked at again
	// once the season has company.
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
	if d.rateLimit.Load() > 0 {
		readers = 1
	}
	parallelN(len(idx), readers, func(k int) {
		if d.gate(ctx) != nil {
			return
		}
		i := idx[k]
		unreadable := 0
		// A file with no known length has no end to count back from, and
		// is simply not compared.
		if start, dur, ok := sp.window(eps[i].RuntimeSecs); ok {
			// A decode that stalls on a share gone quiet is given up on, so
			// one stuck file cannot hold the whole run; the file counts as
			// unreadable this time and is tried again on the next pass.
			began := time.Now()
			fp, err := library.Within(decodeTimeout, func() (media.Fingerprint, error) {
				return d.Fingerprint(ctx, eps[i].Path, start, dur)
			})
			d.pace(ctx, eps[i], dur, time.Since(began))
			if err == nil {
				fps[i] = &fp
			} else if ctx.Err() == nil {
				log.Printf("segment detection: %s: %v", eps[i].Path, err)
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
		d.setStatus(false, func(s *Status) {
			s.EpisodesDone++
			switch {
			case r.Found && r.Kind == store.SegmentIntro:
				s.IntrosFound++
			case r.Found:
				s.CreditsFound++
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
