package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
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
	// CertRegion is the ISO 3166-1 country whose content rating is stored
	// ("GB", "US", "DE"). Certification is regional — a BBFC 15 and an FSK
	// 16 are different boards' judgements, not two spellings — so which one
	// is meaningful depends on where the viewer is. Set from the admin
	// setting; "" falls back to US inside CertificationFor.
	CertRegion string

	mu      sync.Mutex
	running map[string]context.CancelFunc // libraryID -> cancel for the active scan
}

func NewManager(st *store.Store, video metadata.VideoProvider, artworkDir string) *Manager {
	return &Manager{
		Store:      st,
		Video:      video,
		ArtworkDir: artworkDir,
		Probe:      media.Probe,
		running:    map[string]context.CancelFunc{},
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

// StartScan launches a scan in the background and returns its record.
func (m *Manager) StartScan(ctx context.Context, lib store.Library) (store.Scan, error) {
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
		defer m.clearRunning(lib.ID)
		defer cancel()
		m.runScan(runCtx, scan, lib)
	}()
	return scan, nil
}

// RunScanSync runs a scan in the foreground (used by the `cineva scan` CLI).
func (m *Manager) RunScanSync(ctx context.Context, lib store.Library) (store.Scan, error) {
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
	m.runScan(runCtx, scan, lib)
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
}

func (m *Manager) runScan(ctx context.Context, scan store.Scan, lib store.Library) {
	var filesSeen, itemsCreated int
	var scanErr error

	m.Events.Publish(events.Event{Type: "scan", Data: scanEvent{Scan: scan}})

	// Everything upserted this scan gets updated_at >= cutoff; whatever is
	// older afterwards belongs to files that vanished and is pruned.
	cutoff := time.Now().UTC().Add(-time.Second).Format("2006-01-02 15:04:05")

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
			log.Printf("scan progress %s: %v", lib.Name, err)
		}
		m.Events.Publish(events.Event{Type: "scan", Data: scanEvent{Scan: snap, FilesTotal: total}})
		if created > itemsSent {
			itemsSent = created
			m.Events.Publish(events.Event{Type: "items", Data: itemsEvent{LibraryID: lib.ID}})
		}
	}

	switch lib.MediaType {
	case store.MediaMovies:
		filesSeen, itemsCreated, scanErr = m.scanMovies(ctx, lib, report)
	case store.MediaTV:
		filesSeen, itemsCreated, scanErr = m.scanTV(ctx, lib, report)
	case store.MediaMusic:
		filesSeen, itemsCreated, scanErr = m.scanMusic(ctx, lib, report)
	default:
		scanErr = fmt.Errorf("scanner for %s libraries arrives in a later milestone", lib.MediaType)
	}

	if scanErr == nil && filesSeen > 0 {
		if pruned, err := m.Store.PruneUnseen(context.WithoutCancel(ctx), lib.ID, cutoff); err != nil {
			log.Printf("prune %s: %v", lib.Name, err)
		} else if pruned > 0 {
			log.Printf("scan %s: pruned %d stale item(s)", lib.Name, pruned)
		}
		// After pruning, so a removed file cannot leave an item pointing at
		// a primary that no longer exists.
		if lib.MediaType == store.MediaMovies {
			if linked, err := m.Store.GroupVersions(context.WithoutCancel(ctx), lib.ID); err != nil {
				log.Printf("group versions %s: %v", lib.Name, err)
			} else if linked > 0 {
				log.Printf("scan %s: grouped %d alternative version(s)", lib.Name, linked)
			}
		}
	}

	status, errMsg := "completed", ""
	if errors.Is(scanErr, context.Canceled) {
		status, errMsg = "failed", "cancelled"
		log.Printf("scan %s (%s): cancelled", lib.Name, scan.ID)
	} else if scanErr != nil {
		status, errMsg = "failed", scanErr.Error()
		log.Printf("scan %s (%s): %v", lib.Name, scan.ID, scanErr)
	}
	// The scan's own context may be cancelled; finish the row regardless.
	if err := m.Store.FinishScan(context.WithoutCancel(ctx), scan.ID, status, errMsg, filesSeen, itemsCreated); err != nil {
		log.Printf("finish scan %s: %v", scan.ID, err)
	}
	detail := fmt.Sprintf("%d files, %d items", filesSeen, itemsCreated)
	if errMsg != "" {
		detail += " · " + errMsg
	}
	if err := m.Store.RecordAudit(context.WithoutCancel(ctx), store.AuditEntry{
		UserID: "system", Action: "scan." + status, TargetKind: "library",
		TargetID: lib.ID, TargetTitle: lib.Name, Detail: detail,
	}); err != nil {
		log.Printf("audit scan: %v", err)
	}
	m.Events.Publish(events.Event{Type: "logs"})
	done := scan
	done.Status, done.Error = status, errMsg
	done.FilesSeen, done.ItemsCreated = filesSeen, itemsCreated
	m.Events.Publish(events.Event{Type: "scan", Data: scanEvent{Scan: done}})
	// The throttle means the last upserts (and any prune) came after the
	// final report, so close with an unconditional refetch hint.
	m.Events.Publish(events.Event{Type: "items", Data: itemsEvent{LibraryID: lib.ID}})
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

func (m *Manager) scanMovies(ctx context.Context, lib store.Library, report reportFunc) (int, int, error) {
	files, err := m.videoFiles(lib)
	if err != nil {
		return 0, 0, err
	}
	created := 0
	// Files are probed a batch at a time in parallel (see probeBatch); the
	// rest of the loop stays serial, because metadata lookups are rate
	// limited and SQLite takes one writer.
	for start := 0; start < len(files); start += probeBatchSize {
		end := min(start+probeBatchSize, len(files))
		batch := files[start:end]
		probes := m.probeBatch(ctx, batch)

		for bi, path := range batch {
			i := start + bi
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
				log.Printf("scan: %s: %v", filepath.Base(path), nerr)
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
						log.Printf("scan: %s: tmdbid=%d hint failed (%v), searching instead", filepath.Base(path), info.TmdbID, err)
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

			saved, err := m.Store.UpsertItem(ctx, item)
			if err != nil {
				return len(files), created, err
			}
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
		log.Printf("save credits for %s: %v", itemID, err)
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
		log.Printf("save genres for %s: %v", itemID, err)
	}
}

func (m *Manager) scanTV(ctx context.Context, lib store.Library, report reportFunc) (int, int, error) {
	files, err := m.videoFiles(lib)
	if err != nil {
		return 0, 0, err
	}
	created := 0
	seriesIDs := map[string]string{}                 // show dir -> item id
	seriesTmdb := map[string]int{}                   // show dir -> tmdb id
	seasonIDs := map[string]string{}                 // season dir -> item id
	seasonData := map[string]metadata.SeasonResult{} // season dir -> tmdb season

	for i, path := range files {
		if err := ctx.Err(); err != nil {
			return len(files), created, err
		}
		report(i, created, len(files))
		ep, ok := ParseEpisode(path)
		if !ok {
			log.Printf("scan: no SxxEyy marker, skipping %s", path)
			continue
		}
		seasonDir := filepath.Dir(path)
		showDir := filepath.Dir(seasonDir)

		seriesID, ok := seriesIDs[showDir]
		if !ok {
			series := store.Item{
				LibraryID: lib.ID, Kind: store.KindSeries, Title: ep.Show, Path: showDir,
			}
			if m.Video != nil {
				if r, err := m.Video.SearchTV(ctx, ep.Show); err == nil {
					m.applyVideoResult(ctx, &series, r)
				}
			}
			saved, err := m.Store.UpsertItem(ctx, series)
			if err != nil {
				return len(files), created, err
			}
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
			seasonID, seasonIDs[seasonDir] = saved.ID, saved.ID
			created++
		}

		item := store.Item{
			LibraryID: lib.ID, Kind: store.KindEpisode, ParentID: seasonID,
			Title: ep.Title, SeasonNum: ep.Season, EpisodeNum: ep.Episode, Path: path,
		}
		// Probe first: the placeholder below must not stand in for a parsed
		// title, or a genuine title tag would be rejected as a mismatch.
		m.fillFileInfo(&item, path)
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
				if item.Poster == "" && te.StillURL != "" && m.ArtworkDir != "" {
					name := fmt.Sprintf("tmdb-s%d-e%dx%d", seriesTmdb[showDir], ep.Season, ep.Episode)
					if file, err := metadata.DownloadArtwork(ctx, http.DefaultClient, te.StillURL, m.ArtworkDir, name); err == nil {
						item.Poster = "/artwork/" + file
					}
				}
				break
			}
		}
		saved, err := m.Store.UpsertItem(ctx, item)
		if err != nil {
			return len(files), created, err
		}
		m.syncSubtitles(ctx, saved.ID, path)
		m.syncAudioTracks(ctx, saved.ID, path)
		created++
	}
	return len(files), created, nil
}

// fillFileInfo probes technical info plus embedded metadata: tags refine
// filename parsing (they carry correct punctuation, years, and synopses in
// iTunes-style files) and attached cover art becomes the item's poster. The
// title tag is only taken when PreferTagTitle accepts it — scene releases
// stamp their own release name there, which would otherwise overwrite a
// perfectly good title parsed from a clean path.
// fillFileInfo probes the file and applies what it finds. Kept for the
// callers that handle one file at a time (a single-item metadata refresh);
// the scanners use probeBatch and applyProbe so the reads run in parallel.
func (m *Manager) fillFileInfo(item *store.Item, path string) {
	r := probed{Path: path}
	if st, err := os.Stat(path); err == nil {
		r.SizeBytes = st.Size()
	}
	r.Info, r.Err = m.Probe(path)
	m.applyProbe(item, r)
}

// applyProbe copies an already-read probe onto an item.
func (m *Manager) applyProbe(item *store.Item, r probed) {
	item.SizeBytes = r.SizeBytes
	if r.Err != nil {
		log.Printf("probe %s: %v", r.Path, r.Err)
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
	if info.HasCoverArt && item.Poster == "" {
		if img := media.ExtractCoverArt(r.Path); img != nil {
			if p := m.cacheArtwork("embed", r.Path, img); p != "" {
				item.Poster = p
			}
		}
	}
}

// syncSubtitles records an item's subtitle tracks: embedded streams plus
// sidecar files next to the media.
func (m *Manager) syncSubtitles(ctx context.Context, itemID, path string) {
	tracks := []store.SubtitleTrack{}
	embedded, err := media.ProbeSubtitles(path)
	if err != nil {
		log.Printf("probe subtitles %s: %v", path, err)
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
		log.Printf("save subtitles for %s: %v", path, err)
	}
}

// syncAudioTracks catalogues the file's audio streams so the player can
// offer a picker without reprobing, mirroring syncSubtitles.
func (m *Manager) syncAudioTracks(ctx context.Context, itemID, path string) {
	streams, err := media.ProbeAudioTracks(path)
	if err != nil {
		log.Printf("probe audio %s: %v", path, err)
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
		log.Printf("save audio tracks for %s: %v", path, err)
	}
}

func (m *Manager) applyVideoResult(ctx context.Context, item *store.Item, r metadata.VideoResult) {
	item.TmdbID = r.TmdbID
	item.Overview = r.Overview
	item.Rating, item.VoteCount = r.Rating, r.VoteCount
	item.Tagline = r.Tagline
	item.Certification = r.CertificationFor(m.CertRegion)
	item.CollectionID, item.CollectionName = r.CollectionID, r.CollectionName
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
			log.Printf("backdrop for %s: %v", item.Title, err)
		}
	}
	if r.PosterURL != "" && m.ArtworkDir != "" {
		name := "tmdb-" + strconv.Itoa(r.TmdbID)
		if file, err := metadata.DownloadArtwork(ctx, http.DefaultClient, r.PosterURL, m.ArtworkDir, name); err == nil {
			item.Poster = "/artwork/" + file
		} else {
			log.Printf("artwork for %s: %v", item.Title, err)
		}
	}
}
