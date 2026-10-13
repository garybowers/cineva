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
	RecordPlay(ctx context.Context, userID, itemID string, kind ItemKind) (int64, error)
	FinishPlay(ctx context.Context, id int64, secs int, completed, counts bool) error
	PlaysByDay(ctx context.Context, days int) ([]PlayDay, error)
	PlayTotals(ctx context.Context, days int) (PlayTotals, error)
}

// PlayTotals summarises a window of history: how many plays, how long they
// ran in total, and how many reached the end.
type PlayTotals struct {
	Plays     int `json:"plays"`
	Completed int `json:"completed"`
	Secs      int `json:"secs"`
}

var _ PlayEventRepo = (*Store)(nil)

// RecordPlay opens a history row and returns its id, which FinishPlay
// closes once the playback ends.
func (s *Store) RecordPlay(ctx context.Context, userID, itemID string, kind ItemKind) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO play_events (user_id, item_id, kind) VALUES (?, ?, ?)`,
		userID, itemID, string(kind))
	if err != nil {
		return 0, fmt.Errorf("record play: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("record play id: %w", err)
	}
	return id, nil
}

// FinishPlay closes a history row with how long it ran and whether it
// finished. counts adds the play to the user's play count for the item
// (see migration 0035 for what counts as a play).
//
// Only ever written once: a playback that is stopped and resumed opens a new
// row, so a late duplicate — a reap racing an explicit stop — must not
// overwrite a longer measured session with a shorter one, nor count the
// play twice.
func (s *Store) FinishPlay(ctx context.Context, id int64, secs int, completed, counts bool) error {
	if secs < 0 {
		secs = 0
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("finish play %d: %w", id, err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE play_events
		 SET ended_at = datetime('now'), secs = ?, completed = ?
		 WHERE id = ? AND ended_at IS NULL`,
		secs, completed, id)
	if err != nil {
		return fmt.Errorf("finish play %d: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("finish play %d: %w", id, err)
	} else if n == 1 && counts {
		// Joined to items so a play of something deleted since is simply
		// not counted, rather than failing the foreign key.
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO play_counts (user_id, item_id, plays, last_played_at)
			 SELECT p.user_id, p.item_id, 1, datetime('now')
			 FROM play_events p JOIN items i ON i.id = p.item_id WHERE p.id = ?
			 ON CONFLICT (user_id, item_id) DO UPDATE SET
			 	plays = plays + 1, last_played_at = excluded.last_played_at`, id); err != nil {
			return fmt.Errorf("count play %d: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("finish play %d: %w", id, err)
	}
	return nil
}

// PlayTotals aggregates the last n days of history.
func (s *Store) PlayTotals(ctx context.Context, days int) (PlayTotals, error) {
	if days < 1 {
		days = 1
	}
	var t PlayTotals
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(completed), 0), COALESCE(SUM(secs), 0)
		 FROM play_events
		 WHERE started_at >= datetime('now', ?)`,
		fmt.Sprintf("-%d days", days)).Scan(&t.Plays, &t.Completed, &t.Secs)
	if err != nil {
		return PlayTotals{}, fmt.Errorf("play totals: %w", err)
	}
	return t, nil
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
