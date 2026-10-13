package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
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

// SegmentKinds lists every kind of segment, in the order they are shown.
var SegmentKinds = []string{SegmentIntro, SegmentCredits}

// SegmentState is where one kind of segment stands for an item, for the
// administrator correcting it: what is served, and whether detection put it
// there or a person did.
type SegmentState struct {
	Kind string `json:"kind"`
	// Found is true when the item has this segment; StartSecs and EndSecs
	// are then its bounds.
	Found     bool    `json:"found"`
	StartSecs float64 `json:"startSecs,omitempty"`
	EndSecs   float64 `json:"endSecs,omitempty"`
	// Analysed is true when detection has looked at the current file, or
	// the segment was set by hand.
	Analysed bool `json:"analysed"`
	// Locked is true when an administrator set it — a segment, or that
	// there is none — and detection leaves it alone.
	Locked bool `json:"locked"`
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
	AdjacentSeasonEpisodes(ctx context.Context, seasonID string, n int) ([]SegmentEpisode, error)
	SegmentStates(ctx context.Context, itemID string) ([]SegmentState, error)
	SetManualSegment(ctx context.Context, itemID, kind string, seg *Segment) error
	ClearManualSegment(ctx context.Context, itemID, kind string) error
	RedetectSeasonSegments(ctx context.Context, seasonID string) (int64, error)
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
		   AND e.file_mtime != 0 AND (a.item_id IS NULL OR (a.locked = 0 AND a.file_mtime != e.file_mtime))`,
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
		   AND e.file_mtime != 0 AND (a.item_id IS NULL OR (a.locked = 0 AND a.file_mtime != e.file_mtime))`,
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
		        a.item_id IS NOT NULL AND (a.locked = 1 OR a.file_mtime = e.file_mtime)
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
// that kind is replaced — unless an administrator has set it by hand
// (SetManualSegment), which detection never overrides: the call then does
// nothing.
func (s *Store) SaveSegmentAnalysis(ctx context.Context, itemID, kind string, fileMtime int64, seg *Segment) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save segment analysis: %w", err)
	}
	defer tx.Rollback()
	var locked bool
	err = tx.QueryRowContext(ctx,
		`SELECT locked FROM segment_analysis WHERE item_id = ? AND kind = ?`, itemID, kind).Scan(&locked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("save segment analysis: %w", err)
	}
	if locked {
		return nil // set by hand while detection was reading
	}
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

// SegmentStates reports every kind of segment for an item, found or not.
func (s *Store) SegmentStates(ctx context.Context, itemID string) ([]SegmentState, error) {
	segs, err := s.Segments(ctx, itemID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.kind, a.locked, a.locked = 1 OR a.file_mtime = i.file_mtime
		 FROM segment_analysis a JOIN items i ON i.id = a.item_id
		 WHERE a.item_id = ?`, itemID)
	if err != nil {
		return nil, fmt.Errorf("segment states for %s: %w", itemID, err)
	}
	defer rows.Close()
	type analysis struct{ locked, current bool }
	analysed := map[string]analysis{}
	for rows.Next() {
		var kind string
		var a analysis
		if err := rows.Scan(&kind, &a.locked, &a.current); err != nil {
			return nil, fmt.Errorf("segment states for %s: %w", itemID, err)
		}
		analysed[kind] = a
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("segment states for %s: %w", itemID, err)
	}
	out := make([]SegmentState, 0, len(SegmentKinds))
	for _, kind := range SegmentKinds {
		st := SegmentState{Kind: kind, Analysed: analysed[kind].current, Locked: analysed[kind].locked}
		for _, sg := range segs {
			if sg.Kind == kind {
				st.Found, st.StartSecs, st.EndSecs = true, sg.StartSecs, sg.EndSecs
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// SetManualSegment sets an item's segment of one kind by hand — seg nil
// saying it has none — and locks it, so detection leaves it alone until
// ClearManualSegment.
func (s *Store) SetManualSegment(ctx context.Context, itemID, kind string, seg *Segment) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set segment: %w", err)
	}
	defer tx.Rollback()
	var mtime int64
	if err := tx.QueryRowContext(ctx, `SELECT file_mtime FROM items WHERE id = ?`, itemID).Scan(&mtime); err != nil {
		return fmt.Errorf("set segment for %s: %w", itemID, err)
	}
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
		`INSERT INTO segment_analysis (item_id, kind, file_mtime, locked) VALUES (?, ?, ?, 1)
		 ON CONFLICT (item_id, kind) DO UPDATE SET
		   file_mtime = excluded.file_mtime, locked = 1, analysed_at = datetime('now')`,
		itemID, kind, mtime); err != nil {
		return fmt.Errorf("lock segment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set segment: %w", err)
	}
	return nil
}

// ClearManualSegment undoes SetManualSegment: the segment and the record of
// analysis go, so detection looks at the item afresh on its next pass.
// An item with no hand-set segment of that kind is left as it is.
func (s *Store) ClearManualSegment(ctx context.Context, itemID, kind string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("clear segment: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`DELETE FROM segment_analysis WHERE item_id = ? AND kind = ? AND locked = 1`, itemID, kind)
	if err != nil {
		return fmt.Errorf("clear segment lock: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM media_segments WHERE item_id = ? AND kind = ?`, itemID, kind); err != nil {
			return fmt.Errorf("clear segment: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("clear segment: %w", err)
	}
	return nil
}

// RedetectSeasonSegments marks every episode of a season due for detection
// again, both kinds, except what an administrator set by hand. What was
// found before stays served until the new analysis replaces it. Returns how
// many analyses were reopened.
func (s *Store) RedetectSeasonSegments(ctx context.Context, seasonID string) (int64, error) {
	// -1 matches no file's mtime, so each episode reads as changed.
	res, err := s.db.ExecContext(ctx,
		`UPDATE segment_analysis SET file_mtime = -1
		 WHERE locked = 0 AND item_id IN (SELECT id FROM items WHERE parent_id = ? AND kind = 'episode')`,
		seasonID)
	if err != nil {
		return 0, fmt.Errorf("redetect season %s: %w", seasonID, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// AdjacentSeasonEpisodes returns up to n episodes from the other seasons of
// a season's series, nearest first, to compare a season's lone episode
// with: a title sequence rarely changes between seasons. Nearest means the
// closest season, alternating after and before; within a season, the
// episodes closest to this one — the start of a later season, the end of an
// earlier one. Specials (season 0) are never offered: they seldom share the
// series' titles. Episodes not yet stamped by a scan are left out, as
// SeasonSegmentState leaves them out.
func (s *Store) AdjacentSeasonEpisodes(ctx context.Context, seasonID string, n int) ([]SegmentEpisode, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT sn.season_num - me.season_num, e.id, e.episode_num, e.path, e.size_bytes, e.runtime_secs, e.file_mtime
		 FROM items me
		 JOIN items sn ON sn.parent_id = me.parent_id AND sn.kind = 'season' AND sn.id != me.id AND sn.season_num > 0
		 JOIN items e ON e.parent_id = sn.id AND e.kind = 'episode' AND e.file_mtime != 0
		 WHERE me.id = ? AND me.parent_id IS NOT NULL AND sn.season_num != me.season_num
		 ORDER BY e.episode_num, e.path`, seasonID)
	if err != nil {
		return nil, fmt.Errorf("adjacent season episodes: %w", err)
	}
	defer rows.Close()
	// Keyed by seasons away: negative before, positive after.
	bySeason := map[int][]SegmentEpisode{}
	for rows.Next() {
		var dist int
		var ep SegmentEpisode
		if err := rows.Scan(&dist, &ep.ID, &ep.EpisodeNum, &ep.Path, &ep.SizeBytes, &ep.RuntimeSecs, &ep.FileMtime); err != nil {
			return nil, fmt.Errorf("adjacent season episodes: %w", err)
		}
		bySeason[dist] = append(bySeason[dist], ep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adjacent season episodes: %w", err)
	}
	// Seasons in order of nearness; at each distance the later season's
	// episodes and the earlier's alternate, so one season whose titles
	// changed cannot supply every partner.
	var dists []int
	for d := range bySeason {
		if d > 0 || bySeason[-d] == nil {
			dists = append(dists, abs(d))
		}
	}
	sort.Ints(dists)
	var out []SegmentEpisode
	for _, d := range dists {
		after, before := bySeason[d], bySeason[-d]
		for k := 0; k < len(after) || k < len(before); k++ {
			if k < len(after) {
				out = append(out, after[k])
			}
			// An earlier season's closing episodes are the nearest.
			if k < len(before) {
				out = append(out, before[len(before)-1-k])
			}
			if len(out) >= n {
				return out[:n], nil
			}
		}
	}
	return out, nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
