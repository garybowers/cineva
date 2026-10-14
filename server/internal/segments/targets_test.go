package segments

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/background"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

// targetFixture is a TV library of two series, each one season of three
// episodes sharing a title sequence, and a film library.
type targetFixture struct {
	st             *store.Store
	tv, films      store.Library
	alpha, bravo   store.Item // seasons
	bravoEps       []store.Item
	withCredits    store.Item // films
	withoutCredits store.Item
}

func newTargetFixture(t *testing.T) targetFixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f := targetFixture{st: st}
	if f.tv, err = st.CreateLibrary(ctx, "TV", store.MediaTV, []string{"/tv"}); err != nil {
		t.Fatal(err)
	}
	if f.films, err = st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{"/films"}); err != nil {
		t.Fatal(err)
	}
	save := func(lib store.Library, it store.Item) store.Item {
		t.Helper()
		it.LibraryID = lib.ID
		saved, err := st.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	for _, name := range []string{"Alpha", "Bravo"} {
		series := save(f.tv, store.Item{Kind: store.KindSeries, Title: name, Path: "/tv/" + name})
		season := save(f.tv, store.Item{Kind: store.KindSeason, ParentID: series.ID, SeasonNum: 1, Path: "/tv/" + name + "/S1"})
		var eps []store.Item
		for i := 1; i <= 3; i++ {
			eps = append(eps, save(f.tv, store.Item{Kind: store.KindEpisode, ParentID: season.ID, SeasonNum: 1, EpisodeNum: i,
				Path: "/tv/" + name + "/S1/e" + string(rune('0'+i)) + ".mkv", FileMtime: 1, RuntimeSecs: 2400}))
		}
		if name == "Alpha" {
			f.alpha = season
		} else {
			f.bravo, f.bravoEps = season, eps
		}
	}
	f.withCredits = save(f.films, store.Item{Kind: store.KindMovie, Title: "Credits", Path: "/films/credits.mkv", FileMtime: 1, RuntimeSecs: 7200})
	f.withoutCredits = save(f.films, store.Item{Kind: store.KindMovie, Title: "Plain", Path: "/films/plain.mkv", FileMtime: 1, RuntimeSecs: 7200})
	return f
}

// detector returns a detector over the fixture whose fingerprints carry a
// shared title sequence in every episode, and whose films carry chapters —
// "End Credits" last in one of them. Every file read is logged.
func (f targetFixture) detector(log *[]string, mu *sync.Mutex) *Detector {
	d := NewDetector(f.st)
	d.Gate.Poll = 10 * time.Millisecond
	th := theme(1, 45)
	d.Fingerprint = func(_ context.Context, path string, start, dur float64) (media.Fingerprint, error) {
		mu.Lock()
		*log = append(*log, path)
		mu.Unlock()
		seed := int64(len(path)) + int64(path[len(path)-5])
		if start > 0 { // no shared credits
			fp := media.FingerprintSamples(theme(seed+100, 60))
			fp.Start = start
			return fp, nil
		}
		return media.FingerprintSamples(episode(seed, 120, 20, th)), nil
	}
	d.Probe = func(path string) (media.Info, error) {
		mu.Lock()
		*log = append(*log, path)
		mu.Unlock()
		last := "Chapter 3"
		if strings.Contains(path, "credits") {
			last = "End Credits"
		}
		return media.Info{RuntimeSecs: 7200, Chapters: []media.Chapter{
			{StartSecs: 0, EndSecs: 3600, Title: "Chapter 1"},
			{StartSecs: 3600, EndSecs: 6900, Title: "Chapter 2"},
			{StartSecs: 6900, EndSecs: 7200, Title: last},
		}}, nil
	}
	return d
}

// Seasons asked for by name are analysed before the library's own, and
// are not counted in the library run's progress.
func TestDetectorTargetsGoFirst(t *testing.T) {
	f := newTargetFixture(t)
	ctx := context.Background()
	var mu sync.Mutex
	var reads []string
	d := f.detector(&reads, &mu)

	d.Prioritise(ctx, []string{f.bravo.ID}, nil)
	if st := d.Status(); len(st.Targets) != 1 || st.Targets[0].Title != "Bravo" || st.Targets[0].Season != 1 ||
		st.Targets[0].SeriesID == "" || st.Targets[0].Working {
		t.Fatalf("queued targets = %+v, want Bravo season 1 waiting", st.Targets)
	}
	if err := d.analyseLibrary(ctx, f.tv.ID); err != nil {
		t.Fatal(err)
	}
	firstAlpha := slices.IndexFunc(reads, func(p string) bool { return strings.HasPrefix(p, "/tv/Alpha") })
	lastBravo := -1
	for i, p := range reads {
		if strings.HasPrefix(p, "/tv/Bravo") {
			lastBravo = i
		}
	}
	if firstAlpha < 0 || lastBravo < 0 || lastBravo > firstAlpha {
		t.Fatalf("reads = %v, want every Bravo file before any Alpha one", reads)
	}
	st := d.Status()
	if len(st.Targets) != 0 {
		t.Errorf("targets after the run = %+v, want none", st.Targets)
	}
	// The library run analysed Alpha's episodes alone: Bravo's were done
	// by the time it got there.
	if st.Last == nil || st.Last.Seasons != 2 || st.Last.Episodes != 3*len(specs) {
		t.Errorf("last run = %+v, want both seasons passed, 3 episodes analysed for each kind", st.Last)
	}
	for _, id := range []string{f.alpha.ID, f.bravo.ID} {
		state, err := f.st.SeasonSegmentState(ctx, id, store.SegmentIntro)
		if err != nil || len(state) != 3 || !state[0].Analysed || !state[2].Analysed {
			t.Fatalf("season %s state = %+v, %v; want analysed", id, state, err)
		}
	}

	// One episode sent back: only it is analysed again, its neighbours
	// read for comparison only.
	if _, err := f.st.RedetectItemSegments(ctx, []string{f.bravoEps[1].ID}, store.RedetectOptions{Kinds: []string{store.SegmentIntro}}); err != nil {
		t.Fatal(err)
	}
	reads = nil
	d.Prioritise(ctx, []string{f.bravo.ID}, nil)
	if err := d.runTargets(ctx); err != nil {
		t.Fatal(err)
	}
	if len(reads) != 3 {
		t.Errorf("reads = %v, want the episode and its two neighbours", reads)
	}
	if r := d.Status().Recent; len(r) == 0 || r[0].ItemID != f.bravoEps[1].ID || !r[0].Found || r[0].Kind != store.SegmentIntro {
		t.Errorf("latest outcome = %+v, want the re-detected episode's intro", r)
	}
}

// Targeted work ignores the schedule, and gets past a library run that the
// schedule is holding; the held run carries on from where it was, later.
func TestDetectorTargetsIgnoreSchedule(t *testing.T) {
	f := newTargetFixture(t)
	var mu sync.Mutex
	var reads []string
	d := f.detector(&reads, &mu)
	d.SetSchedule(background.Schedule{Mode: background.ModeManual})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Run(ctx)
	d.Enqueue(f.tv.ID)
	waitFor(t, "the library run held by the schedule", func() bool {
		st := d.Status()
		return st.Running && st.PauseReason == "on-demand"
	})
	d.Prioritise(context.Background(), []string{f.bravo.ID}, []string{f.withCredits.ID})
	waitFor(t, "the targets done", func() bool {
		st := d.Status()
		return len(st.Targets) == 0 && st.PauseReason == "on-demand"
	})
	state, err := f.st.SeasonSegmentState(context.Background(), f.bravo.ID, store.SegmentCredits)
	if err != nil || !state[0].Analysed {
		t.Fatalf("Bravo after its targeted run = %+v, %v; want analysed", state, err)
	}
	if segs, _ := f.st.Segments(context.Background(), f.withCredits.ID); len(segs) != 1 {
		t.Fatalf("film segments = %+v, want its credits", segs)
	}
	// Alpha was the library run's, which the schedule still holds.
	state, _ = f.st.SeasonSegmentState(context.Background(), f.alpha.ID, store.SegmentIntro)
	if state[0].Analysed {
		t.Fatal("Alpha was analysed outside the schedule")
	}
	d.RunNow()
	waitFor(t, "the library run finished", func() bool { return !d.Status().Running })
	state, _ = f.st.SeasonSegmentState(context.Background(), f.alpha.ID, store.SegmentIntro)
	if !state[0].Analysed {
		t.Fatal("Alpha not analysed once the run was let through")
	}
}

// A film's credits come from a final chapter named for them; a film
// without one is recorded as having none, and re-detection reads it again.
func TestDetectorFilmCredits(t *testing.T) {
	f := newTargetFixture(t)
	ctx := context.Background()
	var mu sync.Mutex
	var reads []string
	d := f.detector(&reads, &mu)
	if err := d.analyseLibrary(ctx, f.films.ID); err != nil {
		t.Fatal(err)
	}
	segs, err := f.st.Segments(ctx, f.withCredits.ID)
	if err != nil || len(segs) != 1 || segs[0].Kind != store.SegmentCredits || segs[0].StartSecs != 6900 || segs[0].EndSecs != 7200 {
		t.Fatalf("credits = %+v, %v; want 6900-7200", segs, err)
	}
	if segs, _ := f.st.Segments(ctx, f.withoutCredits.ID); len(segs) != 0 {
		t.Fatalf("film without a credits chapter has %+v", segs)
	}
	if st := d.Status(); st.Last == nil || st.Last.Films != 2 || st.Last.CreditsFound != 1 {
		t.Fatalf("last run = %+v, want 2 films, 1 with credits", st.Last)
	}
	reads = nil
	if err := d.analyseLibrary(ctx, f.films.ID); err != nil || len(reads) != 0 {
		t.Fatalf("second pass read %v (%v), want nothing", reads, err)
	}
	res, err := f.st.RedetectItemSegments(ctx, []string{f.withoutCredits.ID}, store.RedetectOptions{})
	if err != nil || len(res.Films) != 1 {
		t.Fatalf("redetect = %+v, %v", res, err)
	}
	d.Prioritise(ctx, nil, res.Films)
	if err := d.runTargets(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reads, []string{"/films/plain.mkv"}) {
		t.Fatalf("reads = %v, want the film read again", reads)
	}
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
