package store

import (
	"context"
	"fmt"
)

// PruneHistory deletes audit-log and play-event rows older than the given
// number of days, returning how many of each went.
//
// Both tables are append-only and grow without limit: every sign-in, every
// settings change, every play. On a household server that is slow growth,
// but it is growth with no ceiling and no one watching it — a year of daily
// use is tens of thousands of rows nobody will ever read.
//
// Zero days disables pruning, for an operator who wants the full history.
func (s *Store) PruneHistory(ctx context.Context, days int) (audit, plays int64, err error) {
	if days <= 0 {
		return 0, 0, nil
	}
	// SQLite computes the cutoff itself, so this does not depend on the
	// server's clock agreeing with the one that wrote the rows.
	cutoff := fmt.Sprintf("-%d days", days)

	res, err := s.db.ExecContext(ctx,
		`DELETE FROM audit_log WHERE at < datetime('now', ?)`, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("prune audit log: %w", err)
	}
	audit, _ = res.RowsAffected()

	res, err = s.db.ExecContext(ctx,
		`DELETE FROM play_events WHERE started_at < datetime('now', ?)`, cutoff)
	if err != nil {
		return audit, 0, fmt.Errorf("prune play events: %w", err)
	}
	plays, _ = res.RowsAffected()
	return audit, plays, nil
}
