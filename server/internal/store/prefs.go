package store

import (
	"context"
	"fmt"
)

// Preferences are stored per layer as plain strings: the user's own
// (user_prefs), a device's (device_prefs), and the admin's defaults for the
// server and for each group (pref_defaults). What a key means, and which
// layer wins, is internal/prefs's business; the store only keeps values.

// Owners of an admin default.
const (
	PrefOwnerServer = "server"
	PrefOwnerGroup  = "group"
)

// PrefDefault is one admin default: the server's (OwnerID "") or a group's.
type PrefDefault struct {
	OwnerKind string
	OwnerID   string
	Key       string
	Value     string
	Locked    bool
}

// PrefSetting is an admin default as written: a value, and whether it is
// locked.
type PrefSetting struct {
	Value  string `json:"value"`
	Locked bool   `json:"locked"`
}

type PrefRepo interface {
	Prefs(ctx context.Context, userID string) (map[string]string, error)
	SetPrefs(ctx context.Context, userID string, prefs map[string]*string) error
	DevicePrefs(ctx context.Context, deviceID string) (map[string]string, error)
	SetDevicePrefs(ctx context.Context, deviceID string, prefs map[string]*string) error
	PrefDefaults(ctx context.Context) ([]PrefDefault, error)
	SetPrefDefaults(ctx context.Context, ownerKind, ownerID string, prefs map[string]*PrefSetting) error
	GroupsOf(ctx context.Context, userID string) ([]UserGroup, error)
}

var _ PrefRepo = (*Store)(nil)

// Prefs returns every preference this user has set, as a plain map. Absent
// keys simply are not present: they fall through to the layers below.
func (s *Store) Prefs(ctx context.Context, userID string) (map[string]string, error) {
	return s.prefMap(ctx, `SELECT key, value FROM user_prefs WHERE user_id = ?`, userID)
}

// SetPrefs merges the given preferences into the user's set. A nil value
// deletes the key, handing it back to the layers below; an empty string is
// a value like any other ("the file's own audio track").
func (s *Store) SetPrefs(ctx context.Context, userID string, prefs map[string]*string) error {
	return s.setPrefMap(ctx, prefs,
		`DELETE FROM user_prefs WHERE user_id = ? AND key = ?`,
		`INSERT INTO user_prefs (user_id, key, value) VALUES (?, ?, ?)
		 ON CONFLICT (user_id, key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')`,
		userID)
}

// DevicePrefs is a device's own values, by its session's public id.
func (s *Store) DevicePrefs(ctx context.Context, deviceID string) (map[string]string, error) {
	return s.prefMap(ctx, `SELECT key, value FROM device_prefs WHERE device_id = ?`, deviceID)
}

// SetDevicePrefs merges values into a device's set; nil deletes a key.
func (s *Store) SetDevicePrefs(ctx context.Context, deviceID string, prefs map[string]*string) error {
	return s.setPrefMap(ctx, prefs,
		`DELETE FROM device_prefs WHERE device_id = ? AND key = ?`,
		`INSERT INTO device_prefs (device_id, key, value) VALUES (?, ?, ?)
		 ON CONFLICT (device_id, key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')`,
		deviceID)
}

func (s *Store) prefMap(ctx context.Context, query, owner string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, query, owner)
	if err != nil {
		return nil, fmt.Errorf("prefs: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan pref: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) setPrefMap(ctx context.Context, prefs map[string]*string, del, upsert, owner string) error {
	if len(prefs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set prefs: %w", err)
	}
	defer tx.Rollback()
	for k, v := range prefs {
		if v == nil {
			if _, err := tx.ExecContext(ctx, del, owner, k); err != nil {
				return fmt.Errorf("clear pref %q: %w", k, err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, upsert, owner, k, *v); err != nil {
			return fmt.Errorf("set pref %q: %w", k, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set prefs: %w", err)
	}
	return nil
}

// PrefDefaults is every admin default: the server's and every group's.
// There are few enough (a handful of keys times a handful of groups) that
// reading them all is simpler than asking per user.
func (s *Store) PrefDefaults(ctx context.Context) ([]PrefDefault, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT owner_kind, owner_id, key, value, locked FROM pref_defaults ORDER BY owner_kind, owner_id, key`)
	if err != nil {
		return nil, fmt.Errorf("pref defaults: %w", err)
	}
	defer rows.Close()
	out := []PrefDefault{}
	for rows.Next() {
		var d PrefDefault
		if err := rows.Scan(&d.OwnerKind, &d.OwnerID, &d.Key, &d.Value, &d.Locked); err != nil {
			return nil, fmt.Errorf("scan pref default: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetPrefDefaults merges defaults into the server's set (ownerID "") or a
// group's; nil deletes a key. A group that does not exist is ErrNotFound.
func (s *Store) SetPrefDefaults(ctx context.Context, ownerKind, ownerID string, prefs map[string]*PrefSetting) error {
	switch ownerKind {
	case PrefOwnerServer:
		ownerID = ""
	case PrefOwnerGroup:
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_groups WHERE id = ?`, ownerID).Scan(&n); err != nil {
			return fmt.Errorf("check group %s: %w", ownerID, err)
		}
		if n == 0 {
			return ErrNotFound
		}
	default:
		return fmt.Errorf("unknown preference owner %q", ownerKind)
	}
	if len(prefs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set pref defaults: %w", err)
	}
	defer tx.Rollback()
	for k, v := range prefs {
		var err error
		if v == nil {
			_, err = tx.ExecContext(ctx,
				`DELETE FROM pref_defaults WHERE owner_kind = ? AND owner_id = ? AND key = ?`, ownerKind, ownerID, k)
		} else {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO pref_defaults (owner_kind, owner_id, key, value, locked) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (owner_kind, owner_id, key) DO UPDATE SET
					value = excluded.value, locked = excluded.locked, updated_at = datetime('now')`,
				ownerKind, ownerID, k, v.Value, v.Locked)
		}
		if err != nil {
			return fmt.Errorf("set pref default %q: %w", k, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set pref defaults: %w", err)
	}
	return nil
}

// GroupsOf is the groups a user belongs to, in the admin's priority order
// (the first wins where two set the same default). Members are not filled
// in: the caller wants the user's groups, not everyone in them.
func (s *Store) GroupsOf(ctx context.Context, userID string) ([]UserGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.name, g.priority, g.created_at FROM user_groups g
		JOIN user_group_members m ON m.group_id = g.id
		WHERE m.user_id = ?
		ORDER BY g.priority, g.name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, fmt.Errorf("groups of %s: %w", userID, err)
	}
	defer rows.Close()
	out := []UserGroup{}
	for rows.Next() {
		var g UserGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Priority, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("groups of %s: %w", userID, err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
