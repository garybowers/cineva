package library

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

var musicLog = logging.For("music")

var audioExts = map[string]bool{
	".flac": true, ".mp3": true, ".m4a": true, ".aac": true,
	".ogg": true, ".opus": true, ".wav": true, ".wma": true,
}

// "1995 - Album Name" directory pattern.
var albumDirRe = regexp.MustCompile(`^(\d{4})\s*-\s*(.+)$`)

// "CD1" / "Disc 2" / "disk3" subdirectory inside a multi-disc album.
var discDirRe = regexp.MustCompile(`(?i)^(?:cd|disc|disk)\s*(\d{1,2})$`)

// "03 - Title.flac" filename pattern.
var trackFileRe = regexp.MustCompile(`^(\d{1,3})\s*-\s*(.+)$`)

// scanMusic builds artist -> album -> track from tags first, directory
// layout (Artist/Year - Album/NN - Title.ext) as fallback.
func (m *Manager) scanMusic(ctx context.Context, lib store.Library, plan *scanPlan, report reportFunc) (int, int, error) {
	var files []string
	// The cover images the walk passes, by folder: what albumArtwork reads,
	// and what an album's sidecar fingerprint is taken over.
	covers := map[string][]fs.DirEntry{}
	for _, root := range lib.Paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if audioExts[strings.ToLower(filepath.Ext(path))] {
				files = append(files, path)
			} else if coverRank(d.Name()) >= 0 {
				dir := filepath.Dir(path)
				covers[dir] = append(covers[dir], d)
			}
			return nil
		})
		if err != nil {
			return 0, 0, fmt.Errorf("walk %s: %w", root, err)
		}
	}
	sort.Strings(files)
	todo := plan.changed(files)
	skipped := len(files) - len(todo)

	art := map[string]string{} // album dir -> artwork already worked out by this scan
	m.rereadChangedCovers(ctx, files, todo, covers, plan, art)

	created := 0
	artistIDs := map[string]string{}              // artist name -> item id
	albumIDs := map[string]string{}               // album dir -> item id
	mbData := map[string]*metadata.MusicRelease{} // album dir -> MB release (nil = looked up, no match)
	probe := m.ProbeAudio
	if probe == nil {
		probe = media.ProbeAudio
	}

	for i, path := range todo {
		if err := ctx.Err(); err != nil {
			return len(files), created, err
		}
		report(skipped+i, created, len(files))
		info, err := probe(path)
		if err != nil {
			musicLog.Warn("audio probe failed", "path", path, "err", err)
			continue
		}
		albumDir, discFromDir := albumDirOf(path)
		artistDir := filepath.Dir(albumDir)

		artistName := info.AlbumArtist
		if artistName == "" {
			artistName = info.Artist
		}
		if artistName == "" {
			artistName = filepath.Base(artistDir)
			// Album folders directly under a library root have no artist
			// folder — don't invent an artist named after the root dir.
			for _, root := range lib.Paths {
				if artistDir == filepath.Clean(root) {
					artistName = "Unknown Artist"
					break
				}
			}
		}
		if info.Compilation {
			artistName = "Various Artists"
		}

		albumName := info.Album
		albumYear := info.Year
		if md := albumDirRe.FindStringSubmatch(filepath.Base(albumDir)); md != nil {
			if albumYear == 0 {
				albumYear, _ = strconv.Atoi(md[1])
			}
			if albumName == "" {
				albumName = md[2]
			}
		}
		if albumName == "" {
			albumName = filepath.Base(albumDir)
		}

		title := info.Title
		trackNum := info.TrackNum
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if mt := trackFileRe.FindStringSubmatch(base); mt != nil {
			if trackNum == 0 {
				trackNum, _ = strconv.Atoi(mt[1])
			}
			if title == "" {
				title = mt[2]
			}
		} else if n, err := strconv.Atoi(base); err == nil && trackNum == 0 {
			trackNum = n // bare "03.flac" style — number only, no title
		}
		if title == "" {
			title = base
		}

		// Artist: keyed by name; path is synthetic (an artist may span dirs).
		artistID, ok := artistIDs[artistName]
		if !ok {
			saved, err := m.Store.UpsertItem(ctx, store.Item{
				LibraryID: lib.ID, Kind: store.KindArtist,
				Title: artistName, Artist: artistName,
				Path: "artist://" + strings.ToLower(artistName),
			})
			if err != nil {
				return len(files), created, err
			}
			plan.keep[saved.ID] = true
			artistID, artistIDs[artistName] = saved.ID, saved.ID
			created++
		}

		albumID, ok := albumIDs[albumDir]
		if !ok {
			album := store.Item{
				LibraryID: lib.ID, Kind: store.KindAlbum, ParentID: artistID,
				Title: albumName, Artist: artistName, Year: albumYear, Path: albumDir,
				SidecarStamp: fingerprint(covers[albumDir]),
			}
			album.Poster = m.albumArtwork(path, albumDir, covers[albumDir], art)
			// MusicBrainz fills what tags and local art couldn't: only
			// queried when this album is actually missing something (the
			// 1 req/s limit makes blanket lookups unaffordable).
			if m.Music != nil && artistName != "Unknown Artist" &&
				(album.Poster == "" || album.Year == 0 || info.Title == "") {
				if rel, err := m.Music.SearchRelease(ctx, artistName, albumName); err == nil {
					mbData[albumDir] = &rel
					if album.Year == 0 {
						album.Year = rel.Year
					}
					if album.Poster == "" {
						if img, err := m.Music.FrontCover(ctx, rel.MBID); err == nil && img != nil {
							album.Poster = m.cacheArtwork("mb", albumDir, img)
						}
					}
					musicLog.Debug("musicbrainz match", "artist", artistName, "album", albumName, "mbid", rel.MBID)
				} else {
					mbData[albumDir] = nil
				}
			}
			saved, err := m.Store.UpsertItem(ctx, album)
			if err != nil {
				return len(files), created, err
			}
			plan.keep[saved.ID] = true
			plan.noteNew(albumDir, saved)
			albumID, albumIDs[albumDir] = saved.ID, saved.ID
			created++
		}

		displayArtist := info.Artist
		if displayArtist == "" {
			displayArtist = artistName
		}
		// Untagged files get canonical track titles from MusicBrainz.
		if info.Title == "" && trackNum > 0 {
			if rel := mbData[albumDir]; rel != nil {
				for _, mt := range rel.Tracks {
					if mt.Pos == trackNum && mt.Title != "" {
						title = mt.Title
						break
					}
				}
			}
		}
		discNum := info.DiscNum
		if discNum == 0 {
			discNum = discFromDir
		}
		track := store.Item{
			LibraryID: lib.ID, Kind: store.KindTrack, ParentID: albumID,
			Title: title, Artist: displayArtist, Year: albumYear,
			TrackNum: trackNum, DiscNum: discNum, Path: path,
			RuntimeSecs: info.RuntimeSecs, Container: info.Container,
			AudioCodec: info.Codec,
		}
		plan.stamp(&track, path)
		saved, err := m.Store.UpsertItem(ctx, track)
		if err != nil {
			return len(files), created, err
		}
		plan.keep[saved.ID] = true
		// Written even when empty, so a genre removed from the tags goes.
		if err := m.Store.ReplaceGenres(ctx, saved.ID, splitGenres(info.Genre)); err != nil {
			musicLog.Error("saving track genres failed", "path", path, "err", err)
		}
		// Tracks inherit their album's cover for player/list display.
		if album, err := m.Store.Item(ctx, albumID); err == nil && album.Poster != "" && saved.Poster != album.Poster {
			saved.Poster = album.Poster
			if _, err := m.Store.UpsertItem(ctx, saved); err != nil {
				musicLog.Error("saving track poster failed", "path", path, "err", err)
			}
		}
		created++
	}
	return len(files), created, nil
}

// cacheArtwork writes an image into the artwork cache under a stable name
// derived from (prefix, key) and returns its serve path ("" on failure or
// when no artwork dir is configured). Existing files are reused.
func (m *Manager) cacheArtwork(prefix, key string, img []byte) string {
	if m.ArtworkDir == "" || len(img) == 0 {
		return ""
	}
	sum := sha1.Sum([]byte(key))
	name := prefix + "-" + hex.EncodeToString(sum[:8]) + ".jpg"
	dest := filepath.Join(m.ArtworkDir, name)
	if _, err := os.Stat(dest); err == nil {
		return "/artwork/" + name
	}
	if err := os.MkdirAll(m.ArtworkDir, 0o755); err != nil {
		return ""
	}
	if err := os.WriteFile(dest, img, 0o644); err != nil {
		artworkLog.Warn("caching artwork failed", "key", key, "err", err)
		return ""
	}
	return "/artwork/" + name
}

// albumDirOf is the album folder a track belongs to, and the disc number
// its folder gives. In a multi-disc layout (Artist/Album/CD1/track) the
// disc folder is part of the album, not an album itself.
func albumDirOf(trackPath string) (string, int) {
	dir := filepath.Dir(trackPath)
	if md := discDirRe.FindStringSubmatch(filepath.Base(dir)); md != nil {
		disc, _ := strconv.Atoi(md[1])
		return filepath.Dir(dir), disc
	}
	return dir, 0
}

// coverNames are the album cover files read from an album folder, best
// first (compared without case).
var coverNames = []string{"cover.jpg", "cover.png", "folder.jpg", "folder.png", "front.jpg", "front.png"}

// coverRank is a file name's place in coverNames, or -1 when it is not an
// album cover.
func coverRank(name string) int {
	name = strings.ToLower(name)
	for i, c := range coverNames {
		if name == c {
			return i
		}
	}
	return -1
}

// splitGenres splits a genre tag that holds several ("Rock; Pop",
// "Jazz/Funk"), dropping blanks and repeats.
func splitGenres(tag string) []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range strings.FieldsFunc(tag, func(r rune) bool { return r == ';' || r == '/' || r == '|' || r == 0 }) {
		g = strings.TrimSpace(g)
		if g != "" && !seen[strings.ToLower(g)] {
			seen[strings.ToLower(g)] = true
			out = append(out, g)
		}
	}
	return out
}

// rereadChangedCovers picks up a cover image replaced, added or removed in
// an album folder whose tracks have not changed: the album's artwork is
// read again and handed to its tracks, and nothing else about them is
// touched — no probe, no lookup. An album with a changed track is left to
// the main loop, which reads its artwork anyway. An album with no
// fingerprint yet (scanned before covers were fingerprinted) adopts the
// current one rather than being read again.
func (m *Manager) rereadChangedCovers(ctx context.Context, files, todo []string,
	covers map[string][]fs.DirEntry, plan *scanPlan, art map[string]string) {
	rescanning := map[string]bool{}
	for _, path := range todo {
		dir, _ := albumDirOf(path)
		rescanning[dir] = true
	}
	seen := map[string]bool{}
	for _, path := range files {
		dir, _ := albumDirOf(path)
		if seen[dir] || rescanning[dir] {
			continue
		}
		seen[dir] = true
		st, ok := plan.stamps[dir]
		if !ok || st.Kind != store.KindAlbum {
			continue
		}
		stamp := fingerprint(covers[dir])
		switch st.SidecarStamp {
		case stamp:
			continue
		case 0:
			plan.adopt[st.ID] = stamp
			continue
		}
		poster := m.albumArtwork(path, dir, covers[dir], art)
		if err := m.Store.SetAlbumArtwork(ctx, st.ID, poster, stamp); err != nil {
			musicLog.Error("saving changed album cover failed", "album", dir, "err", err)
			continue
		}
		musicLog.Info("album cover changed", "album", dir)
	}
}

// albumArtwork caches the album's cover image — a cover file in the album
// folder, else art embedded in the track — and returns its serve path.
//
// The cached file is named for the folder and rewritten each time (see
// writeArtwork). Worked out once per album per scan (art).
func (m *Manager) albumArtwork(trackPath, albumDir string, coverFiles []fs.DirEntry, art map[string]string) string {
	if m.ArtworkDir == "" {
		return ""
	}
	if p, ok := art[albumDir]; ok {
		return p
	}
	var img []byte
	ranked := slices.Clone(coverFiles)
	slices.SortStableFunc(ranked, func(a, b fs.DirEntry) int { return coverRank(a.Name()) - coverRank(b.Name()) })
	for _, d := range ranked {
		if data, err := os.ReadFile(filepath.Join(albumDir, d.Name())); err == nil && len(data) > 0 {
			img = data
			break
		}
	}
	if img == nil {
		extract := m.CoverArt
		if extract == nil {
			extract = media.ExtractCoverArt
		}
		img = extract(trackPath)
	}
	if len(img) == 0 {
		art[albumDir] = ""
		return ""
	}
	p := m.writeArtwork("album", albumDir, img)
	art[albumDir] = p
	return p
}

// writeArtwork writes an image into the artwork cache under a name derived
// from (prefix, key), replacing what was there, and returns its serve
// path ("" on failure). The path carries a hash of the image as a query,
// so a replaced image gets a new URL and no browser goes on showing the
// old one from its cache.
func (m *Manager) writeArtwork(prefix, key string, img []byte) string {
	sum := sha1.Sum([]byte(key))
	name := prefix + "-" + hex.EncodeToString(sum[:8]) + ".jpg"
	if err := os.MkdirAll(m.ArtworkDir, 0o755); err != nil {
		artworkLog.Warn("caching artwork failed", "key", key, "err", err)
		return ""
	}
	if err := os.WriteFile(filepath.Join(m.ArtworkDir, name), img, 0o644); err != nil {
		artworkLog.Warn("caching artwork failed", "key", key, "err", err)
		return ""
	}
	ver := sha1.Sum(img)
	return "/artwork/" + name + "?v=" + hex.EncodeToString(ver[:4])
}
