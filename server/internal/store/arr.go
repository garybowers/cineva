package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ArrConnection is a connected Sonarr or Radarr instance.
type ArrConnection struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // "sonarr" | "radarr" | "lidarr"
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

	// QualityProfileID and RootFolder are what requests sent to this
	// instance are made with, chosen from the instance's own lists; 0 and
	// "" until an admin picks, when the instance's first is used.
	QualityProfileID int    `json:"qualityProfileId"`
	RootFolder       string `json:"rootFolder"`
	// PathMaps translate the instance's paths into Cineva's, for an
	// instance that sees the media through other mount points.
	PathMaps []PathMap `json:"pathMaps"`
	// LastEvent is the type of the last webhook delivery ("Download"),
	// LastEventTitle what it was about ("Show S02E04"), LastEventAt when.
	LastEvent      string `json:"lastEvent,omitempty"`
	LastEventTitle string `json:"lastEventTitle,omitempty"`
	LastEventAt    string `json:"lastEventAt,omitempty"`
}

// PathMap is one translation from an instance's path prefix to Cineva's:
// Sonarr in a container sees /tv where Cineva sees /mnt/media/TV.
type PathMap struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type ArrRepo interface {
	ArrConnections(ctx context.Context) ([]ArrConnection, error)
	ArrConnection(ctx context.Context, id string) (ArrConnection, error)
	CreateArrConnection(ctx context.Context, c ArrConnection) (ArrConnection, error)
	UpdateArrConnection(ctx context.Context, c ArrConnection) (ArrConnection, error)
	DeleteArrConnection(ctx context.Context, id string) error
	SetArrOptions(ctx context.Context, id string, qualityProfileID int, rootFolder string, maps []PathMap) error
	RecordArrEvent(ctx context.Context, id, event, title string) error
}

const arrCols = `id, kind, name, url, api_key, callback_url, secret_hash,
	notification_id, version, created_at, updated_at,
	quality_profile_id, root_folder, path_maps, last_event, last_event_title, last_event_at`

func scanArr(row interface{ Scan(...any) error }) (ArrConnection, error) {
	var c ArrConnection
	var maps string
	err := row.Scan(&c.ID, &c.Kind, &c.Name, &c.URL, &c.APIKey, &c.CallbackURL, &c.SecretHash,
		&c.NotificationID, &c.Version, &c.CreatedAt, &c.UpdatedAt,
		&c.QualityProfileID, &c.RootFolder, &maps, &c.LastEvent, &c.LastEventTitle, &c.LastEventAt)
	if err != nil {
		return c, err
	}
	c.PathMaps = []PathMap{}
	if maps != "" {
		if err := json.Unmarshal([]byte(maps), &c.PathMaps); err != nil {
			return c, fmt.Errorf("path maps of %s: %w", c.ID, err)
		}
	}
	return c, nil
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
	if c.Kind != "sonarr" && c.Kind != "radarr" && c.Kind != "lidarr" {
		return ArrConnection{}, fmt.Errorf("unknown kind %q: want sonarr, radarr or lidarr", c.Kind)
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

// SetArrOptions saves what requests are made with and how the instance's
// paths map onto Cineva's. Nothing here is sent to the instance, so it is
// kept apart from UpdateArrConnection, which re-registers the webhook.
func (s *Store) SetArrOptions(ctx context.Context, id string, qualityProfileID int, rootFolder string, maps []PathMap) error {
	if maps == nil {
		maps = []PathMap{}
	}
	raw, err := json.Marshal(maps)
	if err != nil {
		return fmt.Errorf("encode path maps: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE arr_connections SET
		quality_profile_id = ?, root_folder = ?, path_maps = ?, updated_at = datetime('now')
		WHERE id = ?`, qualityProfileID, rootFolder, string(raw), id)
	if err != nil {
		return fmt.Errorf("set arr options: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("set arr options: %w", err)
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordArrEvent notes the latest webhook delivery from an instance.
func (s *Store) RecordArrEvent(ctx context.Context, id, event, title string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE arr_connections SET
		last_event = ?, last_event_title = ?, last_event_at = datetime('now') WHERE id = ?`,
		event, title, id); err != nil {
		return fmt.Errorf("record arr event: %w", err)
	}
	return nil
}
