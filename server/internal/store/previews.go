package store

import (
	"context"
	"fmt"
	"strings"
)

// PreviewItem is a playable video as seek-bar preview generation sees it.
type PreviewItem struct {
	ID          string
	LibraryID   string
	Title       string
	Series      string // the series' title, for an episode
	SeasonNum   int
	EpisodeNum  int
	Path        string
	SizeBytes   int64
	RuntimeSecs int
	FileMtime   int64
}

type PreviewRepo interface {
	PreviewItems(ctx context.Context, libraryID string) ([]PreviewItem, error)
	PreviewItem(ctx context.Context, itemID string) (PreviewItem, error)
	ExistingItemIDs(ctx context.Context, ids []string) (map[string]bool, error)
}

var _ PreviewRepo = (*Store)(nil)

const previewCols = `i.id, i.library_id, i.title, COALESCE(sr.title, ''), i.season_num, i.episode_num,
	i.path, i.size_bytes, i.runtime_secs, i.file_mtime`

const previewFrom = ` FROM items i
	LEFT JOIN items sn ON i.kind = 'episode' AND sn.id = i.parent_id
	LEFT JOIN items sr ON sr.id = sn.parent_id`

func scanPreviewItem(sc interface{ Scan(...any) error }) (PreviewItem, error) {
	var p PreviewItem
	err := sc.Scan(&p.ID, &p.LibraryID, &p.Title, &p.Series, &p.SeasonNum, &p.EpisodeNum,
		&p.Path, &p.SizeBytes, &p.RuntimeSecs, &p.FileMtime)
	return p, err
}

// PreviewItems lists a library's films and episodes with a known running
// time — what seek-bar previews can be made for — newest additions first,
// so what was added last, and is likeliest to be played next, gets them
// first. Whether previews already exist is for the caller to check: they
// are files beside the database, not rows in it.
func (s *Store) PreviewItems(ctx context.Context, libraryID string) ([]PreviewItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+previewCols+previewFrom+`
		 WHERE i.library_id = ? AND i.kind IN ('movie', 'episode') AND i.runtime_secs > 0
		 ORDER BY i.added_at DESC, i.id`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("preview items: %w", err)
	}
	defer rows.Close()
	var out []PreviewItem
	for rows.Next() {
		p, err := scanPreviewItem(rows)
		if err != nil {
			return nil, fmt.Errorf("preview items: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("preview items: %w", err)
	}
	return out, nil
}

// PreviewItem is one item as PreviewItems describes it.
func (s *Store) PreviewItem(ctx context.Context, itemID string) (PreviewItem, error) {
	p, err := scanPreviewItem(s.db.QueryRowContext(ctx, `SELECT `+previewCols+previewFrom+` WHERE i.id = ?`, itemID))
	if err != nil {
		return PreviewItem{}, fmt.Errorf("preview item %s: %w", itemID, err)
	}
	return p, nil
}

// ExistingItemIDs reports which of ids are still items, so files kept beside
// the database under an item's id — seek-bar previews — can be removed once
// the item has gone.
func (s *Store) ExistingItemIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	// Chunked to stay well inside SQLite's bound-parameter limit.
	for len(ids) > 0 {
		chunk := ids[:min(len(ids), 500)]
		ids = ids[len(chunk):]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT id FROM items WHERE id IN (?`+strings.Repeat(",?", len(chunk)-1)+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("existing items: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("existing items: %w", err)
			}
			out[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("existing items: %w", err)
		}
	}
	return out, nil
}
