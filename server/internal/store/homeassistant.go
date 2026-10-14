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

// Home Assistant (see docs/homeassistant.md): the one connection, the
// entities an admin has exposed and who may use them, modes, and the room
// each device is in. Home Assistant's own state is never stored here.

// HAConnection is the server's Home Assistant instance.
type HAConnection struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// Token is the long-lived access token. Never sent to clients.
	Token          string `json:"-"`
	PlaybackEvents bool   `json:"playbackEvents"`
	Version        string `json:"version"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

// HAModeStep is one step of a mode. Type says which fields apply:
// "action" runs Service on EntityID with the optional fields, "wait"
// pauses Seconds, "play" plays ItemID (or the title the mode was started
// from, when empty) on the device DeviceID.
type HAModeStep struct {
	Type          string   `json:"type"`
	EntityID      string   `json:"entityId,omitempty"`
	Service       string   `json:"service,omitempty"`
	BrightnessPct *int     `json:"brightnessPct,omitempty"`
	Volume        *float64 `json:"volume,omitempty"`
	Muted         *bool    `json:"muted,omitempty"`
	Source        string   `json:"source,omitempty"`
	Seconds       int      `json:"seconds,omitempty"`
	DeviceID      string   `json:"deviceId,omitempty"`
	ItemID        string   `json:"itemId,omitempty"`
}

// HAMode is a named list of steps: "Movie mode".
type HAMode struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Steps     []HAModeStep  `json:"steps"`
	Access    LibraryAccess `json:"access"`
	CreatedAt string        `json:"createdAt"`
	UpdatedAt string        `json:"updatedAt"`
}

var ErrHAModeNameUsed = errors.New("a mode with that name already exists")

type HomeAssistantRepo interface {
	HAConnection(ctx context.Context) (HAConnection, error)
	SaveHAConnection(ctx context.Context, c HAConnection) (HAConnection, error)
	DeleteHAConnection(ctx context.Context) error
	SetHAVersion(ctx context.Context, version string) error
	HAExposures(ctx context.Context) (map[string]LibraryAccess, error)
	ExposeHAEntity(ctx context.Context, entityID string, acc LibraryAccess) error
	HideHAEntity(ctx context.Context, entityID string) error
	HAModes(ctx context.Context) ([]HAMode, error)
	HAMode(ctx context.Context, id string) (HAMode, error)
	CreateHAMode(ctx context.Context, m HAMode) (HAMode, error)
	UpdateHAMode(ctx context.Context, m HAMode) (HAMode, error)
	DeleteHAMode(ctx context.Context, id string) error
	UserGroupIDs(ctx context.Context, userID string) ([]string, error)
	SessionByID(ctx context.Context, id string) (Session, error)
}

var _ HomeAssistantRepo = (*Store)(nil)

// HAConnection is the connection, or ErrNotFound when there is none.
func (s *Store) HAConnection(ctx context.Context) (HAConnection, error) {
	var c HAConnection
	err := s.db.QueryRowContext(ctx, `SELECT name, url, token, playback_events, version, created_at, updated_at
		FROM ha_connection WHERE id = 1`).Scan(&c.Name, &c.URL, &c.Token, &c.PlaybackEvents, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return HAConnection{}, ErrNotFound
	}
	if err != nil {
		return HAConnection{}, fmt.Errorf("home assistant connection: %w", err)
	}
	return c, nil
}

// SaveHAConnection creates the connection or replaces it.
func (s *Store) SaveHAConnection(ctx context.Context, c HAConnection) (HAConnection, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO ha_connection (id, name, url, token, playback_events, version)
		VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, url = excluded.url, token = excluded.token,
			playback_events = excluded.playback_events, version = excluded.version, updated_at = datetime('now')`,
		c.Name, c.URL, c.Token, c.PlaybackEvents, c.Version)
	if err != nil {
		return HAConnection{}, fmt.Errorf("save home assistant connection: %w", err)
	}
	return s.HAConnection(ctx)
}

// DeleteHAConnection removes the connection and nothing else: exposure,
// access, rooms and modes wait for it to come back.
func (s *Store) DeleteHAConnection(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ha_connection`); err != nil {
		return fmt.Errorf("delete home assistant connection: %w", err)
	}
	return nil
}

// SetHAVersion notes the instance's version, from its latest connect.
func (s *Store) SetHAVersion(ctx context.Context, version string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE ha_connection SET version = ? WHERE id = 1`, version); err != nil {
		return fmt.Errorf("set home assistant version: %w", err)
	}
	return nil
}

// haAccess reads the access lists of every target of one kind, keyed by
// target id, into the restricted flags already in acc.
func (s *Store) haAccess(ctx context.Context, kind string, acc map[string]LibraryAccess) error {
	rows, err := s.db.QueryContext(ctx, `SELECT target_id, kind, principal_id FROM ha_access
		WHERE target_kind = ? AND server_id = '' ORDER BY kind, principal_id`, kind)
	if err != nil {
		return fmt.Errorf("home assistant access: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var target, pkind, id string
		if err := rows.Scan(&target, &pkind, &id); err != nil {
			return fmt.Errorf("home assistant access: %w", err)
		}
		a, ok := acc[target]
		if !ok {
			continue
		}
		if pkind == "user" {
			a.UserIDs = append(a.UserIDs, id)
		} else {
			a.GroupIDs = append(a.GroupIDs, id)
		}
		acc[target] = a
	}
	return rows.Err()
}

// setHAAccess replaces a target's access list inside tx, checking every
// user and group exists, as SetLibraryAccess does.
func setHAAccess(ctx context.Context, tx *sql.Tx, kind, target string, acc LibraryAccess) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM ha_access WHERE target_kind = ? AND target_id = ? AND server_id = ''`, kind, target); err != nil {
		return fmt.Errorf("clear home assistant access: %w", err)
	}
	add := func(pkind, table string, ids []string) error {
		for _, id := range ids {
			var ok int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE id = ?`, id).Scan(&ok); err != nil {
				return fmt.Errorf("check %s %s: %w", pkind, id, err)
			}
			if ok == 0 {
				return fmt.Errorf("%w: no %s %s", ErrNotFound, pkind, id)
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO ha_access (target_kind, target_id, kind, principal_id)
				VALUES (?, ?, ?, ?)`, kind, target, pkind, id); err != nil {
				return fmt.Errorf("add %s %s: %w", pkind, id, err)
			}
		}
		return nil
	}
	if err := add("user", "users", acc.UserIDs); err != nil {
		return err
	}
	return add("group", "user_groups", acc.GroupIDs)
}

func emptyAccess(restricted bool) LibraryAccess {
	return LibraryAccess{Restricted: restricted, UserIDs: []string{}, GroupIDs: []string{}}
}

// HAExposures is every exposed entity with its access, keyed by entity id.
func (s *Store) HAExposures(ctx context.Context) (map[string]LibraryAccess, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT entity_id, restricted FROM ha_entities`)
	if err != nil {
		return nil, fmt.Errorf("exposed entities: %w", err)
	}
	out := map[string]LibraryAccess{}
	for rows.Next() {
		var id string
		var restricted bool
		if err := rows.Scan(&id, &restricted); err != nil {
			rows.Close()
			return nil, fmt.Errorf("exposed entities: %w", err)
		}
		out[id] = emptyAccess(restricted)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("exposed entities: %w", err)
	}
	if err := s.haAccess(ctx, "entity", out); err != nil {
		return nil, err
	}
	return out, nil
}

// ExposeHAEntity exposes an entity, or changes who may use it.
func (s *Store) ExposeHAEntity(ctx context.Context, entityID string, acc LibraryAccess) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("expose entity: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO ha_entities (entity_id, restricted) VALUES (?, ?)
		ON CONFLICT (entity_id) DO UPDATE SET restricted = excluded.restricted`, entityID, acc.Restricted); err != nil {
		return fmt.Errorf("expose entity: %w", err)
	}
	if err := setHAAccess(ctx, tx, "entity", entityID, acc); err != nil {
		return err
	}
	return tx.Commit()
}

// HideHAEntity stops exposing an entity; its access list goes with it.
func (s *Store) HideHAEntity(ctx context.Context, entityID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ha_entities WHERE entity_id = ?`, entityID); err != nil {
		return fmt.Errorf("hide entity: %w", err)
	}
	return nil
}

const haModeCols = `id, name, steps, restricted, created_at, updated_at`

func scanHAMode(row interface{ Scan(...any) error }) (HAMode, error) {
	var m HAMode
	var steps string
	if err := row.Scan(&m.ID, &m.Name, &steps, &m.Access.Restricted, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return m, err
	}
	m.Steps = []HAModeStep{}
	if err := json.Unmarshal([]byte(steps), &m.Steps); err != nil {
		return m, fmt.Errorf("steps of mode %s: %w", m.ID, err)
	}
	m.Access.UserIDs, m.Access.GroupIDs = []string{}, []string{}
	return m, nil
}

// HAModes lists every mode by name, with access.
func (s *Store) HAModes(ctx context.Context) ([]HAMode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+haModeCols+` FROM ha_modes ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("modes: %w", err)
	}
	out := []HAMode{}
	for rows.Next() {
		m, err := scanHAMode(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("modes: %w", err)
		}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("modes: %w", err)
	}
	acc := make(map[string]LibraryAccess, len(out))
	for _, m := range out {
		acc[m.ID] = m.Access
	}
	if err := s.haAccess(ctx, "mode", acc); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Access = acc[out[i].ID]
	}
	return out, nil
}

func (s *Store) HAMode(ctx context.Context, id string) (HAMode, error) {
	m, err := scanHAMode(s.db.QueryRowContext(ctx, `SELECT `+haModeCols+` FROM ha_modes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return HAMode{}, ErrNotFound
	}
	if err != nil {
		return HAMode{}, fmt.Errorf("mode %s: %w", id, err)
	}
	acc := map[string]LibraryAccess{id: m.Access}
	if err := s.haAccess(ctx, "mode", acc); err != nil {
		return HAMode{}, err
	}
	m.Access = acc[id]
	return m, nil
}

func (s *Store) CreateHAMode(ctx context.Context, m HAMode) (HAMode, error) {
	m.ID = uuid.NewString()
	return s.saveHAMode(ctx, m, true)
}

func (s *Store) UpdateHAMode(ctx context.Context, m HAMode) (HAMode, error) {
	return s.saveHAMode(ctx, m, false)
}

func (s *Store) saveHAMode(ctx context.Context, m HAMode, create bool) (HAMode, error) {
	if m.Steps == nil {
		m.Steps = []HAModeStep{}
	}
	steps, err := json.Marshal(m.Steps)
	if err != nil {
		return HAMode{}, fmt.Errorf("encode steps: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HAMode{}, fmt.Errorf("save mode: %w", err)
	}
	defer tx.Rollback()
	if create {
		_, err = tx.ExecContext(ctx, `INSERT INTO ha_modes (id, name, steps, restricted) VALUES (?, ?, ?, ?)`,
			m.ID, m.Name, string(steps), m.Access.Restricted)
	} else {
		var res sql.Result
		res, err = tx.ExecContext(ctx, `UPDATE ha_modes SET name = ?, steps = ?, restricted = ?, updated_at = datetime('now')
			WHERE id = ?`, m.Name, string(steps), m.Access.Restricted, m.ID)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return HAMode{}, ErrNotFound
			}
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return HAMode{}, ErrHAModeNameUsed
		}
		return HAMode{}, fmt.Errorf("save mode: %w", err)
	}
	if err := setHAAccess(ctx, tx, "mode", m.ID, m.Access); err != nil {
		return HAMode{}, err
	}
	if err := tx.Commit(); err != nil {
		return HAMode{}, fmt.Errorf("save mode: %w", err)
	}
	return s.HAMode(ctx, m.ID)
}

func (s *Store) DeleteHAMode(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ha_modes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete mode: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UserGroupIDs lists the groups a user belongs to.
func (s *Store) UserGroupIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT group_id FROM user_group_members WHERE user_id = ? ORDER BY group_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("user groups: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("user groups: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SessionByID resolves a live session by its public id, whoever owns it.
func (s *Store) SessionByID(ctx context.Context, id string) (Session, error) {
	sess, err := scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM `+sessionFrom+`
		WHERE s.id = ? AND s.expires_at > datetime('now')`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("session %s: %w", id, err)
	}
	return sess, nil
}
