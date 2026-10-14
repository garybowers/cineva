package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// NotifyDestination is somewhere notifications go: a household one an
// admin set up (OwnerID "") or a user's own (see docs/notifications.md).
type NotifyDestination struct {
	ID      string `json:"id"`
	OwnerID string `json:"ownerId"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	// Config is the kind's settings, secrets included. The API masks them.
	Config      map[string]string `json:"config"`
	Events      []string          `json:"events"`
	LibraryIDs  []string          `json:"libraryIds"`
	Enabled     bool              `json:"enabled"`
	LastSentAt  string            `json:"lastSentAt"`
	LastError   string            `json:"lastError"`
	LastErrorAt string            `json:"lastErrorAt"`
	CreatedAt   string            `json:"createdAt"`
	UpdatedAt   string            `json:"updatedAt"`
}

type NotifyRepo interface {
	NotifyDestinations(ctx context.Context, ownerID string) ([]NotifyDestination, error)
	EnabledNotifyDestinations(ctx context.Context) ([]NotifyDestination, error)
	NotifyDestination(ctx context.Context, id string) (NotifyDestination, error)
	CreateNotifyDestination(ctx context.Context, d NotifyDestination) (NotifyDestination, error)
	UpdateNotifyDestination(ctx context.Context, d NotifyDestination) (NotifyDestination, error)
	DeleteNotifyDestination(ctx context.Context, id string) error
	RecordNotifyResult(ctx context.Context, id, errMsg string) error
	SeriesWatchers(ctx context.Context, seriesID string, days int) ([]string, error)
}

var _ NotifyRepo = (*Store)(nil)

const notifyCols = `id, owner_id, kind, name, config, events, library_ids, enabled,
	last_sent_at, last_error, last_error_at, created_at, updated_at`

func scanNotify(row interface{ Scan(...any) error }) (NotifyDestination, error) {
	var d NotifyDestination
	var config, events, libs string
	if err := row.Scan(&d.ID, &d.OwnerID, &d.Kind, &d.Name, &config, &events, &libs, &d.Enabled,
		&d.LastSentAt, &d.LastError, &d.LastErrorAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return d, err
	}
	d.Config, d.Events, d.LibraryIDs = map[string]string{}, []string{}, []string{}
	for _, f := range []struct {
		raw string
		out any
	}{{config, &d.Config}, {events, &d.Events}, {libs, &d.LibraryIDs}} {
		if err := json.Unmarshal([]byte(f.raw), f.out); err != nil {
			return d, fmt.Errorf("destination %s: %w", d.ID, err)
		}
	}
	return d, nil
}

func (s *Store) queryNotify(ctx context.Context, where string, args ...any) ([]NotifyDestination, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+notifyCols+` FROM notify_destinations `+where+
		` ORDER BY name COLLATE NOCASE`, args...)
	if err != nil {
		return nil, fmt.Errorf("notification destinations: %w", err)
	}
	defer rows.Close()
	out := []NotifyDestination{}
	for rows.Next() {
		d, err := scanNotify(rows)
		if err != nil {
			return nil, fmt.Errorf("notification destinations: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// NotifyDestinations lists one owner's destinations ("" for the household's).
func (s *Store) NotifyDestinations(ctx context.Context, ownerID string) ([]NotifyDestination, error) {
	return s.queryNotify(ctx, `WHERE owner_id = ?`, ownerID)
}

// EnabledNotifyDestinations lists every destination that is switched on.
func (s *Store) EnabledNotifyDestinations(ctx context.Context) ([]NotifyDestination, error) {
	return s.queryNotify(ctx, `WHERE enabled = 1`)
}

func (s *Store) NotifyDestination(ctx context.Context, id string) (NotifyDestination, error) {
	d, err := scanNotify(s.db.QueryRowContext(ctx, `SELECT `+notifyCols+` FROM notify_destinations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return NotifyDestination{}, ErrNotFound
	}
	if err != nil {
		return NotifyDestination{}, fmt.Errorf("notification destination %s: %w", id, err)
	}
	return d, nil
}

func encodeNotify(d NotifyDestination) (config, events, libs string, err error) {
	if d.Config == nil {
		d.Config = map[string]string{}
	}
	if d.Events == nil {
		d.Events = []string{}
	}
	if d.LibraryIDs == nil {
		d.LibraryIDs = []string{}
	}
	var b []byte
	if b, err = json.Marshal(d.Config); err != nil {
		return
	}
	config = string(b)
	if b, err = json.Marshal(d.Events); err != nil {
		return
	}
	events = string(b)
	if b, err = json.Marshal(d.LibraryIDs); err != nil {
		return
	}
	libs = string(b)
	return
}

func (s *Store) CreateNotifyDestination(ctx context.Context, d NotifyDestination) (NotifyDestination, error) {
	config, events, libs, err := encodeNotify(d)
	if err != nil {
		return NotifyDestination{}, fmt.Errorf("encode destination: %w", err)
	}
	d.ID = uuid.NewString()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO notify_destinations
		(id, owner_id, kind, name, config, events, library_ids, enabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.OwnerID, d.Kind, strings.TrimSpace(d.Name), config, events, libs, d.Enabled); err != nil {
		return NotifyDestination{}, fmt.Errorf("create destination: %w", err)
	}
	return s.NotifyDestination(ctx, d.ID)
}

// UpdateNotifyDestination rewrites everything but the owner and kind.
func (s *Store) UpdateNotifyDestination(ctx context.Context, d NotifyDestination) (NotifyDestination, error) {
	config, events, libs, err := encodeNotify(d)
	if err != nil {
		return NotifyDestination{}, fmt.Errorf("encode destination: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE notify_destinations SET name = ?, config = ?, events = ?,
		library_ids = ?, enabled = ?, updated_at = datetime('now') WHERE id = ?`,
		strings.TrimSpace(d.Name), config, events, libs, d.Enabled, d.ID)
	if err != nil {
		return NotifyDestination{}, fmt.Errorf("update destination: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotifyDestination{}, ErrNotFound
	}
	return s.NotifyDestination(ctx, d.ID)
}

func (s *Store) DeleteNotifyDestination(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notify_destinations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete destination: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordNotifyResult notes a delivery: errMsg "" for one that arrived.
func (s *Store) RecordNotifyResult(ctx context.Context, id, errMsg string) error {
	q := `UPDATE notify_destinations SET last_sent_at = datetime('now'), last_error = '' WHERE id = ?`
	args := []any{id}
	if errMsg != "" {
		q = `UPDATE notify_destinations SET last_error = ?, last_error_at = datetime('now') WHERE id = ?`
		args = []any{errMsg, id}
	}
	if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("record delivery: %w", err)
	}
	return nil
}

// SeriesWatchers lists the users who watched or part-watched an episode of
// a series in the last days: who "is watching" it, for new-episode
// messages. Whether each may still see the library is the caller's to
// check.
func (s *Store) SeriesWatchers(ctx context.Context, seriesID string, days int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT p.user_id
		FROM play_state p
		JOIN items e ON e.id = p.item_id AND e.kind = 'episode'
		JOIN items se ON se.id = e.parent_id
		WHERE se.parent_id = ? AND (p.watched = 1 OR p.position_secs > 0)
		  AND p.updated_at >= datetime('now', ?)`, seriesID, fmt.Sprintf("-%d days", days))
	if err != nil {
		return nil, fmt.Errorf("series watchers: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("series watchers: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
