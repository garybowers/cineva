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
	"sync"

	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

var musicLog = logging.For("music")

var audioExts = map[string]bool{
	".flac": true, ".mp3": true, ".m4a": true, ".aac": true,
	".ogg": true, ".opus": true, ".wav": true, ".wma": true,
}

// "1995 - Album Name" directory pattern.
var albumDirRe = regexp.MustCompile(`^(\d{4})\s*-\s*(.+)$`)

// "CD1", "Disc 2", "disk3", "CD 1 - Live", "Disc One": a disc folder
// inside a multi-disc album. The number may be followed by a title.
var discDirRe = regexp.MustCompile(`(?i)^(?:cd|disc|disk)\s*(\d{1,2}|one|two|three|four|five|six|seven|eight|nine|ten)(?:\s*[-:–.]\s*.*|\s+.*)?$`)

// "03 - Title.flac" filename pattern.
var trackFileRe = regexp.MustCompile(`^(\d{1,3})\s*-\s*(.+)$`)

// discWords are the spelled-out disc numbers a folder name may use.
var discWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
}

// discNumberOf is the disc number a folder name gives, or 0 when the folder
// is not a disc folder: "CD1", "Disc 2", "disk3", "CD 1 - Live", "Disc One".
func discNumberOf(name string) int {
	md := discDirRe.FindStringSubmatch(name)
	if md == nil {
		return 0
	}
	if n, err := strconv.Atoi(md[1]); err == nil {
		return n
	}
	return discWords[strings.ToLower(md[1])]
}

// audioFile is one changed file of an album, probed.
type audioFile struct {
	path string
	info media.AudioInfo
	// disc is the disc number the file's folder gives, 0 outside a disc
	// folder.
	disc int
}

// albumGroup is the changed files of one album folder, written together.
type albumGroup struct {
	dir   string
	files []audioFile
}

// probedAudio is one file's probe result, in the batch's order.
type probedAudio struct {
	path string
	info media.AudioInfo
	err  error
}

// probeAudioBatch reads a batch of files' tags in parallel (see probeBatch,
// which does the same for video), in the input's order.
func (m *Manager) probeAudioBatch(ctx context.Context, probe func(string) (media.AudioInfo, error), paths []string) []probedAudio {
	out := make([]probedAudio, len(paths))
	sem := make(chan struct{}, probeWorkers())
	var wg sync.WaitGroup
	for i, p := range paths {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				out[i] = probedAudio{path: p, err: ctx.Err()}
				return
			}
			r := probedAudio{path: p}
			r.info, r.err = probe(p)
			out[i] = r
		}(i, p)
	}
	wg.Wait()
	return out
}

// scanMusic builds artist -> album -> track from tags first, directory
// layout (Artist/Year - Album/NN - Title.ext) as fallback.
//
// Changed files are probed in parallel batches and written an album at a
// time, each album in one transaction (store.SaveAlbum), with the
// compilation decision made over the whole album rather than its first
// track. Nothing here waits on MusicBrainz: albums missing a cover, a year
// or an id are looked up afterwards in the background (enrichMusic), so a
// first scan of a large untagged library finishes in minutes and fills in
// over the following hours.
func (m *Manager) scanMusic(ctx context.Context, lib store.Library, plan *scanPlan, report reportFunc) (int, int, error) {
	var files []string
	// The cover images the walk passes, by album folder (a disc folder's
	// filed under its album): what albumArtwork reads, and what an album's
	// sidecar fingerprint is taken over.
	covers := map[string][]fs.DirEntry{}
	// How many files each album folder holds, changed or not, so a partly
	// rescanned album knows it is partly rescanned.
	albumFiles := map[string]int{}
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
				dir, _ := albumDirOf(path)
				albumFiles[dir]++
			} else if coverRank(d.Name()) >= 0 {
				// A cover in a disc folder belongs to the album above it.
				dir := filepath.Dir(path)
				if albumDir, disc := albumDirOf(path); albumDir != dir {
					covers[albumDir] = append(covers[albumDir],
						discCover{DirEntry: d, name: filepath.Join(filepath.Base(dir), d.Name()), disc: disc})
				} else {
					covers[dir] = append(covers[dir], d)
				}
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
	artistIDs := map[string]string{} // artist name -> item id
	// untitled is, per saved album, the tracks whose files carry no title
	// tag, by track id: the ones the MusicBrainz pass may name.
	untitled := map[string]map[string]discPos{}
	probe := m.ProbeAudio
	if probe == nil {
		probe = media.ProbeAudio
	}

	flush := func(g *albumGroup) error {
		n, err := m.saveAlbumGroup(ctx, lib, plan, g, covers, art, artistIDs, albumFiles[g.dir], untitled)
		created += n
		return err
	}

	var batch []probedAudio
	var group *albumGroup
	for i, path := range todo {
		if err := ctx.Err(); err != nil {
			return len(files), created, err
		}
		if i%probeBatchSize == 0 {
			batch = m.probeAudioBatch(ctx, probe, todo[i:min(i+probeBatchSize, len(todo))])
		}
		pr := batch[i%probeBatchSize]
		report(skipped+i, created, len(files))
		if pr.err != nil {
			musicLog.Warn("audio probe failed", "path", path, "err", pr.err)
			continue
		}
		albumDir, disc := albumDirOf(path)
		if group != nil && group.dir != albumDir {
			if err := flush(group); err != nil {
				return len(files), created, err
			}
			group = nil
		}
		if group == nil {
			group = &albumGroup{dir: albumDir}
		}
		group.files = append(group.files, audioFile{path: path, info: pr.info, disc: disc})
	}
	if group != nil {
		if err := flush(group); err != nil {
			return len(files), created, err
		}
	}
	m.enrichMusic(lib, untitled)
	return len(files), created, nil
}

// primaryArtist is the first artist of a credit: "A feat. B", "A & B" and
// "A, B" all credit A first, and one guest on one track does not make an
// album a compilation.
func primaryArtist(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, joiner := range []string{" feat", " ft.", " ft ", " & ", ", ", " / ", " x ", " with "} {
		if i := strings.Index(n, joiner); i > 0 {
			n = n[:i]
		}
	}
	return strings.TrimSpace(n)
}

// saveAlbumGroup works out an album's artist, title and year from its
// changed files, writes the album and those tracks in one transaction and
// returns how many items that made.
func (m *Manager) saveAlbumGroup(ctx context.Context, lib store.Library, plan *scanPlan, g *albumGroup,
	covers map[string][]fs.DirEntry, art map[string]string, artistIDs map[string]string,
	filesInAlbum int, untitled map[string]map[string]discPos) (int, error) {
	albumDir := g.dir
	artistDir := filepath.Dir(albumDir)
	first := g.files[0].info

	// The album artist: the tag, else the one artist the tracks share,
	// else "Various Artists", else the folder.
	var albumArtistTag, mbArtist string
	compilation := false
	primaries := map[string]bool{}
	var firstArtist string
	for _, f := range g.files {
		if albumArtistTag == "" && f.info.AlbumArtist != "" {
			albumArtistTag = f.info.AlbumArtist
		}
		if mbArtist == "" {
			mbArtist = f.info.MBArtistID
		}
		if f.info.Compilation {
			compilation = true
		}
		if f.info.Artist != "" {
			if firstArtist == "" {
				firstArtist = f.info.Artist
			}
			primaries[primaryArtist(f.info.Artist)] = true
		}
	}
	// A partly rescanned album: the unchanged tracks' artists count too,
	// or an untagged compilation would move under whichever artist's
	// track happened to change.
	if st, ok := plan.stamps[albumDir]; ok && st.Kind == store.KindAlbum && len(g.files) < filesInAlbum {
		if names, err := m.Store.TrackArtists(ctx, st.ID); err == nil {
			for _, n := range names {
				primaries[primaryArtist(n)] = true
			}
		}
	}
	artistName := albumArtistTag
	if artistName == "" {
		if len(primaries) > 1 {
			compilation = true
		} else {
			artistName = firstArtist
		}
	}
	if compilation && (artistName == "" || strings.HasPrefix(strings.ToLower(artistName), "various")) {
		artistName = "Various Artists"
	}
	if strings.HasPrefix(strings.ToLower(artistName), "various") {
		compilation = true
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

	albumName := first.Album
	albumYear := first.Year
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

	// Artist: keyed by name; path is synthetic (an artist may span dirs).
	created := 0
	artistID, ok := artistIDs[artistName]
	if !ok {
		artist := store.Item{
			LibraryID: lib.ID, Kind: store.KindArtist,
			Title: artistName, Artist: artistName,
			Path: "artist://" + strings.ToLower(artistName),
		}
		if !compilation {
			artist.MBID = mbArtist
		}
		saved, err := m.Store.UpsertItem(ctx, artist)
		if err != nil {
			return 0, err
		}
		plan.keep[saved.ID] = true
		artistID, artistIDs[artistName] = saved.ID, saved.ID
		created++
	}

	album := store.Item{
		LibraryID: lib.ID, Kind: store.KindAlbum, ParentID: artistID,
		Title: albumName, Artist: artistName, Year: albumYear, Path: albumDir,
		Compilation: compilation, SidecarStamp: fingerprint(covers[albumDir]),
	}
	for _, f := range g.files {
		if f.info.MBReleaseID != "" {
			album.MBID = f.info.MBReleaseID
			break
		}
	}
	album.Poster = m.albumArtwork(g.files[0].path, albumDir, covers[albumDir], art)

	tracks := make([]store.TrackSave, 0, len(g.files))
	var noTitle []int // indexes into tracks of files without a title tag
	for _, f := range g.files {
		info := f.info
		title := info.Title
		trackNum := info.TrackNum
		base := strings.TrimSuffix(filepath.Base(f.path), filepath.Ext(f.path))
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
		displayArtist := info.Artist
		if displayArtist == "" {
			displayArtist = artistName
		}
		discNum := info.DiscNum
		if discNum == 0 {
			discNum = f.disc
		}
		track := store.Item{
			LibraryID: lib.ID, Kind: store.KindTrack,
			Title: title, Artist: displayArtist, Year: albumYear,
			TrackNum: trackNum, DiscNum: discNum, Path: f.path,
			RuntimeSecs: info.RuntimeSecs, Container: info.Container,
			AudioCodec: info.Codec, Bitrate: info.Bitrate, SampleRate: info.SampleRate,
			BitDepth: info.BitDepth, Channels: info.Channels, MBID: info.MBRecordingID,
		}
		plan.stamp(&track, f.path)
		if info.Title == "" && trackNum > 0 {
			noTitle = append(noTitle, len(tracks))
		}
		tracks = append(tracks, store.TrackSave{
			Item: track, Genres: splitGenres(info.Genre), Lyrics: readLyrics(f.path, info.Lyrics),
		})
	}

	saved, savedTracks, err := m.Store.SaveAlbum(ctx, album, tracks)
	if err != nil {
		return created, err
	}
	plan.keep[saved.ID] = true
	if _, existed := plan.stamps[albumDir]; !existed {
		created++
	}
	plan.noteNew(albumDir, saved)
	for _, t := range savedTracks {
		plan.keep[t.ID] = true
		if _, existed := plan.stamps[t.Path]; !existed {
			created++
		}
	}
	for _, i := range noTitle {
		if i >= len(savedTracks) {
			continue
		}
		t := savedTracks[i]
		if untitled[saved.ID] == nil {
			untitled[saved.ID] = map[string]discPos{}
		}
		untitled[saved.ID][t.ID] = discPos{disc: max(t.DiscNum, 1), pos: t.TrackNum}
	}
	return created, nil
}

// lrcTimed matches a timed lyrics line, "[01:23.45] words".
var lrcTimed = regexp.MustCompile(`(?m)^\[\d{1,3}:\d{2}`)

// lyricsMax is the most lyrics read from a file beside a track; a bigger
// one is not lyrics.
const lyricsMax = 256 << 10

// readLyrics returns a track's lyrics: an .lrc file beside it first (the
// timed form people add by hand), else the tag. Nil when it has none.
func readLyrics(trackPath, tag string) *store.Lyrics {
	text := tag
	lrc := strings.TrimSuffix(trackPath, filepath.Ext(trackPath)) + ".lrc"
	if fi, err := os.Stat(lrc); err == nil && fi.Size() > 0 && fi.Size() <= lyricsMax {
		if data, err := os.ReadFile(lrc); err == nil {
			text = string(data)
		}
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return nil
	}
	return &store.Lyrics{Synced: lrcTimed.MatchString(text), Text: text}
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
	if disc := discNumberOf(filepath.Base(dir)); disc > 0 {
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

// discCover is a cover file found in a disc folder (Album/CD1/cover.jpg),
// listed with the album folder's own covers. Its name is relative to the
// album folder ("CD1/cover.jpg"), which is what albumArtwork reads and what
// the album's fingerprint is taken over; the album folder's own covers keep
// their bare names, so an album without disc folders fingerprints as it
// always did.
type discCover struct {
	fs.DirEntry
	name string
	disc int
}

func (c discCover) Name() string { return c.name }

// coverOrder ranks an album's cover files: the album folder's own first,
// then each disc folder's, first disc first, and within a folder by
// coverNames.
func coverOrder(a, b fs.DirEntry) int {
	folder := func(d fs.DirEntry) int {
		if c, ok := d.(discCover); ok {
			return 1 + c.disc
		}
		return 0
	}
	if fa, fb := folder(a), folder(b); fa != fb {
		return fa - fb
	}
	return coverRank(filepath.Base(a.Name())) - coverRank(filepath.Base(b.Name()))
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
// folder, else one in its disc folders (first disc first), else art
// embedded in the track — and returns its serve path.
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
	slices.SortStableFunc(ranked, coverOrder)
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
