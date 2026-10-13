package library

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/garybowers/cineva/server/internal/store"
)

// NFO is the Kodi movie.nfo format, which Jellyfin, Radarr and Sonarr all
// read and write. Supporting it is the interop layer that lets someone
// migrating from one of those keep their existing matches instead of
// re-matching a whole library — which, given how often scene-release names
// mismatch, is the difference between a migration and a re-curation.
//
// Only the fields Cineva stores are decoded; the format carries a great deal
// more (fileinfo, streamdetails, resume points) that belongs to the tool
// that wrote it.
type NFO struct {
	XMLName xml.Name `xml:"movie"`
	Title   string   `xml:"title"`
	// OriginalTitle is the film's native-language title; preferred only
	// when Title is absent.
	OriginalTitle string   `xml:"originaltitle"`
	SortTitle     string   `xml:"sorttitle"`
	Year          int      `xml:"year"`
	Premiered     string   `xml:"premiered"` // YYYY-MM-DD, where year is absent
	Plot          string   `xml:"plot"`
	Outline       string   `xml:"outline"`
	Tagline       string   `xml:"tagline"`
	Runtime       int      `xml:"runtime"` // minutes
	Rating        float64  `xml:"rating"`
	Genres        []string `xml:"genre"`
	MPAA          string   `xml:"mpaa"` // certification, e.g. "Rated R"
	// Ids come in two shapes: <tmdbid>, or <uniqueid type="tmdb">.
	TmdbID    int    `xml:"tmdbid"`
	ImdbID    string `xml:"imdbid"`
	ID        string `xml:"id"` // legacy: usually the IMDb id
	UniqueIDs []struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"uniqueid"`
}

// ResolvedIDs folds the two id conventions into one answer.
func (n *NFO) ResolvedIDs() (tmdbID int, imdbID string) {
	tmdbID, imdbID = n.TmdbID, n.ImdbID
	for _, u := range n.UniqueIDs {
		v := strings.TrimSpace(u.Value)
		switch strings.ToLower(u.Type) {
		case "tmdb":
			if tmdbID == 0 {
				tmdbID, _ = strconv.Atoi(v)
			}
		case "imdb":
			if imdbID == "" {
				imdbID = v
			}
		}
	}
	// <id> is the old single-id field; it holds an IMDb id in practice.
	if imdbID == "" && strings.HasPrefix(n.ID, "tt") {
		imdbID = n.ID
	}
	return tmdbID, imdbID
}

// ReleaseYear prefers <year>, falling back to the year in <premiered>.
func (n *NFO) ReleaseYear() int {
	if n.Year > 0 {
		return n.Year
	}
	if len(n.Premiered) >= 4 {
		if y, err := strconv.Atoi(n.Premiered[:4]); err == nil {
			return y
		}
	}
	return 0
}

// Synopsis prefers the full plot over the short outline.
func (n *NFO) Synopsis() string {
	if n.Plot != "" {
		return n.Plot
	}
	return n.Outline
}

// nfoCandidates lists where a sidecar for this file may live, in priority
// order: alongside the file with the same stem, then the Kodi convention of
// a movie.nfo in the film's own folder.
func nfoCandidates(videoPath string) []string {
	dir := filepath.Dir(videoPath)
	stem := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	return []string{
		filepath.Join(dir, stem+".nfo"),
		filepath.Join(dir, "movie.nfo"),
	}
}

// ReadNFO loads the sidecar for a video file, if there is one. A missing
// sidecar is not an error — most libraries have none. A malformed one is,
// so the caller can say so rather than silently ignoring the operator's own
// metadata.
func ReadNFO(videoPath string) (*NFO, bool, error) {
	for _, p := range nfoCandidates(videoPath) {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var n NFO
		if err := xml.Unmarshal(data, &n); err != nil {
			return nil, false, fmt.Errorf("parse %s: %w", p, err)
		}
		return &n, true, nil
	}
	return nil, false, nil
}

// localArtwork finds artwork the library carries itself, following the
// Kodi/Jellyfin naming conventions. Returns a path relative to nothing —
// the absolute path on disk — or "" when there is none.
//
// Checked in the order these tools prefer: a file named for the video, then
// the generic name in the film's folder.
func localArtwork(videoPath string, names []string) string {
	dir := filepath.Dir(videoPath)
	stem := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	exts := []string{".jpg", ".jpeg", ".png", ".webp"}
	var candidates []string
	for _, n := range names {
		for _, e := range exts {
			candidates = append(candidates,
				filepath.Join(dir, stem+"-"+n+e),
				filepath.Join(dir, n+e),
			)
		}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() && st.Size() > 0 {
			return c
		}
	}
	return ""
}

// LocalPoster and LocalBackdrop name the conventional files for each.
// "folder" and "cover" are the Jellyfin aliases for a poster; "fanart" is
// the near-universal name for the wide backdrop.
func LocalPoster(videoPath string) string {
	return localArtwork(videoPath, []string{"poster", "folder", "cover"})
}

func LocalBackdrop(videoPath string) string {
	return localArtwork(videoPath, []string{"fanart", "backdrop"})
}

// copyLocalArtwork copies artwork the library carries itself into the
// artwork directory, so it is served from /artwork/ like anything fetched
// from a provider and the client needs no second code path.
//
// The destination name is derived from the source path so two films with a
// poster.jpg each do not collide. Already-copied files are left alone, which
// makes a rescan cheap.
func copyLocalArtwork(src, artworkDir, name string) (string, error) {
	filename := name + filepath.Ext(src)
	dest := filepath.Join(artworkDir, filename)
	srcInfo, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	// Re-copy when the source has changed size or been modified since the
	// copy was made, so replacing a poster on disk is picked up by the next
	// scan even when the new one happens to be the same size.
	if d, err := os.Stat(dest); err == nil && d.Size() == srcInfo.Size() && !d.ModTime().Before(srcInfo.ModTime()) {
		return filename, nil
	}
	if err := os.MkdirAll(artworkDir, 0o755); err != nil {
		return "", fmt.Errorf("artwork dir: %w", err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("read local artwork: %w", err)
	}
	tmp := dest + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", fmt.Errorf("write local artwork: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("write local artwork: %w", err)
	}
	return filename, nil
}

// artworkKey turns a file path into a short, stable, filesystem-safe name.
func artworkKey(path string) string {
	sum := sha1.Sum([]byte(path))
	return "local-" + hex.EncodeToString(sum[:8])
}

// applyNFO overlays a sidecar's fields onto an item, after any provider
// result. Only non-empty fields overwrite: a sparse sidecar (just a tmdb id,
// which is the common Radarr output) must not blank out everything TMDB
// supplied.
func applyNFO(item *store.Item, n *NFO) {
	if t := strings.TrimSpace(n.Title); t != "" {
		item.Title = t
	}
	if y := n.ReleaseYear(); y > 0 {
		item.Year = y
	}
	if p := strings.TrimSpace(n.Synopsis()); p != "" {
		item.Overview = p
	}
	if tl := strings.TrimSpace(n.Tagline); tl != "" {
		item.Tagline = tl
	}
	if n.Rating > 0 {
		item.Rating = n.Rating
	}
	if len(n.Genres) > 0 {
		item.Genres = n.Genres
	}
	if id, _ := n.ResolvedIDs(); id > 0 {
		item.TmdbID = id
	}
	if c := normaliseMPAA(n.MPAA); c != "" {
		item.Certification = c
	}
	if n.Runtime > 0 && item.RuntimeSecs == 0 {
		// Only as a fallback: ffprobe's duration is measured, the sidecar's
		// is whatever the publisher claimed.
		item.RuntimeSecs = n.Runtime * 60
	}
}

// applyLocalArtwork points an item at poster/fanart files the library
// carries itself, copying them into the artwork directory. Local files win
// over the provider's: someone who put a poster.jpg there meant it.
func (m *Manager) applyLocalArtwork(item *store.Item, videoPath string) {
	if m.ArtworkDir == "" {
		return
	}
	if src := LocalPoster(videoPath); src != "" {
		if f, err := copyLocalArtwork(src, m.ArtworkDir, artworkKey(src)); err == nil {
			item.Poster = "/artwork/" + f
		} else {
			artworkLog.Warn("copying local poster failed", "path", src, "err", err)
		}
	}
	if src := LocalBackdrop(videoPath); src != "" {
		if f, err := copyLocalArtwork(src, m.ArtworkDir, artworkKey(src)); err == nil {
			item.Backdrop = "/artwork/" + f
		} else {
			artworkLog.Warn("copying local backdrop failed", "path", src, "err", err)
		}
	}
}

// normaliseMPAA strips the "Rated " prefix Kodi writes, so a sidecar's
// "Rated R" and TMDB's "R" end up as the same string rather than two
// variants of one certification.
func normaliseMPAA(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "Rated ")
	s = strings.TrimPrefix(s, "rated ")
	// "UK:15" and "US:PG-13" are both seen in the wild.
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}
