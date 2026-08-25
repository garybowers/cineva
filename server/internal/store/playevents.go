package store

import (
	"context"
	"fmt"
)

// PlayDay is one day's play counts by media kind (UTC dates).
type PlayDay struct {
	Date     string `json:"date"`
	Movies   int    `json:"movies"`
	Episodes int    `json:"episodes"`
	Tracks   int    `json:"tracks"`
}

type PlayEventRepo interface {
	RecordPlay(ctx context.Context, userID, itemID string, kind ItemKind) error
	PlaysByDay(ctx context.Context, days int) ([]PlayDay, error)
}

var _ PlayEventRepo = (*Store)(nil)

func (s *Store) RecordPlay(ctx context.Context, userID, itemID string, kind ItemKind) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO play_events (user_id, item_id, kind) VALUES (?, ?, ?)`,
		userID, itemID, string(kind)); err != nil {
		return fmt.Errorf("record play: %w", err)
	}
	return nil
}

// PlaysByDay returns one entry per day for the last `days` days (oldest
// first), including days with no plays so charts get a continuous axis.
func (s *Store) PlaysByDay(ctx context.Context, days int) ([]PlayDay, error) {
	if days < 1 {
		days = 1
	}
	// A recursive CTE generates the date spine so empty days are present.
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE spine(d) AS (
			SELECT date('now', ?)
			UNION ALL SELECT date(d, '+1 day') FROM spine WHERE d < date('now')
		)
		SELECT spine.d,
		       COALESCE(SUM(kind = 'movie'), 0),
		       COALESCE(SUM(kind = 'episode'), 0),
		       COALESCE(SUM(kind = 'track'), 0)
		FROM spine LEFT JOIN play_events p ON date(p.started_at) = spine.d
		GROUP BY spine.d ORDER BY spine.d`,
		fmt.Sprintf("-%d days", days-1))
	if err != nil {
		return nil, fmt.Errorf("plays by day: %w", err)
	}
	defer rows.Close()
	out := []PlayDay{}
	for rows.Next() {
		var d PlayDay
		if err := rows.Scan(&d.Date, &d.Movies, &d.Episodes, &d.Tracks); err != nil {
			return nil, fmt.Errorf("plays row: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
