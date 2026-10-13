package library

import (
	"context"
	"fmt"
	"os"

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
	// stat holds size and mtime for the files that are being rescanned, so
	// the scanner can store them with the item.
	stat map[string][2]int64
	// keep is every item id this scan saved or found unchanged. Anything
	// else in the library afterwards has lost its file and is pruned.
	keep map[string]bool
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
		stat:   map[string][2]int64{},
		keep:   map[string]bool{},
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
// last scanned before mtimes were recorded.
func (p *scanPlan) changed(files []string) []string {
	var todo []string
	for _, path := range files {
		var size, mtime int64
		if fi, err := os.Stat(path); err == nil {
			size, mtime = fi.Size(), fi.ModTime().Unix()
		}
		st, ok := p.stamps[path]
		if !p.full && ok && st.FileMtime != 0 && st.FileMtime == mtime &&
			st.SizeBytes == size && st.RuntimeSecs > 0 {
			p.keepWithParents(st.ID)
			continue
		}
		p.stat[path] = [2]int64{size, mtime}
		todo = append(todo, path)
	}
	return todo
}

// stamp sets the file's size and mtime on an item about to be saved.
func (p *scanPlan) stamp(item *store.Item, path string) {
	if s, ok := p.stat[path]; ok {
		item.SizeBytes, item.FileMtime = s[0], s[1]
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
