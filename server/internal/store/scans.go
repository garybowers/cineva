package store

import (
	"context"
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
	Scans(ctx context.Context, limit int) ([]Scan, error)
}

var _ ScanRepo = (*Store)(nil)

func (s *Store) CreateScan(ctx context.Context, libraryID string) (Scan, error) {
	id := uuid.NewString()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO scans (id, library_id) VALUES (?, ?)`, id, libraryID); err != nil {
		return Scan{}, fmt.Errorf("create scan: %w", err)
	}
	return Scan{ID: id, LibraryID: libraryID, Status: "running"}, nil
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
