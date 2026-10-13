package store

import (
	"context"
	"fmt"
)

// Segment kinds.
const (
	SegmentIntro   = "intro"
	SegmentCredits = "credits"
)

// Segment is a part of an item that is not the item itself, in seconds from
// the start of the file.
type Segment struct {
	Kind      string  `json:"kind"`
	StartSecs float64 `json:"startSecs"`
	EndSecs   float64 `json:"endSecs"`
}

// SegmentEpisode is one episode of a season as segment detection sees it.
type SegmentEpisode struct {
	ID          string
	EpisodeNum  int
	Path        string
	SizeBytes   int64
	RuntimeSecs int
	FileMtime   int64
	// Analysed is true when the episode's current file has been analysed
	// for the kind asked about, whether or not anything was found.
	Analysed bool
}

type SegmentRepo interface {
	Segments(ctx context.Context, itemID string) ([]Segment, error)
	SeasonsNeedingSegments(ctx context.Context, libraryID, kind string) ([]string, error)
	SeasonSegmentState(ctx context.Context, seasonID, kind string) ([]SegmentEpisode, error)
	CountEpisodesNeedingSegments(ctx context.Context, libraryID, kind string) (int, error)
	SaveSegmentAnalysis(ctx context.Context, itemID, kind string, fileMtime int64, seg *Segment) error
}

var _ SegmentRepo = (*Store)(nil)

func (s *Store) Segments(ctx context.Context, itemID string) ([]Segment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT kind, start_secs, end_secs FROM media_segments WHERE item_id = ? ORDER BY start_secs`, itemID)
	if err != nil {
		return nil, fmt.Errorf("segments for %s: %w", itemID, err)
	}
	defer rows.Close()
	var out []Segment
	for rows.Next() {
		var sg Segment
		if err := rows.Scan(&sg.Kind, &sg.StartSecs, &sg.EndSecs); err != nil {
			return nil, fmt.Errorf("segments for %s: %w", itemID, err)
		}
		out = append(out, sg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("segments for %s: %w", itemID, err)
	}
	return out, nil
}

// SeasonsNeedingSegments lists the seasons of a library holding at least one
// episode whose current file has not been analysed for kind.
//
// Episodes with no recorded mtime are left out: they were last scanned
// before mtimes were kept, and analysing them now would only mean doing it
// again once the next scan records one.
func (s *Store) SeasonsNeedingSegments(ctx context.Context, libraryID, kind string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT e.parent_id FROM items e
		 LEFT JOIN segment_analysis a ON a.item_id = e.id AND a.kind = ?
		 WHERE e.library_id = ? AND e.kind = 'episode' AND e.parent_id IS NOT NULL
		   AND e.file_mtime != 0 AND (a.item_id IS NULL OR a.file_mtime != e.file_mtime)`,
		kind, libraryID)
	if err != nil {
		return nil, fmt.Errorf("seasons needing segments: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("seasons needing segments: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("seasons needing segments: %w", err)
	}
	return out, nil
}

// CountEpisodesNeedingSegments counts the episodes SeasonsNeedingSegments
// would have analysed, so progress can be shown in episodes — seasons vary
// too much in length to estimate time remaining by.
func (s *Store) CountEpisodesNeedingSegments(ctx context.Context, libraryID, kind string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM items e
		 LEFT JOIN segment_analysis a ON a.item_id = e.id AND a.kind = ?
		 WHERE e.library_id = ? AND e.kind = 'episode' AND e.parent_id IS NOT NULL
		   AND e.file_mtime != 0 AND (a.item_id IS NULL OR a.file_mtime != e.file_mtime)`,
		kind, libraryID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count episodes needing segments: %w", err)
	}
	return n, nil
}

// SeasonSegmentState returns a season's episodes in episode order, with
// whether each has been analysed for kind.
func (s *Store) SeasonSegmentState(ctx context.Context, seasonID, kind string) ([]SegmentEpisode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, e.episode_num, e.path, e.size_bytes, e.runtime_secs, e.file_mtime,
		        a.item_id IS NOT NULL AND a.file_mtime = e.file_mtime
		 FROM items e
		 LEFT JOIN segment_analysis a ON a.item_id = e.id AND a.kind = ?
		 WHERE e.parent_id = ? AND e.kind = 'episode' AND e.file_mtime != 0
		 ORDER BY e.episode_num, e.path`,
		kind, seasonID)
	if err != nil {
		return nil, fmt.Errorf("season segment state: %w", err)
	}
	defer rows.Close()
	var out []SegmentEpisode
	for rows.Next() {
		var ep SegmentEpisode
		if err := rows.Scan(&ep.ID, &ep.EpisodeNum, &ep.Path, &ep.SizeBytes, &ep.RuntimeSecs, &ep.FileMtime, &ep.Analysed); err != nil {
			return nil, fmt.Errorf("season segment state: %w", err)
		}
		out = append(out, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("season segment state: %w", err)
	}
	return out, nil
}

// SaveSegmentAnalysis records that an item's file was analysed for kind,
// with the segment found or nil when there was none. Any earlier segment of
// that kind is replaced.
func (s *Store) SaveSegmentAnalysis(ctx context.Context, itemID, kind string, fileMtime int64, seg *Segment) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save segment analysis: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM media_segments WHERE item_id = ? AND kind = ?`, itemID, kind); err != nil {
		return fmt.Errorf("clear segment: %w", err)
	}
	if seg != nil {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO media_segments (item_id, kind, start_secs, end_secs) VALUES (?, ?, ?, ?)`,
			itemID, kind, seg.StartSecs, seg.EndSecs); err != nil {
			return fmt.Errorf("insert segment: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO segment_analysis (item_id, kind, file_mtime) VALUES (?, ?, ?)
		 ON CONFLICT (item_id, kind) DO UPDATE SET
		   file_mtime = excluded.file_mtime, analysed_at = datetime('now')`,
		itemID, kind, fileMtime); err != nil {
		return fmt.Errorf("record segment analysis: %w", err)
	}
	return tx.Commit()
}
