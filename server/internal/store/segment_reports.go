package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

// SegmentReport is a viewer saying an episode's or film's intro or credits
// are in the wrong place.
type SegmentReport struct {
	ID     string `json:"id"`
	ItemID string `json:"itemId"`
	UserID string `json:"userId"`
	// UserName is the reporter's display name, else their username.
	UserName string `json:"userName"`
	Kind     string `json:"kind"`
	// PositionSecs is where playback was when the report was made.
	PositionSecs float64 `json:"positionSecs"`
	Note         string  `json:"note,omitempty"`
	CreatedAt    string  `json:"createdAt"`
	// ResolvedAt is "" while the report is open; ResolvedBy is the user id
	// of whoever closed it.
	ResolvedAt string `json:"resolvedAt,omitempty"`
	ResolvedBy string `json:"resolvedBy,omitempty"`

	// What the report is about, so a list of them reads without a lookup
	// per row.
	ItemKind    ItemKind `json:"itemKind"`
	Title       string   `json:"title"`
	LibraryID   string   `json:"libraryId"`
	SeriesTitle string   `json:"seriesTitle,omitempty"`
	SeasonID    string   `json:"seasonId,omitempty"`
	SeasonNum   int      `json:"seasonNum,omitempty"`
	EpisodeNum  int      `json:"episodeNum,omitempty"`
	// SeasonOpen counts the open reports on the report's season — a
	// film's, on the film — this one included. Three or more suggest the
	// season wants detecting again rather than each episode correcting.
	SeasonOpen int `json:"seasonOpen"`
}

// SegmentReportRepo keeps reports of wrong intro and credits times.
type SegmentReportRepo interface {
	CreateSegmentReport(ctx context.Context, r SegmentReport) (SegmentReport, error)
	SegmentReports(ctx context.Context, openOnly bool, limit int) ([]SegmentReport, error)
	SegmentReport(ctx context.Context, id string) (SegmentReport, error)
	ResolveSegmentReport(ctx context.Context, id, byUserID string) error
}

var _ SegmentReportRepo = (*Store)(nil)

const segmentReportSelect = `SELECT r.id, r.item_id, r.user_id,
	COALESCE(NULLIF(u.display_name, ''), u.username, ''), r.kind, r.position_secs, r.note,
	r.created_at, r.resolved_at, r.resolved_by,
	i.kind, i.title, i.library_id, COALESCE(sr.title, ''),
	CASE WHEN i.kind = 'episode' THEN COALESCE(i.parent_id, '') ELSE '' END,
	i.season_num, i.episode_num,
	(SELECT count(*) FROM segment_reports o JOIN items oi ON oi.id = o.item_id
	 WHERE o.resolved_at = '' AND CASE WHEN i.kind = 'episode'
	   THEN oi.parent_id = i.parent_id ELSE o.item_id = i.id END)
	FROM segment_reports r
	JOIN items i ON i.id = r.item_id
	LEFT JOIN users u ON u.id = r.user_id
	LEFT JOIN items sn ON sn.id = i.parent_id
	LEFT JOIN items sr ON sr.id = sn.parent_id`

func scanSegmentReport(row interface{ Scan(...any) error }) (SegmentReport, error) {
	var r SegmentReport
	err := row.Scan(&r.ID, &r.ItemID, &r.UserID, &r.UserName, &r.Kind, &r.PositionSecs, &r.Note,
		&r.CreatedAt, &r.ResolvedAt, &r.ResolvedBy,
		&r.ItemKind, &r.Title, &r.LibraryID, &r.SeriesTitle, &r.SeasonID, &r.SeasonNum, &r.EpisodeNum,
		&r.SeasonOpen)
	return r, err
}

// CreateSegmentReport records a report. A user's open report on the same
// item and kind is updated instead — the position and note replaced, the
// time made now — so one person reporting twice counts once.
func (s *Store) CreateSegmentReport(ctx context.Context, r SegmentReport) (SegmentReport, error) {
	if !slices.Contains(SegmentKinds, r.Kind) {
		return SegmentReport{}, fmt.Errorf("unknown segment kind %q", r.Kind)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SegmentReport{}, fmt.Errorf("create segment report: %w", err)
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM segment_reports WHERE item_id = ? AND user_id = ? AND kind = ? AND resolved_at = ''`,
		r.ItemID, r.UserID, r.Kind).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = uuid.NewString()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO segment_reports (id, item_id, user_id, kind, position_secs, note) VALUES (?, ?, ?, ?, ?, ?)`,
			id, r.ItemID, r.UserID, r.Kind, r.PositionSecs, r.Note); err != nil {
			return SegmentReport{}, fmt.Errorf("create segment report: %w", err)
		}
	case err != nil:
		return SegmentReport{}, fmt.Errorf("create segment report: %w", err)
	default:
		if _, err := tx.ExecContext(ctx,
			`UPDATE segment_reports SET position_secs = ?, note = ?, created_at = datetime('now') WHERE id = ?`,
			r.PositionSecs, r.Note, id); err != nil {
			return SegmentReport{}, fmt.Errorf("update segment report: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return SegmentReport{}, fmt.Errorf("create segment report: %w", err)
	}
	return s.SegmentReport(ctx, id)
}

// SegmentReport returns one report.
func (s *Store) SegmentReport(ctx context.Context, id string) (SegmentReport, error) {
	r, err := scanSegmentReport(s.db.QueryRowContext(ctx, segmentReportSelect+` WHERE r.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SegmentReport{}, ErrNotFound
	}
	if err != nil {
		return SegmentReport{}, fmt.Errorf("segment report %s: %w", id, err)
	}
	return r, nil
}

// SegmentReports lists reports, newest first: only the open ones, or all.
func (s *Store) SegmentReports(ctx context.Context, openOnly bool, limit int) ([]SegmentReport, error) {
	q := segmentReportSelect
	if openOnly {
		q += ` WHERE r.resolved_at = ''`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY r.created_at DESC, r.id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("segment reports: %w", err)
	}
	defer rows.Close()
	out := []SegmentReport{}
	for rows.Next() {
		r, err := scanSegmentReport(rows)
		if err != nil {
			return nil, fmt.Errorf("segment reports: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("segment reports: %w", err)
	}
	return out, nil
}

// ResolveSegmentReport closes a report. Closing one already closed changes
// nothing; ErrNotFound when there is no such report.
func (s *Store) ResolveSegmentReport(ctx context.Context, id, byUserID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE segment_reports SET resolved_at = datetime('now'), resolved_by = ? WHERE id = ? AND resolved_at = ''`,
		byUserID, id)
	if err != nil {
		return fmt.Errorf("resolve segment report: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := s.SegmentReport(ctx, id); err != nil {
		return err
	}
	return nil
}
