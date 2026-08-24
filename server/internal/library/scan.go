package library

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// Manager orchestrates library scans. Scans run in managed goroutines; the
// scans table carries progress so any client can observe it.
type Manager struct {
	Store      *store.Store
	Video      metadata.VideoProvider // nil when no TMDB key is configured
	ArtworkDir string
	Probe      func(path string) (media.Info, error)

	mu      sync.Mutex
	running map[string]bool // libraryID -> scan in progress
}

func NewManager(st *store.Store, video metadata.VideoProvider, artworkDir string) *Manager {
	return &Manager{
		Store:      st,
		Video:      video,
		ArtworkDir: artworkDir,
		Probe:      media.Probe,
		running:    map[string]bool{},
	}
}

// StartScan launches a scan in the background and returns its record.
func (m *Manager) StartScan(ctx context.Context, lib store.Library) (store.Scan, error) {
	m.mu.Lock()
	if m.running[lib.ID] {
		m.mu.Unlock()
		return store.Scan{}, fmt.Errorf("library %q is already being scanned", lib.Name)
	}
	m.running[lib.ID] = true
	m.mu.Unlock()

	scan, err := m.Store.CreateScan(ctx, lib.ID)
	if err != nil {
		m.clearRunning(lib.ID)
		return store.Scan{}, err
	}
	go func() {
		defer m.clearRunning(lib.ID)
		// Detached from the request context: a scan outlives its HTTP request.
		m.runScan(context.Background(), scan, lib)
	}()
	return scan, nil
}

// RunScanSync runs a scan in the foreground (used by the `cineva scan` CLI).
func (m *Manager) RunScanSync(ctx context.Context, lib store.Library) (store.Scan, error) {
	m.mu.Lock()
	if m.running[lib.ID] {
		m.mu.Unlock()
		return store.Scan{}, fmt.Errorf("library %q is already being scanned", lib.Name)
	}
	m.running[lib.ID] = true
	m.mu.Unlock()
	defer m.clearRunning(lib.ID)

	scan, err := m.Store.CreateScan(ctx, lib.ID)
	if err != nil {
		return store.Scan{}, err
	}
	m.runScan(ctx, scan, lib)
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

func (m *Manager) runScan(ctx context.Context, scan store.Scan, lib store.Library) {
	var filesSeen, itemsCreated int
	var scanErr error

	switch lib.MediaType {
	case store.MediaMovies:
		filesSeen, itemsCreated, scanErr = m.scanMovies(ctx, lib)
	case store.MediaTV:
		filesSeen, itemsCreated, scanErr = m.scanTV(ctx, lib)
	case store.MediaMusic:
		filesSeen, itemsCreated, scanErr = m.scanMusic(ctx, lib)
	default:
		scanErr = fmt.Errorf("scanner for %s libraries arrives in a later milestone", lib.MediaType)
	}

	status, errMsg := "completed", ""
	if scanErr != nil {
		status, errMsg = "failed", scanErr.Error()
		log.Printf("scan %s (%s): %v", lib.Name, scan.ID, scanErr)
	}
	if err := m.Store.FinishScan(ctx, scan.ID, status, errMsg, filesSeen, itemsCreated); err != nil {
		log.Printf("finish scan %s: %v", scan.ID, err)
	}
}

func (m *Manager) videoFiles(lib store.Library) ([]string, error) {
	var files []string
	for _, root := range lib.Paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && IsVideoFile(path) {
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

func (m *Manager) scanMovies(ctx context.Context, lib store.Library) (int, int, error) {
	files, err := m.videoFiles(lib)
	if err != nil {
		return 0, 0, err
	}
	created := 0
	for _, path := range files {
		info := ParseMovie(path)
		item := store.Item{
			LibraryID: lib.ID, Kind: store.KindMovie,
			Title: info.Title, Year: info.Year, Path: path,
		}
		m.fillFileInfo(&item, path)
		if m.Video != nil {
			// Search with the tag-corrected title/year when available —
			// embedded tags restore punctuation the filename mangles.
			if r, err := m.Video.SearchMovie(ctx, item.Title, item.Year); err == nil {
				m.applyVideoResult(ctx, &item, r)
			}
		}
		saved, err := m.Store.UpsertItem(ctx, item)
		if err != nil {
			return len(files), created, err
		}
		m.syncSubtitles(ctx, saved.ID, path)
		created++
	}
	return len(files), created, nil
}

func (m *Manager) scanTV(ctx context.Context, lib store.Library) (int, int, error) {
	files, err := m.videoFiles(lib)
	if err != nil {
		return 0, 0, err
	}
	created := 0
	seriesIDs := map[string]string{}                 // show dir -> item id
	seriesTmdb := map[string]int{}                   // show dir -> tmdb id
	seasonIDs := map[string]string{}                 // season dir -> item id
	seasonData := map[string]metadata.SeasonResult{} // season dir -> tmdb season

	for _, path := range files {
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
				Title: "Season " + strconv.Itoa(ep.Season), SeasonNum: ep.Season, Path: seasonDir,
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
		if item.Title == "" {
			item.Title = fmt.Sprintf("Episode %d", ep.Episode)
		}
		m.fillFileInfo(&item, path)
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
		created++
	}
	return len(files), created, nil
}

// fillFileInfo probes technical info plus embedded metadata: tags override
// filename parsing (they carry correct punctuation, years, and synopses in
// iTunes-style files), and attached cover art becomes the item's poster.
func (m *Manager) fillFileInfo(item *store.Item, path string) {
	if st, err := os.Stat(path); err == nil {
		item.SizeBytes = st.Size()
	}
	info, err := m.Probe(path)
	if err != nil {
		log.Printf("probe %s: %v", path, err)
		return
	}
	item.RuntimeSecs = info.RuntimeSecs
	item.Container = info.Container
	item.VideoCodec = info.VideoCodec
	item.AudioCodec = info.AudioCodec
	item.Width = info.Width
	item.Height = info.Height

	t := info.Tags
	if t.Title != "" {
		item.Title = t.Title
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
		if img := media.ExtractCoverArt(path); img != nil {
			if p := m.cacheArtwork("embed", path, img); p != "" {
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

func (m *Manager) applyVideoResult(ctx context.Context, item *store.Item, r metadata.VideoResult) {
	item.TmdbID = r.TmdbID
	item.Overview = r.Overview
	if r.Year > 0 && item.Year == 0 {
		item.Year = r.Year
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
