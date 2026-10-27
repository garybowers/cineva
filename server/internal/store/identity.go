package store

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"

	"github.com/garybowers/cineva/server/internal/auth"
)

// Identity is this server's federation keypair (see CLAUDE.md: Federation).
type Identity struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// EnsureIdentity returns the server's identity keypair, generating and
// persisting one on first startup.
func (s *Store) EnsureIdentity(ctx context.Context) (Identity, error) {
	var pub, priv []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT public_key, private_key FROM server_identity WHERE id = 1`).Scan(&pub, &priv)
	switch {
	case err == nil:
		return Identity{PublicKey: pub, PrivateKey: priv}, nil
	case errors.Is(err, sql.ErrNoRows):
		newPub, newPriv, err := ed25519.GenerateKey(nil)
		if err != nil {
			return Identity{}, fmt.Errorf("generate identity: %w", err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO server_identity (id, public_key, private_key) VALUES (1, ?, ?)`,
			[]byte(newPub), []byte(newPriv)); err != nil {
			return Identity{}, fmt.Errorf("persist identity: %w", err)
		}
		return Identity{PublicKey: newPub, PrivateKey: newPriv}, nil
	default:
		return Identity{}, fmt.Errorf("load identity: %w", err)
	}
}

// SecretsKey is the server's sealing key (see auth.Seal), made on first
// use and kept beside the identity. Never rotated: what it sealed would be
// unreadable.
func (s *Store) SecretsKey(ctx context.Context) ([]byte, error) {
	var key []byte
	err := s.db.QueryRowContext(ctx, `SELECT secrets_key FROM server_identity WHERE id = 1`).Scan(&key)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("secrets key: %w", err)
	}
	if len(key) > 0 {
		return key, nil
	}
	if _, err := s.EnsureIdentity(ctx); err != nil {
		return nil, err
	}
	fresh, err := auth.NewSecretKey()
	if err != nil {
		return nil, err
	}
	// Only the first writer wins; a second server start racing this one
	// reads the stored key back.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE server_identity SET secrets_key = ? WHERE id = 1 AND secrets_key IS NULL`, fresh); err != nil {
		return nil, fmt.Errorf("secrets key: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT secrets_key FROM server_identity WHERE id = 1`).Scan(&key); err != nil {
		return nil, fmt.Errorf("secrets key: %w", err)
	}
	return key, nil
}
