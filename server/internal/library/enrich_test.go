package library

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// A film library of n titles, every one the provider knows.
func filmFixture(t *testing.T, n int) (string, *fakeVideo) {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < n; i++ {
		dir := filepath.Join(root, fmt.Sprintf("Film %03d (2001)", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("Film %03d (2001).mkv", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fv := newFakeVideo()
	fv.movie = func(title string, year int) (metadata.VideoResult, error) {
		// A distinct id per title: films sharing one would be grouped as
		// versions of each other.
		var n int
		fmt.Sscanf(title, "Film %d", &n)
		return metadata.VideoResult{TmdbID: 1000 + n, Title: title, Year: year, Overview: "found"}, nil
	}
	return root, fv
}

// A TV library of n series with eps episodes each, every one the provider
// knows, with a title for every episode.
func tvFixture(t *testing.T, n, eps int) (string, *fakeVideo) {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < n; i++ {
		dir := filepath.Join(root, fmt.Sprintf("Show %03d (2001)", i), "Season 01")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for e := 1; e <= eps; e++ {
			name := fmt.Sprintf("Show %03d - S01E%02d.mkv", i, e)
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	fv := newFakeVideo()
	fv.tv = func(title string, year int) (metadata.VideoResult, error) {
		var n int
		fmt.Sscanf(title, "Show %d", &n)
		return metadata.VideoResult{TmdbID: 2000 + n, Title: title, Year: year, Overview: "found"}, nil
	}
	fv.season = func(tvID, season int) (metadata.SeasonResult, error) {
		sr := metadata.SeasonResult{TmdbID: tvID*10 + season, Overview: "season"}
		for e := 1; e <= eps; e++ {
			sr.Episodes = append(sr.Episodes, metadata.EpisodeResult{Num: e, Title: fmt.Sprintf("Ep %d", e)})
		}
		return sr, nil
	}
	return root, fv
}

func enrichManager(t *testing.T, st *store.Store, fv *fakeVideo, bus *events.Bus) *Manager {
	t.Helper()
	m := NewManager(st, fv, t.TempDir())
	m.Events = bus
	m.Probe = func(string) (media.Info, error) {
		return media.Info{RuntimeSecs: 5400, Container: "mkv", VideoCodec: "h264"}, nil
	}
	m.retryPace = 0
	m.enrichDelay = 0
	m.roundWait = 20 * time.Millisecond
	return m
}

// blockSecondBatch makes the manager's probe hold the register pass open
// at its second batch of files until release is closed, so a test can
// watch the job run alongside the pass.
func blockSecondBatch(m *Manager) (release chan struct{}) {
	release = make(chan struct{})
	var probed atomic.Int32
	inner := m.Probe
	m.Probe = func(path string) (media.Info, error) {
		if probed.Add(1) > probeBatchSize {
			<-release
		}
		return inner(path)
	}
	return release
}

func matchedCount(t *testing.T, st *store.Store, libID string, kind store.ItemKind) (matched, total int) {
	t.Helper()
	items, err := st.ItemsByLibrary(context.Background(), libID)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Kind == kind {
			total++
			if it.TmdbID != 0 {
				matched++
			}
		}
	}
	return matched, total
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func scanRow(t *testing.T, st *store.Store, id string) store.Scan {
	t.Helper()
	sc, err := st.ScanByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

// A scan with many new films registers them without the provider and the
// job then matches them, with progress on the scan row and events for
// the clients, and the readers start once, when the job ends.
func TestBigScanDefersLookups(t *testing.T) {
	root, fv := filmFixture(t, enrichThreshold+5)
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	m := enrichManager(t, st, fv, bus)
	var doneCalls atomic.Int32
	m.OnScanDone = func(store.Library) { doneCalls.Add(1) }

	// Watch the events: the scan row's phases and the items counts. The
	// bus drops events a slow subscriber has not taken, so they are read
	// on their own goroutine as they come.
	subCtx, unsubscribe := context.WithCancel(ctx)
	defer unsubscribe()
	eventsCh := bus.Subscribe(subCtx)
	var mu sync.Mutex
	var phases []string
	var lastItems itemsEvent
	go func() {
		for e := range eventsCh {
			mu.Lock()
			switch d := e.Data.(type) {
			case scanEvent:
				if len(phases) == 0 || phases[len(phases)-1] != d.Phase {
					phases = append(phases, d.Phase)
				}
			case itemsEvent:
				if d.ToEnrich > 0 {
					lastItems = d
				}
			}
			mu.Unlock()
		}
	}()

	scan, err := m.StartScan(ctx, lib, false)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the register pass", func() bool { return scanRow(t, st, scan.ID).Status != "running" })
	m.WaitEnrich(lib.ID)
	time.Sleep(50 * time.Millisecond) // the last events land

	if got := fv.calls("movie"); got != enrichThreshold+5 {
		t.Errorf("provider asked %d times, want %d (once per title, by the job)", got, enrichThreshold+5)
	}
	matched, total := matchedCount(t, st, lib.ID, store.KindMovie)
	if matched != total || total != enrichThreshold+5 {
		t.Errorf("matched %d of %d", matched, total)
	}
	sc := scanRow(t, st, scan.ID)
	if sc.Status != "completed" || sc.Phase != store.PhaseDone || sc.Enriched != total || sc.ToEnrich != total {
		t.Errorf("scan row after: %+v", sc)
	}
	if n := doneCalls.Load(); n != 1 {
		t.Errorf("OnScanDone called %d times, want once", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(phases) < 2 || phases[0] != store.PhaseRegistering || phases[len(phases)-1] != store.PhaseDone {
		t.Errorf("phases seen: %v", phases)
	}
	if lastItems.Enriched != total || lastItems.ToEnrich != total {
		t.Errorf("last items event %+v", lastItems)
	}
}

// The job starts while the register pass is still running: the first
// batch's films have their details before the pass has read the second
// batch, the scan row counts them under "registering", and the films
// registered after the job began are picked up by a later round, so the
// job ends with every title matched and the readers started once.
func TestEnrichRunsAlongsideRegisterPass(t *testing.T) {
	const films = 2*probeBatchSize + 1 // three batches
	root, fv := filmFixture(t, films)
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	m := enrichManager(t, st, fv, events.NewBus())
	release := blockSecondBatch(m)
	var doneCalls atomic.Int32
	m.OnScanDone = func(store.Library) { doneCalls.Add(1) }

	scan, err := m.StartScan(ctx, lib, false)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first batch's details", func() bool {
		matched, _ := matchedCount(t, st, lib.ID, store.KindMovie)
		return matched == probeBatchSize
	})
	sc := scanRow(t, st, scan.ID)
	if sc.Status != "running" {
		t.Fatalf("register pass ended before the job matched anything: %+v", sc)
	}
	waitFor(t, "the progress on the row", func() bool {
		sc := scanRow(t, st, scan.ID)
		return sc.Phase == store.PhaseRegistering && sc.Enriched == probeBatchSize
	})
	if n := doneCalls.Load(); n != 0 {
		t.Errorf("readers started during the pass")
	}

	close(release)
	waitFor(t, "the register pass", func() bool { return scanRow(t, st, scan.ID).Status != "running" })
	m.WaitEnrich(lib.ID)

	if matched, total := matchedCount(t, st, lib.ID, store.KindMovie); matched != films || total != films {
		t.Errorf("matched %d of %d, want all %d", matched, total, films)
	}
	if got := fv.calls("movie"); got != films {
		t.Errorf("provider asked %d times, want %d", got, films)
	}
	sc = scanRow(t, st, scan.ID)
	if sc.Status != "completed" || sc.Phase != store.PhaseDone || sc.Enriched != films || sc.ToEnrich != films {
		t.Errorf("scan row after: %+v", sc)
	}
	if n := doneCalls.Load(); n != 1 {
		t.Errorf("OnScanDone called %d times, want once", n)
	}
}

// The job's lookups run several at a time, through its pool of workers.
func TestEnrichLooksUpSeveralAtOnce(t *testing.T) {
	root, fv := filmFixture(t, enrichThreshold+10)
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	// The first enrichWorkers lookups wait for one another: with fewer
	// workers than that they would never all be in flight at once.
	var arrived atomic.Int32
	var inFlight, peak atomic.Int32
	full := make(chan struct{})
	var timedOut atomic.Bool
	inner := fv.movie
	fv.movie = func(title string, year int) (metadata.VideoResult, error) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		if arrived.Add(1) <= enrichWorkers {
			if arrived.Load() == enrichWorkers {
				close(full)
			}
			select {
			case <-full:
			case <-time.After(3 * time.Second):
				timedOut.Store(true)
			}
		}
		return inner(title, year)
	}
	m := enrichManager(t, st, fv, events.NewBus())
	scan, err := m.StartScan(ctx, lib, false)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the register pass", func() bool { return scanRow(t, st, scan.ID).Status != "running" })
	m.WaitEnrich(lib.ID)

	if timedOut.Load() {
		t.Errorf("fewer than %d lookups ran at once", enrichWorkers)
	}
	if p := peak.Load(); p != enrichWorkers {
		t.Errorf("%d lookups in flight at the peak, want %d", p, enrichWorkers)
	}
	if matched, total := matchedCount(t, st, lib.ID, store.KindMovie); matched != total {
		t.Errorf("matched %d of %d", matched, total)
	}
}

// A series whose episodes the pass is still registering is left for a
// later round, so a series is matched (and its episodes filled in from
// the provider) only once all of its episodes are there.
func TestEnrichWaitsForSeriesStillRegistering(t *testing.T) {
	const eps = 3 // a batch of 32 ends part way through a series
	root, fv := tvFixture(t, enrichThreshold+5, eps)
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	m := enrichManager(t, st, fv, events.NewBus())
	release := blockSecondBatch(m)

	scan, err := m.StartScan(ctx, lib, false)
	if err != nil {
		t.Fatal(err)
	}
	// The first batch holds the whole of the first ten series and the
	// start of the eleventh, which must wait.
	whole := probeBatchSize / eps
	waitFor(t, "the first batch's series", func() bool {
		matched, _ := matchedCount(t, st, lib.ID, store.KindSeries)
		return matched == whole
	})
	time.Sleep(100 * time.Millisecond) // several rounds
	if matched, _ := matchedCount(t, st, lib.ID, store.KindSeries); matched != whole {
		t.Errorf("%d series matched while the pass was inside the %dth, want %d", matched, whole+1, whole)
	}
	open := m.openSeriesFor(lib.ID)
	if filepath.Base(open) != fmt.Sprintf("Show %03d (2001)", whole) {
		t.Errorf("open series is %q", open)
	}

	close(release)
	waitFor(t, "the register pass", func() bool { return scanRow(t, st, scan.ID).Status != "running" })
	m.WaitEnrich(lib.ID)

	matched, total := matchedCount(t, st, lib.ID, store.KindSeries)
	if matched != total || total != enrichThreshold+5 {
		t.Errorf("matched %d of %d series", matched, total)
	}
	items, err := st.ItemsByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	episodes, titled := 0, 0
	for _, it := range items {
		if it.Kind == store.KindEpisode {
			episodes++
			if strings.HasPrefix(it.Title, "Ep ") {
				titled++
			}
		}
	}
	if episodes != (enrichThreshold+5)*eps || titled != episodes {
		t.Errorf("%d of %d episodes have the provider's title", titled, episodes)
	}
}

// A scan with a few new films looks them up inline, as before, and the
// readers start as soon as it ends.
func TestSmallScanStaysInline(t *testing.T) {
	root, fv := filmFixture(t, 3)
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	m := enrichManager(t, st, fv, nil)
	var doneCalls atomic.Int32
	m.OnScanDone = func(store.Library) { doneCalls.Add(1) }
	res, err := m.RunScanSync(ctx, lib, false)
	if err != nil || res.Status != "completed" {
		t.Fatalf("scan: %v %+v", err, res)
	}
	if fv.calls("movie") != 3 {
		t.Errorf("provider asked %d times during the scan, want 3", fv.calls("movie"))
	}
	if res.Phase != store.PhaseDone || res.ToEnrich != 0 {
		t.Errorf("scan row: %+v", res)
	}
	if n := doneCalls.Load(); n != 1 {
		t.Errorf("OnScanDone called %d times, want once", n)
	}
	matched, total := matchedCount(t, st, lib.ID, store.KindMovie)
	if matched != 3 || total != 3 {
		t.Errorf("matched %d of %d", matched, total)
	}
}

// Without an event bus (the CLI) a big scan still returns with its details
// fetched: the job runs in line, after the pass.
func TestBigScanInlineWithoutEvents(t *testing.T) {
	root, fv := filmFixture(t, enrichThreshold+1)
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	m := enrichManager(t, st, fv, nil)
	var doneCalls atomic.Int32
	m.OnScanDone = func(store.Library) { doneCalls.Add(1) }
	res, err := m.RunScanSync(ctx, lib, false)
	if err != nil || res.Status != "completed" {
		t.Fatalf("scan: %v %+v", err, res)
	}
	if matched, total := matchedCount(t, st, lib.ID, store.KindMovie); matched != total || total != enrichThreshold+1 {
		t.Errorf("matched %d of %d", matched, total)
	}
	if res.Phase != store.PhaseDone || res.Enriched != enrichThreshold+1 {
		t.Errorf("scan row: %+v", res)
	}
	if n := doneCalls.Load(); n != 1 {
		t.Errorf("OnScanDone called %d times, want once", n)
	}
}

// Stopping the fetch once the files are registered stops the job, and a
// rescan while the job runs does not start a second one.
func TestEnrichCancelAndNoSecondJob(t *testing.T) {
	root, fv := filmFixture(t, enrichThreshold+10)
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	// A slow provider, so the job is observable while it runs.
	release := make(chan struct{})
	var started atomic.Int32
	inner := fv.movie
	fv.movie = func(title string, year int) (metadata.VideoResult, error) {
		if started.Add(1) == 2 {
			<-release
		}
		return inner(title, year)
	}
	m := enrichManager(t, st, fv, events.NewBus())
	var doneCalls atomic.Int32
	m.OnScanDone = func(store.Library) { doneCalls.Add(1) }

	scan, err := m.StartScan(ctx, lib, false)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the job after the pass", func() bool {
		return scanRow(t, st, scan.ID).Phase == store.PhaseEnriching && started.Load() >= 2
	})
	// A rescan now registers nothing new and starts no second job.
	if _, err := m.RunScanSync(ctx, lib, false); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	jobs := len(m.enrichJobs)
	m.mu.Unlock()
	if jobs != 1 {
		t.Errorf("%d jobs running, want 1", jobs)
	}
	// Cancel through the scan, as the admin's button does.
	if err := m.CancelScan(ctx, scan.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	close(release)
	m.WaitEnrich(lib.ID)
	if sc := scanRow(t, st, scan.ID); sc.Phase != store.PhaseDone {
		t.Errorf("phase after cancel: %+v", sc)
	}
	if matched, total := matchedCount(t, st, lib.ID, store.KindMovie); matched >= total {
		t.Errorf("cancel did not stop the job: %d of %d matched", matched, total)
	}
	// The readers still start, once for the scan's job (the rescan, with
	// nothing new, also fired its own at once).
	if n := doneCalls.Load(); n != 2 {
		t.Errorf("OnScanDone called %d times, want 2", n)
	}
}

// Cancelling the scan while it is still registering stops the job with
// it, and the readers do not start.
func TestCancelDuringRegisterPassStopsJob(t *testing.T) {
	root, fv := filmFixture(t, 2*probeBatchSize+1)
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	m := enrichManager(t, st, fv, events.NewBus())
	release := blockSecondBatch(m)
	var doneCalls atomic.Int32
	m.OnScanDone = func(store.Library) { doneCalls.Add(1) }

	scan, err := m.StartScan(ctx, lib, false)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first batch's details", func() bool {
		matched, _ := matchedCount(t, st, lib.ID, store.KindMovie)
		return matched == probeBatchSize
	})
	if err := m.CancelScan(ctx, scan.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	close(release)
	waitFor(t, "the register pass", func() bool { return scanRow(t, st, scan.ID).Status != "running" })
	m.WaitEnrich(lib.ID)

	sc := scanRow(t, st, scan.ID)
	if sc.Status != "failed" || sc.Error != "cancelled" || sc.Phase != store.PhaseDone {
		t.Errorf("scan row after cancel: %+v", sc)
	}
	if m.enrichRunning(lib.ID) {
		t.Errorf("job still running after cancel")
	}
	if n := doneCalls.Load(); n != 0 {
		t.Errorf("OnScanDone called %d times after a cancelled scan", n)
	}
}
