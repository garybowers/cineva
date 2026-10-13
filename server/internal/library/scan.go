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
	// CertRegion is the ISO 3166-1 country whose content rating is stored
	// ("GB", "US", "DE"). Certification is regional — a BBFC 15 and an FSK
	// 16 are different boards' judgements, not two spellings — so which one
	// is meaningful depends on where the viewer is. Set from the admin
	// setting; "" falls back to US inside CertificationFor.
	CertRegion string

	// Storage, when set, is consulted before a scan walks anything: a scan
	// of a share that has stopped answering would otherwise hang on its
	// first directory read.
	Storage *StorageMonitor

	// OnScanDone, when set, is called after a scan completes successfully —
	// how the server starts work that depends on what a scan found.
	OnScanDone func(lib store.Library)

	mu      sync.Mutex
	running map[string]context.CancelFunc // libraryID -> cancel for the active scan
	again   map[string]bool               // libraryID -> rescan once the active scan ends
}

func NewManager(st *store.Store, video metadata.VideoProvider, artworkDir string) *Manager {
	return &Manager{
		Store:      st,
		Video:      video,
		ArtworkDir: artworkDir,
		Probe:      media.Probe,
		running:    map[string]context.CancelFunc{},
		again:      map[string]bool{},
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
		return fmt.Errorf("scan is not running")
	}
	m.mu.Lock()
	cancel, ok := m.running[sc.LibraryID]
	m.mu.Unlock()
	if ok {
		cancel() // runScan records the row as cancelled
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
func (m *Manager) RequestScan(ctx context.Context, lib store.Library) error {
	m.mu.Lock()
	if _, ok := m.running[lib.ID]; ok {
		m.again[lib.ID] = true
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	_, err := m.StartScan(ctx, lib, false)
	return err
}

// rescanIfRequested starts the follow-up RequestScan queued during a scan.
// The library is read again, since it may have been edited or deleted
// while the scan ran.
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
	if _, err := m.StartScan(ctx, lib, false); err != nil {
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
		if err := m.Store.UpdateScanProgress(context.WithoutCancel(ctx), scan.ID, seen, created); err != nil {
			logger.Warn("saving scan progress failed", "library", lib.Name, "err", err)
		}
		m.Events.Publish(events.Event{Type: "scan", Data: scanEvent{Scan: snap, FilesTotal: total}})
		if created > itemsSent {
			itemsSent = created
			m.Events.Publish(events.Event{Type: "items", Data: itemsEvent{LibraryID: lib.ID, Scanning: true}})
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

	if scanErr == nil && filesSeen > 0 {
		if pruned, err := m.Store.PruneExcept(context.WithoutCancel(ctx), lib.ID, plan.keep); err != nil {
			logger.Error("pruning stale items failed", "library", lib.Name, "err", err)
		} else if pruned > 0 {
			logger.Info("pruned stale items", "library", lib.Name, "count", pruned)
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
	if status == "completed" && m.OnScanDone != nil {
		m.OnScanDone(lib)
	}
	done := scan
	done.Status, done.Error = status, errMsg
	done.FilesSeen, done.ItemsCreated = filesSeen, itemsCreated
	m.Events.Publish(events.Event{Type: "scan", Data: scanEvent{Scan: done}})
	// The throttle means the last upserts (and any prune) came after the
	// final report, so close with an unconditional refetch hint. Scanning is
	// false here: this fires once the scan is over, and a client left
	// believing one is still running would show the wrong empty state for
	// good.
	m.Events.Publish(events.Event{Type: "items", Data: itemsEvent{LibraryID: lib.ID, Scanning: false}})
	m.Events.Publish(events.Event{Type: "libraries"})
}

// reportFunc receives scanner progress: files handled, items upserted, and
// the total files found by the upfront listing.
type reportFunc func(seen, created, total int)

func (m *Manager) videoFiles(lib store.Library) ([]string, error) {
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
	files, err := m.videoFiles(lib)
	if err != nil {
		return 0, 0, err
	}
	todo := plan.changed(files)
	skipped := len(files) - len(todo)
	created := 0
	// Files are probed a batch at a time in parallel (see probeBatch); the
	// rest of the loop stays serial, because metadata lookups are rate
	// limited and SQLite takes one writer.
	for start := 0; start < len(todo); start += probeBatchSize {
		end := min(start+probeBatchSize, len(todo))
		batch := todo[start:end]
		probes := m.probeBatch(ctx, batch)

		for bi, path := range batch {
			i := skipped + start + bi
			if err := ctx.Err(); err != nil {
				return len(files), created, err
			}
			report(i, created, len(files))
			info := ParseMovie(path)
			item := store.Item{
				LibraryID: lib.ID, Kind: store.KindMovie,
				Title: info.Title, Year: info.Year, Path: path,
			}
			m.applyProbe(&item, probes[bi])

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
			if m.Video != nil {
				// An explicit id in the filename is the operator telling us the
				// answer, so it skips searching entirely — that is the whole
				// point of the convention. Anything else searches with the
				// tag-corrected title/year, since embedded tags restore
				// punctuation the filename mangles.
				var r metadata.VideoResult
				var err error
				if info.TmdbID > 0 {
					r, err = m.Video.MovieByID(ctx, info.TmdbID)
					if err != nil {
						// A bad hint must not silently become a wrong match, so
						// fall back to searching rather than to nothing.
						metadataLog.Warn("tmdb id hint failed, searching instead", "path", path, "tmdb_id", info.TmdbID, "err", err)
						r, err = m.Video.SearchMovie(ctx, item.Title, item.Year)
					}
				} else {
					r, err = m.Video.SearchMovie(ctx, item.Title, item.Year)
				}
				if err == nil {
					videoResult = r
					m.applyVideoResult(ctx, &item, r)
				}
			}
			// Applied after the provider so the operator's own files win: they
			// chose to put them there, and a local poster is the whole point of
			// curating one.
			if hasNFO {
				applyNFO(&item, nfo)
			}
			m.applyLocalArtwork(&item, path)
			plan.stamp(&item, path)

			saved, err := m.Store.UpsertItem(ctx, item)
			if err != nil {
				return len(files), created, err
			}
			plan.keep[saved.ID] = true
			m.saveGenres(ctx, saved.ID, item.Genres)
			m.saveCredits(ctx, saved.ID, videoResult)
			m.syncSubtitles(ctx, saved.ID, path)
			m.syncAudioTracks(ctx, saved.ID, path)
			created++
		}
	}
	return len(files), created, nil
}

// saveCredits stores an item's cast and crew, downloading each headshot
// once. People are shared across films, so a cast member in twenty titles
// costs one row and one image, not twenty.
func (m *Manager) saveCredits(ctx context.Context, itemID string, r metadata.VideoResult) {
	if len(r.Cast) == 0 && len(r.Crew) == 0 {
		return
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
	cast := convert(r.Cast, func(p metadata.Person) string { return p.Character })
	crew := convert(r.Crew, func(p metadata.Person) string { return p.Job })
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
	files, err := m.videoFiles(lib)
	if err != nil {
		return 0, 0, err
	}
	todo := plan.changed(files)
	skipped := len(files) - len(todo)
	created := 0
	seriesIDs := map[string]string{}                 // show dir -> item id
	seriesTmdb := map[string]int{}                   // show dir -> tmdb id
	seasonIDs := map[string]string{}                 // season dir -> item id
	seasonData := map[string]metadata.SeasonResult{} // season dir -> tmdb season

	// Files are probed a batch at a time in parallel, as the film scanner
	// does; the rest stays serial for the provider's rate limit and
	// SQLite's single writer. Probing one file at a time made a first scan
	// of a ten-thousand-episode library take most of a day.
	var probes []probed
	batchStart := 0
	for i, path := range todo {
		if err := ctx.Err(); err != nil {
			return len(files), created, err
		}
		if i%probeBatchSize == 0 {
			probes = m.probeBatch(ctx, todo[i:min(i+probeBatchSize, len(todo))])
			batchStart = i
		}
		report(skipped+i, created, len(files))
		ep, ok := ParseEpisode(path)
		if !ok {
			logger.Debug("no SxxEyy marker, skipping", "path", path)
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
		seasonDir := seasonDirFor(showDir, path)

		// Only reached for a changed file, so its series and season are
		// matched again: a new episode is the likeliest moment for the
		// provider's series and season data to have moved on too.
		seriesID, ok := seriesIDs[showDir]
		if !ok {
			saved, err := m.saveSeries(ctx, lib, showDir, ep.Show, ep.Year)
			if err != nil {
				return len(files), created, err
			}
			plan.keep[saved.ID] = true
			seriesID, seriesIDs[showDir] = saved.ID, saved.ID
			seriesTmdb[showDir] = saved.TmdbID
			created++
		}

		seasonID, ok := seasonIDs[seasonDir]
		if !ok {
			season := store.Item{
				LibraryID: lib.ID, Kind: store.KindSeason, ParentID: seriesID,
				Title: fmt.Sprintf("Season %02d", ep.Season), SeasonNum: ep.Season, Path: seasonDir,
			}
			// Season-level TMDB data: poster + overview for the season item,
			// plus per-episode titles/overviews/stills applied below.
			if m.Video != nil && seriesTmdb[showDir] != 0 {
				if sr, err := m.Video.TVSeason(ctx, seriesTmdb[showDir], ep.Season); err == nil {
					seasonData[seasonDir] = sr
					season.Overview = sr.Overview
					// A season could not be re-matched or refreshed on its
					// own without its own id, the way a film can.
					season.TmdbID = sr.TmdbID
					season.AirDate = sr.AirDate
					if len(sr.AirDate) >= 4 {
						if y, err := strconv.Atoi(sr.AirDate[:4]); err == nil {
							season.Year = y
						}
					}
					if sr.PosterURL != "" && m.ArtworkDir != "" {
						name := fmt.Sprintf("tmdb-s%d-season%d", seriesTmdb[showDir], ep.Season)
						if file, err := metadata.DownloadArtwork(ctx, http.DefaultClient, sr.PosterURL, m.ArtworkDir, name); err == nil {
							season.Poster = "/artwork/" + file
						}
					}
				}
			}
			saved, err := m.Store.UpsertItem(ctx, season)
			if err != nil {
				return len(files), created, err
			}
			plan.keep[saved.ID] = true
			seasonID, seasonIDs[seasonDir] = saved.ID, saved.ID
			created++
		}

		item := store.Item{
			LibraryID: lib.ID, Kind: store.KindEpisode, ParentID: seasonID,
			Title: ep.Title, SeasonNum: ep.Season, EpisodeNum: ep.Episode, Path: path,
		}
		// Probe first: the placeholder below must not stand in for a parsed
		// title, or a genuine title tag would be rejected as a mismatch.
		m.applyProbe(&item, probes[i-batchStart])
		if item.Title == "" {
			item.Title = fmt.Sprintf("Episode %d", ep.Episode)
		}
		// TMDB episode data fills whatever tags and filenames didn't provide.
		if sr, ok := seasonData[seasonDir]; ok {
			for _, te := range sr.Episodes {
				if te.Num != ep.Episode {
					continue
				}
				if item.Overview == "" {
					item.Overview = te.Overview
				}
				if item.Title == fmt.Sprintf("Episode %d", ep.Episode) && te.Title != "" {
					item.Title = te.Title
				}
				if te.StillURL != "" && m.ArtworkDir != "" {
					name := fmt.Sprintf("tmdb-s%d-e%dx%d", seriesTmdb[showDir], ep.Season, ep.Episode)
					if file, err := metadata.DownloadArtwork(ctx, http.DefaultClient, te.StillURL, m.ArtworkDir, name); err == nil {
						item.Poster = "/artwork/" + file
					}
				}
				item.TmdbID = te.TmdbID
				item.Rating, item.VoteCount = te.Rating, te.VoteCount
				// The broadcast date is the only date an episode has. Two
				// thirds of this library's episodes had none at all, which
				// also left nothing correct to sort them by within a year.
				item.AirDate = te.AirDate
				if item.Year == 0 && len(te.AirDate) >= 4 {
					if y, err := strconv.Atoi(te.AirDate[:4]); err == nil {
						item.Year = y
					}
				}
				break
			}
		}
		// The TMDB still is the episode's image: a landscape frame from this
		// episode, which is what an episode list shows. Embedded cover art is
		// only the fallback, read when there is no still, because in an
		// episode it is nearly always the series' or season's portrait cover
		// and would make every row of a season look the same.
		if item.Poster == "" {
			item.Poster = m.embeddedArt(probes[i-batchStart])
		}
		guests := episodeGuests(seasonData[seasonDir], ep.Episode)
		plan.stamp(&item, path)
		saved, err := m.Store.UpsertItem(ctx, item)
		if err != nil {
			return len(files), created, err
		}
		plan.keep[saved.ID] = true
		// Guest stars are the people in this episode and no other, which is
		// the only thing an episode-level cast is for — the regulars are
		// already credited on the series.
		if len(guests) > 0 {
			m.saveCredits(ctx, saved.ID, metadata.VideoResult{Cast: guests})
		}
		m.syncSubtitles(ctx, saved.ID, path)
		m.syncAudioTracks(ctx, saved.ID, path)
		created++
	}

	// A series still on air changes without any file changing: its next
	// episode date, its status, its season count. Those were refreshed by
	// every scan when every scan redid everything, so the series that were
	// skipped above are still refreshed here — one lookup each, and only
	// for series that have not ended.
	if !plan.full && m.Video != nil {
		for dir, st := range plan.stamps {
			if ctx.Err() != nil {
				return len(files), created, ctx.Err()
			}
			if st.Kind != store.KindSeries || st.Status == "Ended" || st.Status == "Canceled" ||
				!plan.keep[st.ID] || seriesIDs[dir] != "" {
				continue
			}
			if t, _ := showName(filepath.Base(dir)); t == "" {
				continue
			}
			if _, err := m.saveSeries(ctx, lib, dir, "", 0); err != nil {
				return len(files), created, err
			}
		}
	}
	return len(files), created, nil
}

// saveSeries matches a series folder against the provider and saves it,
// with its genres and credits. The folder names the series; the fallback
// title and year (from an episode's filename) are used only when the
// folder name yields nothing.
func (m *Manager) saveSeries(ctx context.Context, lib store.Library, showDir, fallbackTitle string, fallbackYear int) (store.Item, error) {
	title, year := showName(filepath.Base(showDir))
	if title == "" {
		title, year = fallbackTitle, fallbackYear
	}
	series := store.Item{
		LibraryID: lib.ID, Kind: store.KindSeries, Title: title, Path: showDir, Year: year,
	}
	var seriesResult metadata.VideoResult
	if m.Video != nil {
		if r, err := m.Video.SearchTV(ctx, title, year); err == nil {
			seriesResult = r
			m.applyVideoResult(ctx, &series, r)
		}
	}
	saved, err := m.Store.UpsertItem(ctx, series)
	if err != nil {
		return store.Item{}, err
	}
	// Genres and credits live in their own tables, so they need saving
	// explicitly after the row has an id. The film scanner has always done
	// this; the series scanner never did, which is why every series had an
	// empty genre list and no cast while every film had both.
	m.saveGenres(ctx, saved.ID, series.Genres)
	m.saveCredits(ctx, saved.ID, seriesResult)
	return saved, nil
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

// syncSubtitles records an item's subtitle tracks: embedded streams plus
// sidecar files next to the media.
func (m *Manager) syncSubtitles(ctx context.Context, itemID, path string) {
	tracks := []store.SubtitleTrack{}
	embedded, err := media.ProbeSubtitles(path)
	if err != nil {
		logger.Warn("subtitle probe failed", "path", path, "err", err)
	}
	for _, s := range embedded {
		tracks = append(tracks, store.SubtitleTrack{
			Source: "embedded", StreamIndex: s.Index, Codec: s.Codec,
			Language: s.Language, Title: s.Title, Forced: s.Forced,
		})
	}
	for _, s := range media.SidecarSubtitles(path) {
		tracks = append(tracks, store.SubtitleTrack{
			Source: "sidecar", StreamIndex: -1, Path: s.Path, Codec: s.Codec,
			Language: s.Language, Forced: s.Forced,
		})
	}
	if err := m.Store.ReplaceSubtitles(ctx, itemID, tracks); err != nil {
		logger.Error("saving subtitles failed", "path", path, "err", err)
	}
}

// syncAudioTracks catalogues the file's audio streams so the player can
// offer a picker without reprobing, mirroring syncSubtitles.
func (m *Manager) syncAudioTracks(ctx context.Context, itemID, path string) {
	streams, err := media.ProbeAudioTracks(path)
	if err != nil {
		logger.Warn("audio track probe failed", "path", path, "err", err)
		return
	}
	tracks := make([]store.AudioTrack, 0, len(streams))
	for _, a := range streams {
		tracks = append(tracks, store.AudioTrack{
			StreamIndex: a.Index, Codec: a.Codec, Language: a.Language,
			Title: a.Title, Channels: a.Channels, BitRate: a.BitRate, Default: a.Default,
		})
	}
	if err := m.Store.ReplaceAudioTracks(ctx, itemID, tracks); err != nil {
		logger.Error("saving audio tracks failed", "path", path, "err", err)
	}
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

// episodeGuests picks one episode's guest stars out of a season's data.
// Returns nothing when the season was never fetched or the episode is not
// in it, which is the normal case for a file TMDB has no record of.
func episodeGuests(sr metadata.SeasonResult, episodeNum int) []metadata.Person {
	for _, te := range sr.Episodes {
		if te.Num == episodeNum {
			return te.GuestStars
		}
	}
	return nil
}
