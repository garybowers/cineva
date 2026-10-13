package store

import (
	"context"
	"fmt"
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
