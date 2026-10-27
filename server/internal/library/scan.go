package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

var (
	logger      = logging.For("scan")
	artworkLog  = logging.For("artwork")
	metadataLog = logging.For("metadata")
)

// Manager orchestrates library scans. Scans run in managed goroutines; the
// scans table carries progress so any client can observe it.
type Manager struct {
	Store      *store.Store
	Video      metadata.VideoProvider // nil when no TMDB key is configured
	Music      metadata.MusicProvider // nil when MusicBrainz is disabled
	ArtworkDir string
	Probe      func(path string) (media.Info, error)
	Events     *events.Bus // nil outside the server (CLI scans)
	// CoverArt reads a file's embedded cover art; nil uses
	// media.ExtractCoverArt.
	CoverArt func(path string) []byte
	// ProbeAudio reads a music file's tags and duration; nil uses
	// media.ProbeAudio.
	ProbeAudio func(path string) (media.AudioInfo, error)
	// CertRegion is the ISO 3166-1 country whose content rating is stored
	// ("GB", "US", "DE"). Certification is regional — a BBFC 15 and an FSK
	// 16 are different boards' judgements, not two spellings — so which one
	// is meaningful depends on where the viewer is. Set from the admin
	// setting; "" falls back to US inside CertificationFor.
	CertRegion string

	// FilmCredits, when set, records each scanned film's end credits from
	// its chapter names (media.CreditsChapter), from the probe the scan
	// already makes. libraries.intro_detection turns it off with the rest
	// of detection.
	FilmCredits bool

	// Storage, when set, is consulted before a scan walks anything: a scan
	// of a share that has stopped answering would otherwise hang on its
	// first directory read.
	Storage *StorageMonitor

	// OnScanDone, when set, is called after a scan completes successfully —
	// how the server starts work that depends on what a scan found.
	OnScanDone func(lib store.Library)
	// OnScanFinished, when set, is called after every scan, failed ones
	// included, with its result and the titles it added (see noteNew):
	// how notifications hear of new titles and failed scans.
	OnScanFinished func(lib store.Library, scan store.Scan, added []store.Item)

	// Playing, when set, reports whether anything is being played. A
	// rescan requested by a webhook waits for it to stop, up to
	// webhookRescanCeiling; a scan an admin starts by hand does not.
	Playing func() bool

	// Quiet, when set, says why now is not a quiet time for a queued full
	// rescan to start — outside the background work window, something
	// playing — or returns "" when it is (see QueueFullScan). Unset, any
	// time is quiet.
	Quiet func() string

	// artists holds the ids of artists being looked up (see LookUpArtist);
	// enriching the ids of libraries whose albums are (see enrichMusic);
	// enrichJobs the film and TV enrichment jobs by library (see enrich.go).
	artists    sync.Map
	enriching  sync.Map
	enrichJobs map[string]*enrichJob
	// openSeries is, per library, the series folder the TV register pass
	// is still writing episodes into (see setOpenSeriesLocked).
	openSeries map[string]string

	mu      sync.Mutex
	queued  map[string]bool               // libraryID -> full rescan waiting for a quiet time
	running map[string]context.CancelFunc // libraryID -> cancel for the active scan
	again   map[string]bool               // libraryID -> rescan once the active scan ends
	waiting map[string]bool               // libraryID -> a requested rescan is waiting for playback

	// poll and ceiling override playbackPoll and webhookRescanCeiling in
	// tests.
	poll, ceiling time.Duration
	// clock is the time unmatched titles are retried against; time.Now
	// when nil. retryPace spaces the retries' provider lookups.
	clock     func() time.Time
	retryPace time.Duration
	// enrichDelay is how long after a scan defers its lookups the
	// enrichment job starts, and roundWait how long the job waits between
	// rounds while the pass is still registering (enrichStartDelay and
	// enrichRoundWait; shorter in tests).
	enrichDelay, roundWait time.Duration
}

func NewManager(st *store.Store, video metadata.VideoProvider, artworkDir string) *Manager {
	return &Manager{
		Store:       st,
		Video:       video,
		ArtworkDir:  artworkDir,
		Probe:       media.Probe,
		retryPace:   refreshRate,
		enrichDelay: enrichStartDelay,
		roundWait:   enrichRoundWait,
		running:     map[string]context.CancelFunc{},
		again:       map[string]bool{},
		waiting:     map[string]bool{},
	}
}

// acquire takes the scan lock for a library: in-process via the running map,
// cross-process via the scans table (CLI and server share the database).
func (m *Manager) acquire(ctx context.Context, lib store.Library, cancel context.CancelFunc) error {
	m.mu.Lock()
	if _, ok := m.running[lib.ID]; ok {
		m.mu.Unlock()
		return fmt.Errorf("library %q is already being scanned", lib.Name)
	}
	m.running[lib.ID] = cancel
	m.mu.Unlock()
	if _, running, err := m.Store.RunningScan(ctx, lib.ID); err != nil || running {
		m.clearRunning(lib.ID)
		if err != nil {
			return err
		}
		return fmt.Errorf("library %q is already being scanned (possibly by another process)", lib.Name)
	}
	return nil
}

// CancelScan stops a running scan. Scans owned by this process are cancelled
// via their context; a stale row from a dead process is marked failed.
func (m *Manager) CancelScan(ctx context.Context, scanID string) error {
	sc, err := m.Store.ScanByID(ctx, scanID)
	if err != nil {
		return err
	}
	if sc.Status != "running" {
		// The register pass is over; the enrichment job may still be
		// fetching the details, and that is what the button stops then.
		if sc.Phase == store.PhaseEnriching && m.cancelEnrich(sc.LibraryID) {
			return nil
		}
		return fmt.Errorf("scan is not running")
	}
	m.mu.Lock()
	cancel, ok := m.running[sc.LibraryID]
	m.mu.Unlock()
	if ok {
		cancel() // runScan records the row as cancelled
		// The job fetching details alongside the pass stops with it.
		m.cancelEnrich(sc.LibraryID)
		return nil
	}
	return m.Store.FinishScan(ctx, scanID, "failed", "cancelled", sc.FilesSeen, sc.ItemsCreated)
}

// StartScan launches a scan in the background and returns its record. A
// full scan rereads and rematches every file; otherwise only files that
// changed since the last scan are (see scanPlan).
func (m *Manager) StartScan(ctx context.Context, lib store.Library, full bool) (store.Scan, error) {
	// Detached from the request context: a scan outlives its HTTP request.
	runCtx, cancel := context.WithCancel(context.Background())
	if err := m.acquire(ctx, lib, cancel); err != nil {
		cancel()
		return store.Scan{}, err
	}
	scan, err := m.Store.CreateScan(ctx, lib.ID)
	if err != nil {
		m.clearRunning(lib.ID)
		cancel()
		return store.Scan{}, err
	}
	go func() {
		defer m.rescanIfRequested(lib.ID)
		defer m.clearRunning(lib.ID)
		defer cancel()
		m.runScan(runCtx, scan, lib, full)
	}()
	return scan, nil
}

// RequestScan starts an incremental scan of the library or, when one is
// already running here, queues another to follow it: the scan in flight may
// have walked past the folder a new file has just landed in. Requests made
// during one scan collapse into a single follow-up.
//
// While anything is playing the scan waits for it to stop, for up to
// webhookRescanCeiling, and requests made while it waits collapse into it.
// RequestScan itself returns at once either way.
func (m *Manager) RequestScan(ctx context.Context, lib store.Library) error {
	m.mu.Lock()
	if _, ok := m.running[lib.ID]; ok {
		m.again[lib.ID] = true
		m.mu.Unlock()
		return nil
	}
	if m.waiting[lib.ID] {
		m.mu.Unlock()
		return nil
	}
	if m.Playing != nil && m.Playing() {
		m.waiting[lib.ID] = true
		m.mu.Unlock()
		go m.scanAfterPlayback(lib)
		return nil
	}
	m.mu.Unlock()
	_, err := m.StartScan(ctx, lib, false)
	return err
}

// scanAfterPlayback waits for playback to stop, or the ceiling, then starts
// the requested scan. The library is read again, since it may have been
// edited or deleted while it waited.
func (m *Manager) scanAfterPlayback(lib store.Library) {
	poll, ceiling := m.poll, m.ceiling
	if poll <= 0 {
		poll = playbackPoll
	}
	if ceiling <= 0 {
		ceiling = webhookRescanCeiling
	}
	logger.Debug("rescan waiting for playback to finish", "library", lib.Name, "max_wait", ceiling)
	ctx := context.Background()
	waited, timedOut, err := waitForPlayback(ctx, m.Playing, poll, ceiling)
	m.mu.Lock()
	delete(m.waiting, lib.ID)
	m.mu.Unlock()
	if err != nil {
		logger.Warn("requested rescan abandoned", "library", lib.Name, "err", err)
		return
	}
	if timedOut {
		logger.Info("requested rescan starting while playback continues", "library", lib.Name, "waited", waited.Round(time.Second))
	} else {
		logger.Debug("playback finished, requested rescan starting", "library", lib.Name, "waited", waited.Round(time.Second))
	}
	fresh, err := m.Store.Library(ctx, lib.ID)
	if err != nil {
		logger.Warn("requested rescan: library lookup failed", "library_id", lib.ID, "err", err)
		return
	}
	m.mu.Lock()
	if _, ok := m.running[lib.ID]; ok {
		// Someone started one by hand meanwhile; follow it, as a request
		// made during a scan would.
		m.again[lib.ID] = true
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	if _, err := m.StartScan(ctx, fresh, false); err != nil {
		logger.Error("requested rescan failed", "library", fresh.Name, "err", err)
	}
}

// rescanIfRequested starts the follow-up RequestScan queued during a scan,
// waiting for playback like any requested scan. The library is read again,
// since it may have been edited or deleted while the scan ran.
func (m *Manager) rescanIfRequested(libID string) {
	m.mu.Lock()
	queued := m.again[libID]
	delete(m.again, libID)
	m.mu.Unlock()
	if !queued {
		return
	}
	ctx := context.Background()
	lib, err := m.Store.Library(ctx, libID)
	if err != nil {
		logger.Warn("queued rescan: library lookup failed", "library_id", libID, "err", err)
		return
	}
	if err := m.RequestScan(ctx, lib); err != nil {
		logger.Error("queued rescan failed", "library", lib.Name, "err", err)
	}
}

// RunScanSync runs a scan in the foreground (used by the `cineva scan` CLI).
func (m *Manager) RunScanSync(ctx context.Context, lib store.Library, full bool) (store.Scan, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := m.acquire(ctx, lib, cancel); err != nil {
		return store.Scan{}, err
	}
	defer m.clearRunning(lib.ID)

	scan, err := m.Store.CreateScan(ctx, lib.ID)
	if err != nil {
		return store.Scan{}, err
	}
	m.runScan(runCtx, scan, lib, full)
	scans, err := m.Store.Scans(ctx, 50)
	if err != nil {
		return store.Scan{}, err
	}
	for _, s := range scans {
		if s.ID == scan.ID {
			return s, nil
		}
	}
	return scan, nil
}

func (m *Manager) clearRunning(libID string) {
	m.mu.Lock()
	delete(m.running, libID)
	m.mu.Unlock()
}

// scanEvent is the payload of "scan" events. FilesTotal comes from the
// scanner's upfront file listing — it lives only on the event (not the
// scans table) and is 0 until the listing completes.
type scanEvent struct {
	store.Scan
	FilesTotal int `json:"filesTotal,omitempty"`
}

// itemsEvent is the payload of "items" events: a hint that the named
// library's contents changed and open views should refetch them. It carries
// no progress detail — unlike "scan", which is admin-only — so every user
// receives it and a grid fills in live as a scan runs.
type itemsEvent struct {
	LibraryID string `json:"libraryId"`
	// Scanning says a scan is in progress, with no detail about it. That one
	// bit is not sensitive — a user can see items appearing anyway — and
	// without it a first scan of an empty library tells a non-admin to "run
	// a scan from the admin UI" while one is already running.
	Scanning bool `json:"scanning"`
	// Enriched of ToEnrich titles have their details so far, while the
	// enrichment job after a big scan runs (server/docs/scanning.md); both
	// zero otherwise. Counts of titles, nothing more, so every user may see
	// them.
	Enriched int `json:"enriched,omitempty"`
	ToEnrich int `json:"toEnrich,omitempty"`
}

func (m *Manager) runScan(ctx context.Context, scan store.Scan, lib store.Library, full bool) {
	var filesSeen, itemsCreated int
	var scanErr error
	started := time.Now()

	m.Events.Publish(events.Event{Type: "scan", Data: scanEvent{Scan: scan}})

	// report streams progress to the scans table and the event bus, at most
	// once a second so a fast local scan doesn't turn into a write storm.
	var lastReport time.Time
	// itemsSent is the created count at the last "items" event, so the grid
	// is only told to refetch when the scan actually added something.
	itemsSent := 0
	report := func(seen, created, total int) {
		if time.Since(lastReport) < time.Second {
			return
		}
		lastReport = time.Now()
		snap := scan
		snap.FilesSeen, snap.ItemsCreated = seen, created
		// The job fetching details alongside the pass reports on the
		// same row, so its counts ride along rather than reading as zero
		// between its reports.
		snap.Enriched, snap.ToEnrich = m.enrichProgress(lib.ID)
		if err := m.Store.UpdateScanProgress(context.WithoutCancel(ctx), scan.ID, seen, created); err != nil {
			logger.Warn("saving scan progress failed", "library", lib.Name, "err", err)
		}
		m.Events.Publish(events.Event{Type: "scan", Data: scanEvent{Scan: snap, FilesTotal: total}})
		if created > itemsSent {
			itemsSent = created
			m.Events.Publish(events.Event{Type: "items", Data: itemsEvent{LibraryID: lib.ID, Scanning: true, Enriched: snap.Enriched, ToEnrich: snap.ToEnrich}})
		}
	}

	if m.Storage != nil {
		if st := m.Storage.Check(lib); !st.Available {
			scanErr = fmt.Errorf("storage unreachable: %s: %s", st.Path, st.Error)
		}
	}
	var plan *scanPlan
	if scanErr == nil {
		plan, scanErr = m.newScanPlan(ctx, lib, full)
	}
	if scanErr == nil {
		plan.scanID = scan.ID
		switch lib.MediaType {
		case store.MediaMovies:
			filesSeen, itemsCreated, scanErr = m.scanMovies(ctx, lib, plan, report)
		case store.MediaTV:
			filesSeen, itemsCreated, scanErr = m.scanTV(ctx, lib, plan, report)
		case store.MediaMusic:
			filesSeen, itemsCreated, scanErr = m.scanMusic(ctx, lib, plan, report)
		default:
			scanErr = fmt.Errorf("scanner for %s libraries arrives in a later milestone", lib.MediaType)
		}
	}

	// Fingerprints for unchanged items that had none, written whether or
	// not the scan finished: they describe files the walk did see.
	if plan != nil && len(plan.adopt) > 0 {
		if err := m.Store.SetSidecarStamps(context.WithoutCancel(ctx), plan.adopt); err != nil {
			logger.Warn("recording sidecar fingerprints failed", "library", lib.Name, "err", err)
		}
	}

	if scanErr == nil && filesSeen > 0 {
		if pruned, err := m.Store.PruneExcept(context.WithoutCancel(ctx), lib.ID, plan.keep); err != nil {
			logger.Error("pruning stale items failed", "library", lib.Name, "err", err)
		} else if pruned > 0 {
			logger.Info("pruned stale items", "library", lib.Name, "count", pruned)
		}
		// Before grouping, so a film matched here joins its other versions
		// straight away. A run that deferred its lookups leaves every
		// unmatched title to the enrichment job instead, and so does a
		// rescan while that job is still running, or the two would look
		// the same titles up at once.
		if !plan.deferLookups && !m.enrichRunning(lib.ID) {
			if err := m.retryUnmatched(ctx, lib, plan.looked); err != nil && !errors.Is(err, context.Canceled) {
				metadataLog.Warn("retrying unmatched titles failed", "library", lib.Name, "err", err)
			}
		}
		// After pruning, so a removed file cannot leave an item pointing at
		// a primary that no longer exists.
		if lib.MediaType == store.MediaMovies {
			if linked, err := m.Store.GroupVersions(context.WithoutCancel(ctx), lib.ID); err != nil {
				logger.Error("grouping alternative versions failed", "library", lib.Name, "err", err)
			} else if linked > 0 {
				logger.Debug("grouped alternative versions", "library", lib.Name, "count", linked)
			}
		}
	}

	status, errMsg := "completed", ""
	if errors.Is(scanErr, context.Canceled) {
		status, errMsg = "failed", "cancelled"
		logger.Info("scan cancelled", "library", lib.Name, "scan", scan.ID)
	} else if scanErr != nil {
		status, errMsg = "failed", scanErr.Error()
		logger.Error("scan failed", "library", lib.Name, "scan", scan.ID, "err", scanErr)
	} else {
		// Worth a line by default only when it found something, or was asked
		// for in full: an unchanged library rescanned every 15 minutes is
		// not news.
		level := slog.LevelDebug
		if itemsCreated > 0 || full {
			level = slog.LevelInfo
		}
		logger.Log(ctx, level, "scan finished", "library", lib.Name, "files", filesSeen,
			"items", itemsCreated, "full", full, "took", time.Since(started))
	}
	// The scan's own context may be cancelled; finish the row regardless.
	if err := m.Store.FinishScan(context.WithoutCancel(ctx), scan.ID, status, errMsg, filesSeen, itemsCreated); err != nil {
		logger.Error("saving scan result failed", "scan", scan.ID, "err", err)
	}
	detail := fmt.Sprintf("%d files, %d items", filesSeen, itemsCreated)
	if errMsg != "" {
		detail += " · " + errMsg
	}
	if err := m.Store.RecordAudit(context.WithoutCancel(ctx), store.AuditEntry{
		UserID: "system", Action: "scan." + status, TargetKind: "library",
		TargetID: lib.ID, TargetTitle: lib.Name, Detail: detail,
	}); err != nil {
		logger.Error("recording scan audit entry failed", "library", lib.Name, "err", err)
	}
	m.Events.Publish(events.Event{Type: "logs"})
	done := scan
	done.Status, done.Error = status, errMsg
	done.FilesSeen, done.ItemsCreated = filesSeen, itemsCreated
	done.Phase = store.PhaseDone
	// The lookups a big scan deferred: without an event bus (the CLI) they
	// run now, in line, so the command ends with the details fetched; in
	// the server the job started alongside the pass carries on and closes
	// the phase itself. Either way the job starts the readers (detection,
	// previews) when it ends, rather than racing them; a scan that
	// deferred nothing starts them now, as it always did.
	inline := false
	if status == "completed" && plan != nil && plan.deferLookups && !plan.enriching && m.Events == nil {
		plan.enriching = m.startEnrich(lib, scan.ID, closedChan, 0)
		inline = plan.enriching // ran to its end just now, readers included
	}
	published, readers := false, true
	if plan != nil {
		var enriched, toEnrich int
		enriched, toEnrich, published, readers = m.registerEnded(context.WithoutCancel(ctx), lib, plan, done, status)
		if published {
			done.Phase, done.Enriched, done.ToEnrich = store.PhaseEnriching, enriched, toEnrich
		}
	}
	if status == "completed" && readers && !inline && m.OnScanDone != nil {
		m.OnScanDone(lib)
	}
	if !published {
		m.Events.Publish(events.Event{Type: "scan", Data: scanEvent{Scan: done}})
	}
	if m.OnScanFinished != nil && errMsg != "cancelled" {
		var added []store.Item
		if plan != nil && status == "completed" {
			added = plan.added
		}
		m.OnScanFinished(lib, done, added)
	}
	// The throttle means the last upserts (and any prune) came after the
	// final report, so close with an unconditional refetch hint. Scanning is
	// false here: this fires once the scan is over, and a client left
	// believing one is still running would show the wrong empty state for
	// good. A job still fetching details says so, with its count.
	closing := itemsEvent{LibraryID: lib.ID, Scanning: false}
	if done.Phase == store.PhaseEnriching {
		closing.Enriched, closing.ToEnrich = done.Enriched, done.ToEnrich
	}
	m.Events.Publish(events.Event{Type: "items", Data: closing})
	m.Events.Publish(events.Event{Type: "libraries"})
}

// reportFunc receives scanner progress: files handled, items upserted, and
// the total files found by the upfront listing.
type reportFunc func(seen, created, total int)

// videoFiles walks a library's folders for its video files. The sidecar
// files the walk passes on the way are noted in sidecars, when given, for
// the change test (see sidecarIndex) — from the same directory reads, so
// noticing an edited .nfo costs no extra trip to the share.
func (m *Manager) videoFiles(lib store.Library, sidecars *sidecarIndex) ([]string, error) {
	var files []string
	for _, root := range lib.Paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// Skip an extras folder whole rather than filtering its files
			// one by one: nothing inside one is ever a feature.
			if d.IsDir() {
				if path != root && extrasDirRe.MatchString(d.Name()) {
					return fs.SkipDir
				}
				return nil
			}
			if IsVideoFile(path) && !IsExtra(path) {
				files = append(files, path)
			} else {
				sidecars.add(path, d)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", root, err)
		}
	}
	sort.Strings(files)
	return files, nil
}

func (m *Manager) scanMovies(ctx context.Context, lib store.Library, plan *scanPlan, report reportFunc) (int, int, error) {
	plan.sidecars = newSidecarIndex(true)
	files, err := m.videoFiles(lib, plan.sidecars)
	if err != nil {
		return 0, 0, err
	}
	todo := plan.changed(files)
	skipped := len(files) - len(todo)
	created := 0
	newFilms := 0
	for _, path := range todo {
		if _, known := plan.stamps[path]; !known {
			newFilms++
		}
	}
	plan.decideDeferral(newFilms, m.Video != nil)
	video := m.providerFor(lib, plan)
	// Files are probed a batch at a time in parallel (see probeBatch); the
	// rest stays serial, because metadata lookups are rate limited and
	// SQLite takes one writer. Each batch is prepared in full, with every
	// lookup and download done, then written in one transaction
	// (store.SaveFilms): one commit a batch rather than half a dozen a
	// film, and the write lock held for the writes alone.
	for start := 0; start < len(todo); start += probeBatchSize {
		end := min(start+probeBatchSize, len(todo))
		batch := todo[start:end]
		probes := m.probeBatch(ctx, batch)

		saves := make([]store.VideoSave, 0, len(batch))
		for bi, path := range batch {
			if err := ctx.Err(); err != nil {
				return len(files), created, err
			}
			report(skipped+start+bi, created+len(saves), len(files))
			saves = append(saves, m.prepareFilm(ctx, lib, plan, path, probes[bi], video))
		}
		saved, err := m.Store.SaveFilms(ctx, saves)
		if err != nil {
			return len(files), created, err
		}
		for _, it := range saved {
			plan.keep[it.ID] = true
			plan.looked[it.ID] = true
			plan.noteNew(it.Path, it)
			created++
		}
	}
	return len(files), created, nil
}

// prepareFilm reads everything a film's row and the rows that hang off it
// are made from: the filename, the probe, a sidecar .nfo and artwork, and
// the provider's match when there is a provider for this run. Nothing is
// written; the batch is (SaveFilms).
func (m *Manager) prepareFilm(ctx context.Context, lib store.Library, plan *scanPlan, path string, probe probed, video metadata.VideoProvider) store.VideoSave {
	info := ParseMovie(path)
	item := store.Item{
		LibraryID: lib.ID, Kind: store.KindMovie,
		Title: info.Title, Year: info.Year, Path: path,
	}
	m.applyProbe(&item, probe)

	// A sidecar written by Kodi, Jellyfin, Radarr or Sonarr is the
	// operator's own curation, so it outranks anything we would guess:
	// its title and year drive the search, and its tmdb id skips the
	// search entirely.
	nfo, hasNFO, nerr := ReadNFO(path)
	if nerr != nil {
		logger.Warn("nfo unreadable", "path", path, "err", nerr)
	}
	if hasNFO {
		if t := strings.TrimSpace(nfo.Title); t != "" {
			item.Title = t
		} else if t := strings.TrimSpace(nfo.OriginalTitle); t != "" {
			item.Title = t
		}
		if y := nfo.ReleaseYear(); y > 0 {
			item.Year = y
		}
		if id, _ := nfo.ResolvedIDs(); id > 0 && info.TmdbID == 0 {
			info.TmdbID = id
		}
	}

	var videoResult metadata.VideoResult
	var lookupErr error
	if video != nil {
		// An explicit id in the filename is the operator telling us the
		// answer, so it skips searching entirely — that is the whole
		// point of the convention. Anything else searches with the
		// tag-corrected title/year, since embedded tags restore
		// punctuation the filename mangles.
		var r metadata.VideoResult
		var err error
		if info.TmdbID > 0 {
			r, err = video.MovieByID(ctx, info.TmdbID)
			if err != nil {
				// A bad hint must not silently become a wrong match, so
				// fall back to searching rather than to nothing.
				metadataLog.Warn("tmdb id hint failed, searching instead", "path", path, "tmdb_id", info.TmdbID, "err", err)
				r, err = video.SearchMovie(ctx, item.Title, item.Year)
			}
		} else {
			r, err = video.SearchMovie(ctx, item.Title, item.Year)
		}
		if err == nil && r.TmdbID == 0 {
			err = metadata.ErrNotFound
		}
		if err == nil {
			videoResult = r
			m.applyVideoResult(ctx, &item, r)
		}
		lookupErr = err
	}
	// Applied after the provider so the operator's own files win: they
	// chose to put them there, and a local poster is the whole point of
	// curating one.
	if hasNFO {
		applyNFO(&item, nfo)
	}
	m.applyLocalArtwork(&item, path)
	plan.stamp(&item, path)

	save := store.VideoSave{Item: item, Genres: item.Genres, Streams: true}
	save.Cast, save.Crew = m.creditsOf(ctx, videoResult)
	save.Subtitles, save.AudioTracks = streamsOf(path, probe)
	// Spaces out the automatic retries of a film the provider does not
	// know (see retryUnmatched).
	if errors.Is(lookupErr, metadata.ErrNotFound) {
		save.MatchFailedAt = m.now()
	}
	save.CreditsAnalysed, save.Credits = m.filmCredits(item.FileMtime, probe)
	return save
}

// filmCredits reads where a film's end credits start from its chapter
// names, or that it has none to find: the chapters are already in the
// probe, so this costs no read of the file. It reports whether there is
// an analysis to record: a file the probe could not read is left for
// detection to try again. Credits set by hand are kept when it is
// written (saveSegmentAnalysis leaves them alone).
func (m *Manager) filmCredits(fileMtime int64, r probed) (bool, *store.Segment) {
	if !m.FilmCredits || r.Err != nil || fileMtime == 0 {
		return false, nil
	}
	if start, end, ok := media.CreditsChapter(r.Info.Chapters, r.Info.RuntimeSecs); ok {
		return true, &store.Segment{Kind: store.SegmentCredits, StartSecs: start, EndSecs: end}
	}
	return true, nil
}

// creditsOf converts a match's cast and crew for storing, downloading
// each headshot once. People are shared across films, so a cast member in
// twenty titles costs one row and one image, not twenty.
func (m *Manager) creditsOf(ctx context.Context, r metadata.VideoResult) (cast, crew []store.Person) {
	if len(r.Cast) == 0 && len(r.Crew) == 0 {
		return nil, nil
	}
	convert := func(people []metadata.Person, roleOf func(metadata.Person) string) []store.Person {
		out := make([]store.Person, 0, len(people))
		for _, p := range people {
			sp := store.Person{TmdbID: p.TmdbID, Name: p.Name, Role: roleOf(p), Order: p.Order}
			if p.ProfileURL != "" && m.ArtworkDir != "" {
				name := "person-" + strconv.Itoa(p.TmdbID)
				// DownloadArtwork skips a file that already exists, so a
				// person seen again costs a stat, not a request.
				if file, err := metadata.DownloadArtwork(ctx, http.DefaultClient, p.ProfileURL, m.ArtworkDir, name); err == nil {
					sp.Profile = "/artwork/" + file
				}
			}
			out = append(out, sp)
		}
		return out
	}
	cast = convert(r.Cast, func(p metadata.Person) string { return p.Character })
	crew = convert(r.Crew, func(p metadata.Person) string { return p.Job })
	return cast, crew
}

// saveCredits stores an item's cast and crew (creditsOf) on their own,
// outside a scan's batches.
func (m *Manager) saveCredits(ctx context.Context, itemID string, r metadata.VideoResult) {
	cast, crew := m.creditsOf(ctx, r)
	if len(cast) == 0 && len(crew) == 0 {
		return
	}
	if err := m.Store.ReplaceCredits(ctx, itemID, cast, crew); err != nil {
		logger.Error("saving credits failed", "item", itemID, "err", err)
	}
}

// saveGenres writes an item's genres, leaving what is there when a match
// produced none — the same rule the other metadata fields follow.
func (m *Manager) saveGenres(ctx context.Context, itemID string, genres []string) {
	if len(genres) == 0 {
		return
	}
	// Genres live in their own table, so the lock that UpsertItem applies to
	// the item's own columns does not reach them; check it here instead.
	if it, err := m.Store.Item(ctx, itemID); err == nil && it.IsLocked("genres") {
		return
	}
	if err := m.Store.ReplaceGenres(ctx, itemID, genres); err != nil {
		logger.Error("saving genres failed", "item", itemID, "err", err)
	}
}

func (m *Manager) scanTV(ctx context.Context, lib store.Library, plan *scanPlan, report reportFunc) (int, int, error) {
	plan.sidecars = newSidecarIndex(false)
	files, err := m.videoFiles(lib, plan.sidecars)
	if err != nil {
		return 0, 0, err
	}
	todo := plan.changed(files)
	skipped := len(files) - len(todo)
	created := 0
	seriesIDs := map[string]string{} // show dir -> item id
	seriesTmdb := map[string]int{}   // show dir -> tmdb id
	// show dir -> the provider's seasons and when each began, for placing
	// date-stamped episodes
	premieres := map[string][]metadata.SeasonPremiere{}
	seasonIDs := map[string]string{} // season row path -> item id
	newSeries := map[string]bool{}
	for _, path := range todo {
		if dir := seriesDirFor(lib.Paths, path); dir != "" {
			if _, known := plan.stamps[dir]; !known {
				newSeries[dir] = true
			}
		}
	}
	plan.decideDeferral(len(newSeries), m.Video != nil)
	video := m.providerFor(lib, plan)
	seasons := newSeasonCache(video)

	// Files are probed a batch at a time in parallel, as the film scanner
	// does; the rest stays serial for the provider's rate limit and
	// SQLite's single writer. Probing one file at a time made a first scan
	// of a ten-thousand-episode library take most of a day. Each batch is
	// prepared in full, grouped by series and season, then written one
	// series at a time, each in one transaction (store.SaveSeries).
	for start := 0; start < len(todo); start += probeBatchSize {
		end := min(start+probeBatchSize, len(todo))
		batch := todo[start:end]
		probes := m.probeBatch(ctx, batch)

		var groups []*seriesGroup
		byDir := map[string]*seriesGroup{}
		for bi, path := range batch {
			if err := ctx.Err(); err != nil {
				return len(files), created, err
			}
			report(skipped+start+bi, created, len(files))
			ep, ok := ParseEpisode(path)
			if !ok {
				logger.Debug("no episode number or date, skipping", "path", path)
				continue
			}
			showDir := seriesDirFor(lib.Paths, path)
			if showDir == "" {
				// A video sitting loose in the library root has no series
				// folder, and guessing one from the filename is how a stray
				// file becomes a series of its own.
				logger.Debug("no series folder, skipping", "path", path)
				continue
			}

			g := byDir[showDir]
			if g == nil {
				g = &seriesGroup{dir: showDir, seasonIdx: map[string]int{}}
				g.save.SeriesID = seriesIDs[showDir]
				if g.save.SeriesID == "" {
					// Only reached for a changed file, so its series is
					// matched again: a new episode is the likeliest moment
					// for the provider's series data to have moved on too.
					series, res, _ := m.prepareSeries(ctx, lib, showDir, ep.Show, ep.Year, video)
					g.save.Series = &series
					// A series registered without a lookup keeps the id
					// an earlier scan found for it (upsertItem leaves a
					// zero id alone), so its seasons can still be fetched.
					if res.TmdbID == 0 {
						res.TmdbID = plan.stamps[showDir].TmdbID
					}
					seriesTmdb[showDir] = res.TmdbID
					premieres[showDir] = res.SeasonPremieres
				}
				groups = append(groups, g)
				byDir[showDir] = g
			}
			// A daily show's episode numbered by its date has nothing to
			// fetch: the provider was just asked about every season it
			// could be in.
			numberedByDate := ep.Dated() && !m.placeDatedEpisode(ctx, &ep, seriesTmdb[showDir], premieres[showDir], seasons)
			seasonPath := seasonItemPath(showDir, seasonDirFor(showDir, path), ep.Season)
			var sr metadata.SeasonResult
			haveSeason := false
			if !numberedByDate {
				sr, haveSeason = seasons.get(ctx, seriesTmdb[showDir], ep.Season)
			}

			si, ok := g.seasonIdx[seasonPath]
			if !ok {
				ss := store.SeasonSave{SeasonID: seasonIDs[seasonPath]}
				if ss.SeasonID == "" {
					season := store.Item{
						LibraryID: lib.ID, Kind: store.KindSeason,
						Title: fmt.Sprintf("Season %02d", ep.Season), SeasonNum: ep.Season, Path: seasonPath,
					}
					// Season-level TMDB data: poster + overview for the
					// season item, plus per-episode titles/overviews/stills
					// applied below.
					if haveSeason {
						m.applySeasonData(ctx, &season, seriesTmdb[showDir], sr)
					}
					ss.Season = &season
				}
				g.save.Seasons = append(g.save.Seasons, ss)
				si = len(g.save.Seasons) - 1
				g.seasonIdx[seasonPath] = si
			}

			item := store.Item{
				LibraryID: lib.ID, Kind: store.KindEpisode,
				Title: ep.Title, SeasonNum: ep.Season, EpisodeNum: ep.Episode, Path: path,
				AirDate: ep.AirDate,
			}
			// Probe first: the placeholder below must not stand in for a
			// parsed title, or a genuine title tag would be rejected as a
			// mismatch.
			m.applyProbe(&item, probes[bi])
			if item.Title == "" {
				item.Title = fmt.Sprintf("Episode %d", ep.Episode)
			}
			// TMDB episode data fills whatever tags and filenames didn't
			// provide.
			var guests []metadata.Person
			if haveSeason {
				guests = m.applyEpisodeData(ctx, &item, seriesTmdb[showDir], ep.Season, ep.Episode, sr)
			}
			if item.Title == fmt.Sprintf("Episode %d", ep.Episode) && ep.AirDate != "" {
				// A daily show's episode the provider could not place is
				// better known by its date than by a number derived from it.
				item.Title = ep.AirDate
			}
			// The TMDB still is the episode's image: a landscape frame from
			// this episode, which is what an episode list shows. Embedded
			// cover art is only the fallback, read when there is no still,
			// because in an episode it is nearly always the series' or
			// season's portrait cover and would make every row of a season
			// look the same.
			if item.Poster == "" {
				item.Poster = m.embeddedArt(probes[bi])
			}
			plan.stamp(&item, path)
			save := store.VideoSave{Item: item, Streams: true}
			// Guest stars are the people in this episode and no other,
			// which is the only thing an episode-level cast is for — the
			// regulars are already credited on the series.
			save.Cast, _ = m.creditsOf(ctx, metadata.VideoResult{Cast: guests})
			save.Subtitles, save.AudioTracks = streamsOf(path, probes[bi])
			g.save.Seasons[si].Episodes = append(g.save.Seasons[si].Episodes, save)
		}

		for _, g := range groups {
			if err := m.Store.SaveSeries(ctx, &g.save); err != nil {
				return len(files), created, err
			}
			if g.save.Series != nil {
				plan.keep[g.save.Saved.ID] = true
				plan.looked[g.save.Saved.ID] = true
				seriesIDs[g.dir] = g.save.Saved.ID
				created++
			}
			for _, ss := range g.save.Seasons {
				if ss.Season != nil {
					plan.keep[ss.Saved.ID] = true
					seasonIDs[ss.Saved.Path] = ss.Saved.ID
					created++
				}
				for _, ep := range ss.SavedEpisodes {
					plan.keep[ep.ID] = true
					plan.noteNew(ep.Path, ep)
					created++
				}
			}
		}
		// The batch's last series may carry on into the next batch; the
		// enrichment job running alongside leaves it until it is whole.
		if plan.deferLookups && len(groups) > 0 {
			m.setOpenSeries(lib.ID, groups[len(groups)-1].dir)
		}
	}

	// A series still on air changes without any file changing: its next
	// episode date, its status, its season count. Those were refreshed by
	// every scan when every scan redid everything, so the series that were
	// skipped above are still refreshed here — one lookup each, and only
	// for series that have not ended.
	if !plan.full && video != nil {
		for dir, st := range plan.stamps {
			if ctx.Err() != nil {
				return len(files), created, ctx.Err()
			}
			// An unmatched series has no status either, but looking it up
			// again every scan is the retry's job, on its own slower
			// cadence (see retryUnmatched).
			if st.Kind != store.KindSeries || st.Status == "Ended" || st.Status == "Canceled" ||
				st.TmdbID == 0 || !plan.keep[st.ID] || seriesIDs[dir] != "" {
				continue
			}
			if t, _ := showName(filepath.Base(dir)); t == "" {
				continue
			}
			if _, err := m.saveSeriesWith(ctx, lib, dir, "", 0, video); err != nil {
				return len(files), created, err
			}
		}
	}
	return len(files), created, nil
}

// savedSeries is a series row as saveSeries left it, with what the
// provider said about it.
type savedSeries struct {
	store.Item
	// result is the provider's match; zero when there was none.
	result metadata.VideoResult
	// lookupErr is why there was none: metadata.ErrNotFound when the
	// provider knows no such series, anything else when it could not be
	// asked. Nil when matched, or when no provider is configured.
	lookupErr error
}

// saveSeries matches a series folder against the provider and saves it,
// with its genres and credits. The folder names the series; the fallback
// title and year (from an episode's filename) are used only when the
// folder name yields nothing. A series the provider does not know has the
// failed lookup recorded, which spaces out its retries.
func (m *Manager) saveSeries(ctx context.Context, lib store.Library, showDir, fallbackTitle string, fallbackYear int) (savedSeries, error) {
	return m.saveSeriesWith(ctx, lib, showDir, fallbackTitle, fallbackYear, m.Video)
}

// saveSeriesWith is saveSeries against the given provider: nil when a big
// scan registers series without looking them up, leaving that to the
// enrichment job (scanPlan.deferLookups).
func (m *Manager) saveSeriesWith(ctx context.Context, lib store.Library, showDir, fallbackTitle string, fallbackYear int, video metadata.VideoProvider) (savedSeries, error) {
	series, res, lookupErr := m.prepareSeries(ctx, lib, showDir, fallbackTitle, fallbackYear, video)
	sv := store.SeriesSave{Series: &series}
	if err := m.Store.SaveSeries(ctx, &sv); err != nil {
		return savedSeries{}, err
	}
	return savedSeries{Item: sv.Saved, result: res, lookupErr: lookupErr}, nil
}

// prepareSeries matches a series folder against the provider and gathers
// what its row is made of: the match, its genres and credits (headshots
// downloaded here), and the failed lookup to record when the provider
// does not know it. The folder names the series; the fallback title and
// year (from an episode's filename) are used only when the folder name
// yields nothing. Nothing is written.
func (m *Manager) prepareSeries(ctx context.Context, lib store.Library, showDir, fallbackTitle string, fallbackYear int, video metadata.VideoProvider) (store.VideoSave, metadata.VideoResult, error) {
	title, year := showName(filepath.Base(showDir))
	if title == "" {
		title, year = fallbackTitle, fallbackYear
	}
	series := store.Item{
		LibraryID: lib.ID, Kind: store.KindSeries, Title: title, Path: showDir, Year: year,
	}
	var res metadata.VideoResult
	var lookupErr error
	if video != nil {
		r, err := video.SearchTV(ctx, title, year)
		if err == nil && r.TmdbID == 0 {
			err = metadata.ErrNotFound
		}
		if err == nil {
			res = r
			m.applyVideoResult(ctx, &series, r)
		} else {
			lookupErr = err
		}
	}
	// Genres and credits live in their own tables. The film scanner has
	// always saved them; the series scanner never did, which is why every
	// series had an empty genre list and no cast while every film had both.
	save := store.VideoSave{Item: series, Genres: series.Genres}
	save.Cast, save.Crew = m.creditsOf(ctx, res)
	if errors.Is(lookupErr, metadata.ErrNotFound) {
		save.MatchFailedAt = m.now()
	}
	return save, res, lookupErr
}

// seriesGroup is one series' share of a probe batch, on its way to
// SaveSeries: which season of the batch each season path is.
type seriesGroup struct {
	dir       string
	save      store.SeriesSave
	seasonIdx map[string]int
}

// applyProbe copies an already-read probe onto an item: technical info plus
// embedded metadata. Tags refine filename parsing (they carry correct
// punctuation, years, and synopses in iTunes-style files) and attached cover
// art becomes the item's poster — except for an episode, whose poster is
// decided by scanTV once it knows whether TMDB has a still. The title tag is only taken when
// PreferTagTitle accepts it — scene releases stamp their own release name
// there, which would otherwise overwrite a perfectly good title parsed from
// a clean path.
func (m *Manager) applyProbe(item *store.Item, r probed) {
	item.SizeBytes = r.SizeBytes
	if r.Err != nil {
		logger.Warn("probe failed", "path", r.Path, "err", r.Err)
		return
	}
	info := r.Info
	item.RuntimeSecs = info.RuntimeSecs
	item.Container = info.Container
	item.VideoCodec = info.VideoCodec
	item.AudioCodec = info.AudioCodec
	item.Width = info.Width
	item.Height = info.Height

	t := info.Tags
	if title, ok := PreferTagTitle(t.Title, item.Title); ok {
		item.Title = title
	}
	if t.Year != 0 && item.Year == 0 {
		item.Year = t.Year
	}
	if t.Overview != "" && item.Overview == "" {
		item.Overview = t.Overview
	}
	if t.SeasonNum != 0 && item.Kind == store.KindEpisode {
		item.SeasonNum = t.SeasonNum
	}
	if t.EpisodeNum != 0 && item.Kind == store.KindEpisode {
		item.EpisodeNum = t.EpisodeNum
	}
	if item.Kind != store.KindEpisode && item.Poster == "" {
		item.Poster = m.embeddedArt(r)
	}
}

// embeddedArt caches a probed file's attached cover art and returns its
// serve path, or "" when it has none.
func (m *Manager) embeddedArt(r probed) string {
	if r.Err != nil || !r.Info.HasCoverArt {
		return ""
	}
	extract := m.CoverArt
	if extract == nil {
		extract = media.ExtractCoverArt
	}
	img := extract(r.Path)
	if img == nil {
		return ""
	}
	return m.cacheArtwork("embed", r.Path, img)
}

// streamsOf is a file's subtitle tracks, the embedded streams from its
// probe plus sidecar files next to it, and its audio tracks from the
// probe, so the player can offer its pickers without reprobing.
func streamsOf(path string, r probed) ([]store.SubtitleTrack, []store.AudioTrack) {
	subs := []store.SubtitleTrack{}
	for _, s := range r.Info.Subtitles {
		subs = append(subs, store.SubtitleTrack{
			Source: "embedded", StreamIndex: s.Index, Codec: s.Codec,
			Language: s.Language, Title: s.Title, Forced: s.Forced,
		})
	}
	for _, s := range media.SidecarSubtitles(path) {
		subs = append(subs, store.SubtitleTrack{
			Source: "sidecar", StreamIndex: -1, Path: s.Path, Codec: s.Codec,
			Language: s.Language, Forced: s.Forced,
		})
	}
	audio := make([]store.AudioTrack, 0, len(r.Info.AudioTracks))
	for _, a := range r.Info.AudioTracks {
		audio = append(audio, store.AudioTrack{
			StreamIndex: a.Index, Codec: a.Codec, Language: a.Language,
			Title: a.Title, Channels: a.Channels, BitRate: a.BitRate, Default: a.Default,
		})
	}
	return subs, audio
}

func (m *Manager) applyVideoResult(ctx context.Context, item *store.Item, r metadata.VideoResult) {
	item.TmdbID = r.TmdbID
	item.Overview = r.Overview
	item.Rating, item.VoteCount = r.Rating, r.VoteCount
	item.Tagline = r.Tagline
	item.Certification = r.CertificationFor(m.CertRegion)
	item.CollectionID, item.CollectionName = r.CollectionID, r.CollectionName
	// Television only; a film leaves these empty.
	item.Status, item.Network = r.Status, r.Network
	item.SeasonCount, item.EpisodeCount = r.SeasonCount, r.EpisodeCount
	item.LastAirDate, item.NextAirDate = r.LastAired, r.NextAired
	item.OriginalTitle, item.Country = r.OriginalTitle, r.Country
	if r.FirstAired != "" {
		item.AirDate = r.FirstAired
	}
	// Carried on the item so the caller can store them once the row has an
	// id; genres live in their own table.
	item.Genres = r.Genres
	if r.Year > 0 && item.Year == 0 {
		item.Year = r.Year
	}
	if r.BackdropURL != "" && m.ArtworkDir != "" {
		name := "tmdb-" + strconv.Itoa(r.TmdbID) + "-backdrop"
		if file, err := metadata.DownloadArtwork(ctx, http.DefaultClient, r.BackdropURL, m.ArtworkDir, name); err == nil {
			item.Backdrop = "/artwork/" + file
		} else {
			artworkLog.Warn("backdrop download failed", "title", item.Title, "err", err)
		}
	}
	if r.PosterURL != "" && m.ArtworkDir != "" {
		name := "tmdb-" + strconv.Itoa(r.TmdbID)
		if file, err := metadata.DownloadArtwork(ctx, http.DefaultClient, r.PosterURL, m.ArtworkDir, name); err == nil {
			item.Poster = "/artwork/" + file
		} else {
			artworkLog.Warn("poster download failed", "title", item.Title, "err", err)
		}
	}
}

// AnyScanning reports whether any scan is running in this process.
func (m *Manager) AnyScanning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.running) > 0
}

// Scanning reports whether a scan of this library is running in this
// process. Used to tell a client that a library is filling up rather than
// empty — one bit, with none of the progress detail the "scan" events carry,
// so it is safe to show any signed-in user.
func (m *Manager) Scanning(libraryID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.running[libraryID]
	return ok
}

// seriesDirFor returns the series folder for an episode: the first directory
// beneath whichever library root contains the path.
//
// The layout below that varies and cannot be assumed. Most libraries use
// Show/Season NN/file, but episodes also sit directly in the show folder,
// and some keep a release folder per episode
// (Show/Season 03/Show.S03E01.1080p.WEB-GROUP/file.mkv). Taking the
// grandparent as the series — as this did — turns a season folder into a
// series whenever anything is nested one level deeper, which is how this
// library ended up with "Alone" listed three times, twice pointing at a
// season.
//
// Returns "" when the path is not under any root, or is a file sitting in
// the root itself with no folder of its own.
func seriesDirFor(roots []string, path string) string {
	dir := filepath.Dir(path)
	for _, root := range roots {
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		first := strings.Split(filepath.ToSlash(rel), "/")[0]
		if first == "" || first == "." {
			continue
		}
		// A root whose own children are season folders is a single show's
		// directory, added as a library in its own right. The root is then
		// the series, and taking "Season 01" as one produces a library of
		// series called "Season 01" with no show anywhere — and season
		// lookups against a series id that was never found.
		if _, isSeason := seasonFromDir(first); isSeason {
			return root
		}
		return filepath.Join(root, first)
	}
	return ""
}

// seasonDirFor returns the season folder for an episode: the nearest
// ancestor named "Season N" between the file and its series folder.
//
// Falls back to the series folder when there is none, which is the correct
// answer for a show whose episodes sit loose in its own directory — they are
// all one season's worth as far as the layout says, and the SxxEyy marker in
// the filename supplies the real number.
func seasonDirFor(seriesDir, path string) string {
	dir := filepath.Dir(path)
	for dir != seriesDir && strings.HasPrefix(dir, seriesDir) {
		if _, ok := seasonFromDir(filepath.Base(dir)); ok {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return seriesDir
}
