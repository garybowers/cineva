package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ArrConnection is a connected Sonarr or Radarr instance.
type ArrConnection struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // "sonarr" | "radarr"
	Name string `json:"name"`
	URL  string `json:"url"`
	// APIKey is the instance's key. Never sent to clients.
	APIKey string `json:"-"`
	// CallbackURL is Cineva's address as the instance reaches it — often not
	// the one a browser uses, when Sonarr runs in a container.
	CallbackURL string `json:"callbackUrl"`
	// SecretHash is the hash of the webhook's password.
	SecretHash     string `json:"-"`
	NotificationID int    `json:"notificationId"`
	Version        string `json:"version"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type ArrRepo interface {
	ArrConnections(ctx context.Context) ([]ArrConnection, error)
	ArrConnection(ctx context.Context, id string) (ArrConnection, error)
	CreateArrConnection(ctx context.Context, c ArrConnection) (ArrConnection, error)
	UpdateArrConnection(ctx context.Context, c ArrConnection) (ArrConnection, error)
	DeleteArrConnection(ctx context.Context, id string) error
}

const arrCols = `id, kind, name, url, api_key, callback_url, secret_hash,
	notification_id, version, created_at, updated_at`

func scanArr(row interface{ Scan(...any) error }) (ArrConnection, error) {
	var c ArrConnection
	err := row.Scan(&c.ID, &c.Kind, &c.Name, &c.URL, &c.APIKey, &c.CallbackURL, &c.SecretHash,
		&c.NotificationID, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (s *Store) ArrConnections(ctx context.Context) ([]ArrConnection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+arrCols+` FROM arr_connections ORDER BY kind, name`)
	if err != nil {
		return nil, fmt.Errorf("arr connections: %w", err)
	}
	defer rows.Close()
	out := []ArrConnection{}
	for rows.Next() {
		c, err := scanArr(rows)
		if err != nil {
			return nil, fmt.Errorf("scan arr connection: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ArrConnection(ctx context.Context, id string) (ArrConnection, error) {
	c, err := scanArr(s.db.QueryRowContext(ctx, `SELECT `+arrCols+` FROM arr_connections WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ArrConnection{}, ErrNotFound
	}
	if err != nil {
		return ArrConnection{}, fmt.Errorf("arr connection %s: %w", id, err)
	}
	return c, nil
}

func (s *Store) CreateArrConnection(ctx context.Context, c ArrConnection) (ArrConnection, error) {
	if c.Kind != "sonarr" && c.Kind != "radarr" {
		return ArrConnection{}, fmt.Errorf("unknown kind %q: want sonarr or radarr", c.Kind)
	}
	c.ID = uuid.NewString()
	_, err := s.db.ExecContext(ctx, `INSERT INTO arr_connections
		(id, kind, name, url, api_key, callback_url, secret_hash, notification_id, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Kind, c.Name, c.URL, c.APIKey, c.CallbackURL, c.SecretHash, c.NotificationID, c.Version)
	if err != nil {
		return ArrConnection{}, fmt.Errorf("create arr connection: %w", err)
	}
	return s.ArrConnection(ctx, c.ID)
}

// UpdateArrConnection rewrites everything but the kind, which is fixed at
// creation like a library's media type.
func (s *Store) UpdateArrConnection(ctx context.Context, c ArrConnection) (ArrConnection, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE arr_connections SET
		name = ?, url = ?, api_key = ?, callback_url = ?, secret_hash = ?,
		notification_id = ?, version = ?, updated_at = datetime('now')
		WHERE id = ?`,
		c.Name, c.URL, c.APIKey, c.CallbackURL, c.SecretHash, c.NotificationID, c.Version, c.ID)
	if err != nil {
		return ArrConnection{}, fmt.Errorf("update arr connection: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return ArrConnection{}, fmt.Errorf("update arr connection: %w", err)
	} else if n == 0 {
		return ArrConnection{}, ErrNotFound
	}
	return s.ArrConnection(ctx, c.ID)
}

func (s *Store) DeleteArrConnection(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM arr_connections WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete arr connection: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("delete arr connection: %w", err)
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}
