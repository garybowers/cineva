package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SetSidecarStamps records sidecar fingerprints for items a scan found
// unchanged but that had none stored yet — every row, on the first scan
// after migration 0032. Adopting the current fingerprint costs one write
// per row here, where treating the missing one as a change would have
// reread and rematched every film that has an .nfo or a subtitle beside it.
//
// updated_at is left alone: nothing a client shows has changed, and a
// touched row is refetched by every client polling the library.
func (s *Store) SetSidecarStamps(ctx context.Context, stamps map[string]int64) error {
	if len(stamps) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set sidecar stamps: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `UPDATE items SET sidecar_stamp = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("set sidecar stamps: %w", err)
	}
	defer stmt.Close()
	for id, stamp := range stamps {
		if _, err := stmt.ExecContext(ctx, stamp, id); err != nil {
			return fmt.Errorf("set sidecar stamp %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set sidecar stamps: %w", err)
	}
	return nil
}

// ForgetFileStamps clears the recorded mtime of the given items, so the
// next incremental scan reads their files again as though they had changed.
func (s *Store) ForgetFileStamps(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE items SET file_mtime = 0 WHERE id IN (?`+strings.Repeat(", ?", len(ids)-1)+`)`, args...)
	if err != nil {
		return fmt.Errorf("forget file stamps: %w", err)
	}
	return nil
}

// UnmatchedItem is a film or series the metadata provider has not matched,
// with the record of how often and when it was last tried.
type UnmatchedItem struct {
	ID       string
	Kind     ItemKind
	Path     string
	Title    string
	Year     int
	Attempts int
	// TriedAt is when the last failed lookup ran; zero when none is
	// recorded.
	TriedAt time.Time
}

// UnmatchedItems lists a library's films and series that have no provider
// id. Alternative versions are included: a match is what groups them with
// their primary.
func (s *Store) UnmatchedItems(ctx context.Context, libraryID string) ([]UnmatchedItem, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, path, title, year, match_attempts, match_tried_at FROM items
		 WHERE library_id = ? AND kind IN ('movie', 'series') AND tmdb_id = 0
		 ORDER BY match_tried_at, title`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("unmatched items %s: %w", libraryID, err)
	}
	defer rows.Close()
	var out []UnmatchedItem
	for rows.Next() {
		var it UnmatchedItem
		var tried int64
		if err := rows.Scan(&it.ID, &it.Kind, &it.Path, &it.Title, &it.Year, &it.Attempts, &tried); err != nil {
			return nil, fmt.Errorf("unmatched items %s: %w", libraryID, err)
		}
		if tried > 0 {
			it.TriedAt = time.Unix(tried, 0)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("unmatched items %s: %w", libraryID, err)
	}
	return out, nil
}

// RecordMatchFailure notes that a lookup for the item found nothing, which
// pushes its next retry further out. Like SetSidecarStamps it leaves
// updated_at alone, since nothing visible changed.
func (s *Store) RecordMatchFailure(ctx context.Context, id string, at time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE items SET match_attempts = match_attempts + 1, match_tried_at = ? WHERE id = ?`,
		at.Unix(), id); err != nil {
		return fmt.Errorf("record match failure %s: %w", id, err)
	}
	return nil
}
