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

// CustomTheme is a theme someone made in Settings (migration 0044).
type CustomTheme struct {
	ID         string            `json:"id"`
	OwnerID    string            `json:"ownerId"`
	Name       string            `json:"name"`
	Appearance string            `json:"appearance"`
	Tokens     map[string]string `json:"tokens"`
	Background string            `json:"background"`
	Dim        int               `json:"dim"`
	Effect     string            `json:"effect"`
	Household  bool              `json:"household"`
	CreatedAt  string            `json:"createdAt"`
	UpdatedAt  string            `json:"updatedAt"`
}

// CustomThemePrefix starts every custom theme's id, keeping them apart from
// the built-in themes' folder names.
const CustomThemePrefix = "c-"

const customThemeCols = `id, owner_id, name, appearance, tokens, background, dim, effect, household, created_at, updated_at`

func scanCustomTheme(row interface{ Scan(...any) error }) (CustomTheme, error) {
	var t CustomTheme
	var tokens string
	if err := row.Scan(&t.ID, &t.OwnerID, &t.Name, &t.Appearance, &tokens, &t.Background, &t.Dim, &t.Effect, &t.Household, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return t, err
	}
	if err := json.Unmarshal([]byte(tokens), &t.Tokens); err != nil {
		return t, fmt.Errorf("theme %s tokens: %w", t.ID, err)
	}
	return t, nil
}

// CustomThemes is what a user may pick: their own themes and everyone's
// shared ones. userID "" gives the shared ones only.
func (s *Store) CustomThemes(ctx context.Context, userID string) ([]CustomTheme, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+customThemeCols+` FROM custom_themes WHERE household = 1 OR owner_id = ? ORDER BY name COLLATE NOCASE, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list themes: %w", err)
	}
	defer rows.Close()
	out := []CustomTheme{}
	for rows.Next() {
		t, err := scanCustomTheme(rows)
		if err != nil {
			return nil, fmt.Errorf("scan theme: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CustomTheme is one theme by id, whoever owns it.
func (s *Store) CustomTheme(ctx context.Context, id string) (CustomTheme, error) {
	t, err := scanCustomTheme(s.db.QueryRowContext(ctx, `SELECT `+customThemeCols+` FROM custom_themes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, fmt.Errorf("theme %s: %w", id, err)
	}
	return t, nil
}

// SaveCustomTheme creates a theme (when t.ID is "") or replaces one's
// name, appearance, tokens, dim, effect and sharing. The background is
// set apart, by SetCustomThemeBackground.
func (s *Store) SaveCustomTheme(ctx context.Context, t CustomTheme) (CustomTheme, error) {
	t.Name = strings.TrimSpace(t.Name)
	tokens, err := json.Marshal(t.Tokens)
	if err != nil {
		return t, fmt.Errorf("encode tokens: %w", err)
	}
	if t.ID == "" {
		t.ID = CustomThemePrefix + uuid.NewString()
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO custom_themes (id, owner_id, name, appearance, tokens, dim, effect, household) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.OwnerID, t.Name, t.Appearance, string(tokens), t.Dim, t.Effect, t.Household)
	} else {
		var res sql.Result
		res, err = s.db.ExecContext(ctx,
			`UPDATE custom_themes SET name = ?, appearance = ?, tokens = ?, dim = ?, effect = ?, household = ?, updated_at = datetime('now') WHERE id = ?`,
			t.Name, t.Appearance, string(tokens), t.Dim, t.Effect, t.Household, t.ID)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return t, ErrNotFound
			}
		}
	}
	if err != nil {
		return t, fmt.Errorf("save theme: %w", err)
	}
	return s.CustomTheme(ctx, t.ID)
}

// SetCustomThemeBackground records a theme's background picture, an
// /artwork/ path ("" for none).
func (s *Store) SetCustomThemeBackground(ctx context.Context, id, path string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE custom_themes SET background = ?, updated_at = datetime('now') WHERE id = ?`, path, id)
	if err != nil {
		return fmt.Errorf("theme background: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCustomTheme removes a theme. Anyone who had chosen it falls back
// to the default, as for a theme folder that has gone.
func (s *Store) DeleteCustomTheme(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM custom_themes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete theme: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
