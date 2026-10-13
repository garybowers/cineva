package library

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

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
	for _, root := range lib.Paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && audioExts[strings.ToLower(filepath.Ext(path))] {
				files = append(files, path)
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

	created := 0
	artistIDs := map[string]string{}              // artist name -> item id
	albumIDs := map[string]string{}               // album dir -> item id
	albumArt := map[string]bool{}                 // album dirs whose artwork is done
	mbData := map[string]*metadata.MusicRelease{} // album dir -> MB release (nil = looked up, no match)

	for i, path := range todo {
		if err := ctx.Err(); err != nil {
			return len(files), created, err
		}
		report(skipped+i, created, len(files))
		info, err := media.ProbeAudio(path)
		if err != nil {
			log.Printf("probe audio %s: %v", path, err)
			continue
		}
		albumDir := filepath.Dir(path)
		// Multi-disc layout: Artist/Album/CD1/track — the disc folder is
		// part of the album, not an album itself.
		discFromDir := 0
		if md := discDirRe.FindStringSubmatch(filepath.Base(albumDir)); md != nil {
			discFromDir, _ = strconv.Atoi(md[1])
			albumDir = filepath.Dir(albumDir)
		}
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
			}
			album.Poster = m.albumArtwork(ctx, path, albumDir, albumArt)
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
					log.Printf("musicbrainz: matched %q / %q -> %s", artistName, albumName, rel.MBID)
				} else {
					mbData[albumDir] = nil
				}
			}
			saved, err := m.Store.UpsertItem(ctx, album)
			if err != nil {
				return len(files), created, err
			}
			plan.keep[saved.ID] = true
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
		// Tracks inherit their album's cover for player/list display.
		if album, err := m.Store.Item(ctx, albumID); err == nil && album.Poster != "" && saved.Poster != album.Poster {
			saved.Poster = album.Poster
			if _, err := m.Store.UpsertItem(ctx, saved); err != nil {
				log.Printf("track poster %s: %v", path, err)
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
		log.Printf("cache artwork %s: %v", key, err)
		return ""
	}
	return "/artwork/" + name
}

// albumArtwork caches the album's cover image (embedded art from a track, or
// a cover/folder image file in the album dir) and returns its serve path.
func (m *Manager) albumArtwork(ctx context.Context, trackPath, albumDir string, done map[string]bool) string {
	if m.ArtworkDir == "" {
		return ""
	}
	if done[albumDir] {
		sum := sha1.Sum([]byte(albumDir))
		return "/artwork/album-" + hex.EncodeToString(sum[:8]) + ".jpg"
	}

	var img []byte
	for _, candidate := range []string{"cover.jpg", "cover.png", "folder.jpg", "front.jpg"} {
		if data, err := os.ReadFile(filepath.Join(albumDir, candidate)); err == nil {
			img = data
			break
		}
	}
	if img == nil {
		img = media.ExtractCoverArt(trackPath)
	}
	p := m.cacheArtwork("album", albumDir, img)
	if p != "" {
		done[albumDir] = true
	}
	return p
}
