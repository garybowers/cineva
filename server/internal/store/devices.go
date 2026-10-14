package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Device is a screen: a browser profile, later a native app instance. It
// outlives the sessions signed in on it and holds one at a time, which
// shares its id (see docs/devices.md).
type Device struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Client     string `json:"client"`
	ClientIP   string `json:"clientIp"`
	AreaID     string `json:"areaId"`
	CreatedAt  string `json:"createdAt"`
	LastSeenAt string `json:"lastSeenAt"`
	// SecretHash is the hash of its cookie's secret; '' until it has one.
	SecretHash string `json:"-"`
	// UserID is whoever is signed in on it now; '' for nobody.
	UserID string `json:"userId"`
	// AutoUserID is who it signs in as whenever nobody is signed in on it;
	// '' for a device that shows the sign-in screen.
	AutoUserID string `json:"autoUserId"`
}

// DeviceUnusedDays is how long a device nobody is signed in on is kept.
const DeviceUnusedDays = 90

type DeviceRepo interface {
	CreateDevice(ctx context.Context, secretHash, client, ip string) (Device, error)
	Device(ctx context.Context, id string) (Device, error)
	DeviceBySecret(ctx context.Context, secretHash string) (Device, error)
	SetDeviceSecret(ctx context.Context, id, secretHash string) error
	SetDeviceArea(ctx context.Context, id, areaID string) error
	SetDeviceName(ctx context.Context, id, name string) error
	SetDeviceAutoUser(ctx context.Context, id, userID string) error
	Devices(ctx context.Context) ([]Device, error)
	DeleteDevice(ctx context.Context, id string) error
	PruneDevices(ctx context.Context) (int, error)
}

var _ DeviceRepo = (*Store)(nil)

// deviceCols reads a device with whoever is signed in on it now, if
// anyone: a live session shares the device's id.
const deviceCols = `d.id, d.name, d.client, d.client_ip, d.ha_area, d.created_at, d.last_seen_at, d.secret_hash,
	COALESCE((SELECT s.user_id FROM sessions s WHERE s.id = d.id AND s.expires_at > datetime('now')), ''), d.auto_user_id`

func scanDevice(row interface{ Scan(...any) error }) (Device, error) {
	var d Device
	err := row.Scan(&d.ID, &d.Name, &d.Client, &d.ClientIP, &d.AreaID, &d.CreatedAt, &d.LastSeenAt, &d.SecretHash, &d.UserID, &d.AutoUserID)
	return d, err
}

// CreateDevice records a new screen.
func (s *Store) CreateDevice(ctx context.Context, secretHash, client, ip string) (Device, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return Device{}, fmt.Errorf("device id: %w", err)
	}
	id := hex.EncodeToString(b)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO devices (id, secret_hash, client, client_ip) VALUES (?, ?, ?, ?)`,
		id, secretHash, client, ip); err != nil {
		return Device{}, fmt.Errorf("create device: %w", err)
	}
	return s.Device(ctx, id)
}

// Device is one device by id, signed in on or not.
func (s *Store) Device(ctx context.Context, id string) (Device, error) {
	d, err := scanDevice(s.db.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices d WHERE d.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("device %s: %w", id, err)
	}
	return d, nil
}

// DeviceBySecret finds the device a cookie names.
func (s *Store) DeviceBySecret(ctx context.Context, secretHash string) (Device, error) {
	if secretHash == "" {
		return Device{}, ErrNotFound
	}
	d, err := scanDevice(s.db.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices d WHERE d.secret_hash = ?`, secretHash))
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("device by secret: %w", err)
	}
	return d, nil
}

// SetDeviceSecret gives a device its cookie's secret, as a hash: a device
// from before devices existed gets one on its next request.
func (s *Store) SetDeviceSecret(ctx context.Context, id, secretHash string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE devices SET secret_hash = ? WHERE id = ?`, secretHash, id); err != nil {
		return fmt.Errorf("set device secret: %w", err)
	}
	return nil
}

// SetDeviceArea puts a device in a Home Assistant area ("" for none).
func (s *Store) SetDeviceArea(ctx context.Context, id, areaID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE devices SET ha_area = ? WHERE id = ?`, areaID, id)
	if err != nil {
		return fmt.Errorf("set device room: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDeviceName names a device ("" for its browser's name).
func (s *Store) SetDeviceName(ctx context.Context, id, name string) error {
	return s.setDeviceField(ctx, id, "name", strings.TrimSpace(name))
}

// SetDeviceAutoUser sets who a device signs in as when nobody is signed in
// on it ("" for nobody). Whether that user may be chosen is the caller's
// to check.
func (s *Store) SetDeviceAutoUser(ctx context.Context, id, userID string) error {
	return s.setDeviceField(ctx, id, "auto_user_id", userID)
}

func (s *Store) setDeviceField(ctx context.Context, id, column, value string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE devices SET `+column+` = ? WHERE id = ?`, value, id)
	if err != nil {
		return fmt.Errorf("set device %s: %w", column, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Devices lists every device, most recently seen first.
func (s *Store) Devices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deviceCols+` FROM devices d ORDER BY d.last_seen_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("devices: %w", err)
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("devices: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDevice forgets a device: its name, room and preferences go, and
// whoever is signed in on it is signed out.
func (s *Store) DeleteDevice(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("forget device: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneDevices forgets devices nobody is signed in on that have not been
// used for DeviceUnusedDays, so a laptop sold years ago does not linger.
func (s *Store) PruneDevices(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM devices
		WHERE last_seen_at < datetime('now', ?)
		  AND NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id = devices.id)`,
		fmt.Sprintf("-%d days", DeviceUnusedDays))
	if err != nil {
		return 0, fmt.Errorf("prune devices: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}
