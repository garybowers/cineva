package store

import (
	"context"
	"fmt"
)

// Prefs returns every preference this user has set, as a plain map. Absent
// keys simply are not present — the client supplies its own defaults, which
// keeps the server from having an opinion about UI it does not render.
func (s *Store) Prefs(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value FROM user_prefs WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("user prefs: %w", err)
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

// SetPrefs merges the given preferences into the user's set. An empty value
// deletes the key, so a client can clear one without a second endpoint.
func (s *Store) SetPrefs(ctx context.Context, userID string, prefs map[string]string) error {
	if len(prefs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set prefs: %w", err)
	}
	defer tx.Rollback()
	for k, v := range prefs {
		if v == "" {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM user_prefs WHERE user_id = ? AND key = ?`, userID, k); err != nil {
				return fmt.Errorf("clear pref %q: %w", k, err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_prefs (user_id, key, value) VALUES (?, ?, ?)
			ON CONFLICT (user_id, key) DO UPDATE SET
				value = excluded.value, updated_at = datetime('now')`,
			userID, k, v); err != nil {
			return fmt.Errorf("set pref %q: %w", k, err)
		}
	}
	return tx.Commit()
}
