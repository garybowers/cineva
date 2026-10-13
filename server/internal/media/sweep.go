package media

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// DefaultTranscodeMaxBytes caps the segment cache. The transcode directory
// usually lives under /tmp, which on a systemd host is tmpfs — i.e. RAM —
// so an unbounded cache does not merely waste disk, it evicts page cache and
// can exhaust memory. 8 GiB is roughly two 4K films' worth of segments.
const DefaultTranscodeMaxBytes int64 = 8 << 30

// dirUsage is one session directory's size and last use.
type dirUsage struct {
	path  string
	bytes int64
	mod   time.Time
}

// SweepOrphans removes session directories left behind by a previous run.
// Sessions die with the process — their segments do not — so without this
// every crash, restart or redeploy leaks a directory that nothing will ever
// clean up.
//
// Called at startup after restoring sessions, so every directory under Root
// except those named in keep is by definition an orphan. It returns the number of directories removed and the
// bytes reclaimed. Errors removing one directory are logged and skipped
// rather than returned: a leftover directory must never stop the server
// starting.
func SweepOrphans(root string, keep ...string) (int, int64, error) {
	spare := make(map[string]bool, len(keep))
	for _, d := range keep {
		spare[filepath.Base(d)] = true
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("sweep transcode dir: %w", err)
	}
	var n int
	var freed int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Sibling caches that are not session directories and must survive.
		switch e.Name() {
		case "subtitles", "dlcache", "trickplay":
			continue
		}
		// A session restored from the previous run owns its directory; its
		// segments are the whole reason the session was worth keeping.
		if spare[e.Name()] {
			continue
		}
		p := filepath.Join(root, e.Name())
		size := dirSize(p)
		if err := os.RemoveAll(p); err != nil {
			logger.Warn("could not remove orphaned session directory", "path", p, "err", err)
			continue
		}
		n++
		freed += size
	}
	return n, freed, nil
}

// enforceCap deletes whole session directories, least recently modified
// first, until the cache is back under maxBytes. Directories belonging to
// live sessions are passed in as keep and are never considered: evicting the
// segments of something being watched would break that playback.
func enforceCap(root string, maxBytes int64, keep map[string]bool) (int, int64) {
	if maxBytes <= 0 {
		return 0, 0
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0
	}
	var total int64
	var cand []dirUsage
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		switch e.Name() {
		case "subtitles", "dlcache", "trickplay":
			continue
		}
		p := filepath.Join(root, e.Name())
		size := dirSize(p)
		total += size
		if keep[e.Name()] {
			continue // live session: counts towards the total, never evicted
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		cand = append(cand, dirUsage{path: p, bytes: size, mod: info.ModTime()})
	}
	if total <= maxBytes {
		return 0, 0
	}
	// Oldest first: the least recently touched session is the one least
	// likely to be resumed.
	sort.Slice(cand, func(i, j int) bool { return cand[i].mod.Before(cand[j].mod) })
	var n int
	var freed int64
	for _, c := range cand {
		if total-freed <= maxBytes {
			break
		}
		if err := os.RemoveAll(c.path); err != nil {
			logger.Warn("could not evict session directory", "path", c.path, "err", err)
			continue
		}
		n++
		freed += c.bytes
	}
	return n, freed
}

// dirSize totals the regular files under a directory. Unreadable entries
// count as zero: this drives cleanup, where under-counting merely means
// cleaning less eagerly.
func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable entry is counted as zero, by design
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
