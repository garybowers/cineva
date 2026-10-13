package library

import (
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/garybowers/cineva/server/internal/store"
)

// scanPlan is what makes a rescan incremental: what the library already
// holds, which files on disk differ from it, and which items the scan has
// kept so far.
//
// A file whose size and modification time both match its stored item is
// skipped outright — no probe, no metadata lookup, no write. That is the
// common case by a wide margin, and doing the full work for it is what made
// every rescan as slow as the first scan. A full scan (the admin's "Full
// rescan", `cineva scan --full`) ignores the stamps and redoes everything,
// which is the way to pick up new provider data or recover from a bad
// match.
type scanPlan struct {
	full   bool
	stamps map[string]store.FileStamp // by path
	byID   map[string]store.FileStamp
	// stat holds size, mtime and sidecar fingerprint for the files that are
	// being rescanned, so the scanner can store them with the item.
	stat map[string]fileStat
	// keep is every item id this scan saved or found unchanged. Anything
	// else in the library afterwards has lost its file and is pruned.
	keep map[string]bool

	// sidecars, when set, is what the directory walk found beside the
	// videos, so a changed .nfo, poster or subtitle counts as a change to
	// its video. Nil for libraries whose scanner reads no sidecars.
	sidecars *sidecarIndex
	// looked is every film and series this scan already asked the provider
	// about, which the retry of unmatched titles then leaves alone.
	looked map[string]bool
	// adopt holds fingerprints for unchanged items that had none recorded
	// (every item, on the first scan after migration 0032), written once
	// the walk is done rather than treated as a change.
	adopt map[string]int64
}

// fileStat is what the change test reads from disk for one video.
type fileStat struct {
	size, mtime, sidecars int64
}

func (m *Manager) newScanPlan(ctx context.Context, lib store.Library, full bool) (*scanPlan, error) {
	stamps, err := m.Store.FileStamps(ctx, lib.ID)
	if err != nil {
		return nil, fmt.Errorf("scan plan: %w", err)
	}
	p := &scanPlan{
		full:   full,
		stamps: stamps,
		byID:   make(map[string]store.FileStamp, len(stamps)),
		stat:   map[string]fileStat{},
		keep:   map[string]bool{},
		adopt:  map[string]int64{},
		looked: map[string]bool{},
	}
	for _, st := range stamps {
		p.byID[st.ID] = st
	}
	return p, nil
}

// changed returns the files that need scanning, in their original order,
// and marks every unchanged one kept along with the items above it.
//
// A stored runtime of zero means the file was never read successfully, so
// it is retried rather than trusted; a stored mtime of zero means it was
// last scanned before mtimes were recorded. A video whose sidecars differ
// from the fingerprint stored with it is rescanned like a changed video;
// one with no fingerprint stored adopts the current one instead.
func (p *scanPlan) changed(files []string) []string {
	var todo []string
	for _, path := range files {
		var cur fileStat
		if fi, err := os.Stat(path); err == nil {
			cur.size, cur.mtime = fi.Size(), fi.ModTime().Unix()
		}
		if p.sidecars != nil {
			cur.sidecars = p.sidecars.stamp(path)
		}
		st, ok := p.stamps[path]
		same := ok && st.FileMtime != 0 && st.FileMtime == cur.mtime &&
			st.SizeBytes == cur.size && st.RuntimeSecs > 0
		if same && cur.sidecars != 0 && st.SidecarStamp != 0 && st.SidecarStamp != cur.sidecars {
			same = false
		}
		if !p.full && same {
			p.keepWithParents(st.ID)
			if cur.sidecars != 0 && st.SidecarStamp == 0 {
				p.adopt[st.ID] = cur.sidecars
			}
			continue
		}
		p.stat[path] = cur
		todo = append(todo, path)
	}
	return todo
}

// stamp sets the file's size, mtime and sidecar fingerprint on an item
// about to be saved.
func (p *scanPlan) stamp(item *store.Item, path string) {
	if s, ok := p.stat[path]; ok {
		item.SizeBytes, item.FileMtime, item.SidecarStamp = s.size, s.mtime, s.sidecars
	}
}

// keepWithParents keeps an item and its chain of parents: an unchanged
// episode needs its season and series to survive the prune as well.
func (p *scanPlan) keepWithParents(id string) {
	for id != "" && !p.keep[id] {
		p.keep[id] = true
		id = p.byID[id].ParentID
	}
}

// sidecarExts are the extensions of the files a video scan reads beside a
// video: Kodi-style .nfo, local artwork and external subtitles (see
// ReadNFO, localArtwork and media.SidecarSubtitles).
var sidecarExts = map[string]bool{
	".nfo": true, ".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
	".srt": true, ".vtt": true,
}

// subtitleExts are the sidecars an episode's scan reads; the TV scanner
// takes no .nfo or local artwork, so a Sonarr-written thumbnail changing
// must not reread the episode.
var subtitleExts = map[string]bool{".srt": true, ".vtt": true}

// folderSidecars are the names that belong to every film in their folder
// rather than to one file (lower-cased, extension removed).
var folderSidecars = map[string]bool{
	"movie": true, "poster": true, "folder": true, "cover": true,
	"fanart": true, "backdrop": true,
}

// sidecarIndex is the sidecar files the directory walk passed, by folder.
// Entries are kept as the walk's own DirEntry values, so a sidecar costs a
// stat only when a video in the same folder claims it — never a second
// directory read, and nothing at all for the thumbnails and artwork no
// scanner reads.
type sidecarIndex struct {
	// movies widens the claim to the .nfo and artwork the film scanner
	// reads; otherwise only subtitles count.
	movies bool
	byDir  map[string][]fs.DirEntry
}

func newSidecarIndex(movies bool) *sidecarIndex {
	return &sidecarIndex{movies: movies, byDir: map[string][]fs.DirEntry{}}
}

// add records a file the walk passed, if it could be a sidecar.
func (x *sidecarIndex) add(path string, d fs.DirEntry) {
	if x == nil {
		return
	}
	ext := strings.ToLower(filepath.Ext(d.Name()))
	if !sidecarExts[ext] || (!x.movies && !subtitleExts[ext]) {
		return
	}
	dir := filepath.Dir(path)
	x.byDir[dir] = append(x.byDir[dir], d)
}

// claims reports whether a file in a video's folder is one of its
// sidecars: named for the video ("Film.nfo", "Film.en.srt",
// "Film-poster.jpg"), or for a film the folder's generic names
// ("movie.nfo", "poster.jpg", "fanart.png").
func (x *sidecarIndex) claims(videoStem, name string) bool {
	ext := filepath.Ext(name)
	stem := name[:len(name)-len(ext)]
	if stem == videoStem || strings.HasPrefix(stem, videoStem+".") ||
		(x.movies && strings.HasPrefix(stem, videoStem+"-")) {
		return true
	}
	return x.movies && folderSidecars[strings.ToLower(stem)]
}

// stamp fingerprints a video's sidecars: a hash of each one's name, size
// and modification time, so an edit, an addition and a removal all change
// it. Never 0, which the store reads as "not recorded".
func (x *sidecarIndex) stamp(videoPath string) int64 {
	base := filepath.Base(videoPath)
	videoStem := strings.TrimSuffix(base, filepath.Ext(base))
	var claimed []fs.DirEntry
	for _, d := range x.byDir[filepath.Dir(videoPath)] {
		if x.claims(videoStem, d.Name()) {
			claimed = append(claimed, d)
		}
	}
	return fingerprint(claimed)
}

// fingerprint hashes files' names, sizes and modification times, in name
// order: the same files unchanged give the same value, and an edit, an
// addition or a removal a different one. Never 0, which the store reads as
// "not recorded" — an empty set has a fingerprint too.
func fingerprint(entries []fs.DirEntry) int64 {
	var parts []string
	for _, d := range entries {
		fi, err := d.Info()
		if err != nil {
			continue // gone since the walk listed it
		}
		parts = append(parts, d.Name()+"\x00"+strconv.FormatInt(fi.Size(), 10)+"\x00"+
			strconv.FormatInt(fi.ModTime().UnixNano(), 10))
	}
	sort.Strings(parts)
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0xff})
	}
	if v := int64(h.Sum64()); v != 0 {
		return v
	}
	return 1
}
