package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type Scan struct {
	ID           string `json:"id"`
	LibraryID    string `json:"libraryId"`
	Status       string `json:"status"`
	FilesSeen    int    `json:"filesSeen"`
	ItemsCreated int    `json:"itemsCreated"`
	Error        string `json:"error,omitempty"`
	StartedAt    string `json:"startedAt"`
	FinishedAt   string `json:"finishedAt,omitempty"`
}

type ScanRepo interface {
	CreateScan(ctx context.Context, libraryID string) (Scan, error)
	FinishScan(ctx context.Context, id, status, errMsg string, filesSeen, itemsCreated int) error
	UpdateScanProgress(ctx context.Context, id string, filesSeen, itemsCreated int) error
	Scans(ctx context.Context, limit int) ([]Scan, error)
	ScanByID(ctx context.Context, id string) (Scan, error)
	RunningScan(ctx context.Context, libraryID string) (Scan, bool, error)
	FailOrphanedScans(ctx context.Context) (int64, error)
}

var _ ScanRepo = (*Store)(nil)

func (s *Store) CreateScan(ctx context.Context, libraryID string) (Scan, error) {
	id := uuid.NewString()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO scans (id, library_id) VALUES (?, ?)`, id, libraryID); err != nil {
		return Scan{}, fmt.Errorf("create scan: %w", err)
	}
	// Read the row back so callers (and the event stream) get the DB's
	// started_at rather than a zero value.
	return s.ScanByID(ctx, id)
}

func (s *Store) FinishScan(ctx context.Context, id, status, errMsg string, filesSeen, itemsCreated int) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE scans SET status = ?, error = ?, files_seen = ?, items_created = ?,
		 finished_at = datetime('now') WHERE id = ?`,
		status, errMsg, filesSeen, itemsCreated, id); err != nil {
		return fmt.Errorf("finish scan %s: %w", id, err)
	}
	return nil
}

// UpdateScanProgress records how far a running scan has got, so observers
// (the /scans API and the event stream) can show live progress.
func (s *Store) UpdateScanProgress(ctx context.Context, id string, filesSeen, itemsCreated int) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE scans SET files_seen = ?, items_created = ? WHERE id = ? AND status = 'running'`,
		filesSeen, itemsCreated, id); err != nil {
		return fmt.Errorf("scan progress %s: %w", id, err)
	}
	return nil
}

const scanCols = `id, library_id, status, files_seen, items_created, error, started_at, COALESCE(finished_at, '')`

func (s *Store) ScanByID(ctx context.Context, id string) (Scan, error) {
	var sc Scan
	err := s.db.QueryRowContext(ctx,
		`SELECT `+scanCols+` FROM scans WHERE id = ?`, id).
		Scan(&sc.ID, &sc.LibraryID, &sc.Status, &sc.FilesSeen, &sc.ItemsCreated, &sc.Error, &sc.StartedAt, &sc.FinishedAt)
	if err != nil {
		return Scan{}, fmt.Errorf("scan %s: %w", id, err)
	}
	return sc, nil
}

// RunningScan reports whether a library has a scan in progress — the
// cross-process lock (CLI scans and server scans share the database).
func (s *Store) RunningScan(ctx context.Context, libraryID string) (Scan, bool, error) {
	var sc Scan
	err := s.db.QueryRowContext(ctx,
		`SELECT `+scanCols+` FROM scans WHERE library_id = ? AND status = 'running' LIMIT 1`, libraryID).
		Scan(&sc.ID, &sc.LibraryID, &sc.Status, &sc.FilesSeen, &sc.ItemsCreated, &sc.Error, &sc.StartedAt, &sc.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Scan{}, false, nil
	}
	if err != nil {
		return Scan{}, false, fmt.Errorf("running scan: %w", err)
	}
	return sc, true, nil
}

// FailOrphanedScans marks all 'running' scans as failed — called at server
// startup, when any scan still marked running belonged to a dead process.
func (s *Store) FailOrphanedScans(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE scans SET status = 'failed', error = 'interrupted (process exited)',
		 finished_at = datetime('now') WHERE status = 'running'`)
	if err != nil {
		return 0, fmt.Errorf("fail orphaned scans: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Store) Scans(ctx context.Context, limit int) ([]Scan, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, library_id, status, files_seen, items_created, error, started_at,
		 COALESCE(finished_at, '') FROM scans ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list scans: %w", err)
	}
	defer rows.Close()
	scans := []Scan{}
	for rows.Next() {
		var sc Scan
		if err := rows.Scan(&sc.ID, &sc.LibraryID, &sc.Status, &sc.FilesSeen,
			&sc.ItemsCreated, &sc.Error, &sc.StartedAt, &sc.FinishedAt); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		scans = append(scans, sc)
	}
	return scans, rows.Err()
}
