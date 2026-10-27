package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Download jobs (server/docs/offline.md): what a native app asked to keep,
// prepared on the server and fetched afterwards.

// Job statuses.
const (
	DownloadQueued    = "queued"
	DownloadPreparing = "preparing"
	DownloadReady     = "ready"
	DownloadFailed    = "failed"
)

// MaxUnfinishedDownloads is how many jobs a user may have queued or
// preparing at once: enough for an artist's discography, not a library.
const MaxUnfinishedDownloads = 500

// FinishedDownloadDays is how long a ready or failed job is kept: an app
// fetches what it asked for well within a day.
const FinishedDownloadDays = 1

// DownloadJob is one track at one profile for one user.
type DownloadJob struct {
	ID       string `json:"id"`
	UserID   string `json:"-"`
	DeviceID string `json:"deviceId,omitempty"`
	ItemID   string `json:"itemId"`
	Profile  string `json:"profile"`
	Status   string `json:"status"`
	// Bytes is the file's size once known (an original's at once, a
	// transcode's when it is ready).
	Bytes int64 `json:"bytes,omitempty"`
	// Error says why a job failed.
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	// URL is where the file is fetched from, filled by the API when the
	// job is ready; not stored.
	URL string `json:"url,omitempty"`
}

const downloadJobCols = `id, user_id, device_id, item_id, profile, status, bytes, error, created_at, updated_at`

func scanDownloadJob(row interface{ Scan(...any) error }) (DownloadJob, error) {
	var j DownloadJob
	err := row.Scan(&j.ID, &j.UserID, &j.DeviceID, &j.ItemID, &j.Profile, &j.Status, &j.Bytes, &j.Error, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

// CreateDownloadJobs makes one job per item for the user, all with the
// given profile and status, in one transaction, and returns them in the
// items' order.
func (s *Store) CreateDownloadJobs(ctx context.Context, userID, deviceID, profile string, items []Item, status func(Item) (string, int64)) ([]DownloadJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create download jobs: %w", err)
	}
	defer tx.Rollback()
	out := make([]DownloadJob, 0, len(items))
	for _, it := range items {
		st, bytes := status(it)
		id := uuid.NewString()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO download_jobs (id, user_id, device_id, item_id, profile, status, bytes) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, userID, deviceID, it.ID, profile, st, bytes); err != nil {
			return nil, fmt.Errorf("create download job: %w", err)
		}
		j, err := scanDownloadJob(tx.QueryRowContext(ctx, `SELECT `+downloadJobCols+` FROM download_jobs WHERE id = ?`, id))
		if err != nil {
			return nil, fmt.Errorf("create download job: %w", err)
		}
		out = append(out, j)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("create download jobs: %w", err)
	}
	return out, nil
}

// DownloadJob returns one job; ErrNotFound when there is none.
func (s *Store) DownloadJob(ctx context.Context, id string) (DownloadJob, error) {
	j, err := scanDownloadJob(s.db.QueryRowContext(ctx, `SELECT `+downloadJobCols+` FROM download_jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return DownloadJob{}, ErrNotFound
	}
	if err != nil {
		return DownloadJob{}, fmt.Errorf("download job %s: %w", id, err)
	}
	return j, nil
}

// DownloadJobs lists a user's jobs, oldest first.
func (s *Store) DownloadJobs(ctx context.Context, userID string) ([]DownloadJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+downloadJobCols+` FROM download_jobs WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("download jobs: %w", err)
	}
	defer rows.Close()
	out := []DownloadJob{}
	for rows.Next() {
		j, err := scanDownloadJob(rows)
		if err != nil {
			return nil, fmt.Errorf("download jobs: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// UnfinishedDownloadJobs is how many of a user's jobs are still queued or
// preparing, and the ids of the ones that are, so a restart can pick them
// up (nil ids when none).
func (s *Store) UnfinishedDownloadJobs(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM download_jobs WHERE user_id = ? AND status IN ('queued', 'preparing')`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("unfinished download jobs: %w", err)
	}
	return n, nil
}

// SetDownloadJobStatus moves a job on: preparing, ready with its size, or
// failed with why.
func (s *Store) SetDownloadJobStatus(ctx context.Context, id, status string, bytes int64, errText string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE download_jobs SET status = ?, bytes = ?, error = ?, updated_at = datetime('now') WHERE id = ?`,
		status, bytes, strings.TrimSpace(errText), id); err != nil {
		return fmt.Errorf("download job %s: %w", id, err)
	}
	return nil
}

// DeleteDownloadJob forgets a user's job; ErrNotFound when it is not theirs.
func (s *Store) DeleteDownloadJob(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM download_jobs WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete download job: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneDownloadJobs forgets finished jobs (ready or failed) older than the
// given number of days: an app fetches what it asked for well within a
// day, and the cache keeps the file regardless.
func (s *Store) PruneDownloadJobs(ctx context.Context, days int) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM download_jobs WHERE status IN ('ready', 'failed')
		   AND updated_at < datetime('now', ?)`, fmt.Sprintf("-%d days", days))
	if err != nil {
		return 0, fmt.Errorf("prune download jobs: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// LibraryItemIDs is every item id in a library, for a client's local copy
// to find what was deleted: cheap, since it reads one indexed column.
func (s *Store) LibraryItemIDs(ctx context.Context, libraryID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM items WHERE library_id = ? ORDER BY id`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("library ids: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("library ids: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
