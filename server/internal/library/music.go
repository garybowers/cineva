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
	"github.com/garybowers/cineva/server/internal/store"
)

var audioExts = map[string]bool{
	".flac": true, ".mp3": true, ".m4a": true, ".aac": true,
	".ogg": true, ".opus": true, ".wav": true, ".wma": true,
}

// "1995 - Album Name" directory pattern.
var albumDirRe = regexp.MustCompile(`^(\d{4})\s*-\s*(.+)$`)

// "03 - Title.flac" filename pattern.
var trackFileRe = regexp.MustCompile(`^(\d{1,3})\s*-\s*(.+)$`)

// scanMusic builds artist -> album -> track from tags first, directory
// layout (Artist/Year - Album/NN - Title.ext) as fallback.
func (m *Manager) scanMusic(ctx context.Context, lib store.Library) (int, int, error) {
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

	created := 0
	artistIDs := map[string]string{} // artist name -> item id
	albumIDs := map[string]string{}  // album dir -> item id
	albumArt := map[string]bool{}    // album dirs whose artwork is done

	for _, path := range files {
		info, err := media.ProbeAudio(path)
		if err != nil {
			log.Printf("probe audio %s: %v", path, err)
			continue
		}
		albumDir := filepath.Dir(path)
		artistDir := filepath.Dir(albumDir)

		artistName := info.AlbumArtist
		if artistName == "" {
			artistName = info.Artist
		}
		if artistName == "" {
			artistName = filepath.Base(artistDir)
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
			saved, err := m.Store.UpsertItem(ctx, album)
			if err != nil {
				return len(files), created, err
			}
			albumID, albumIDs[albumDir] = saved.ID, saved.ID
			created++
		}

		displayArtist := info.Artist
		if displayArtist == "" {
			displayArtist = artistName
		}
		track := store.Item{
			LibraryID: lib.ID, Kind: store.KindTrack, ParentID: albumID,
			Title: title, Artist: displayArtist, Year: albumYear,
			TrackNum: trackNum, DiscNum: info.DiscNum, Path: path,
			RuntimeSecs: info.RuntimeSecs, Container: info.Container,
			AudioCodec: info.Codec,
		}
		if st, err := os.Stat(path); err == nil {
			track.SizeBytes = st.Size()
		}
		saved, err := m.Store.UpsertItem(ctx, track)
		if err != nil {
			return len(files), created, err
		}
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
