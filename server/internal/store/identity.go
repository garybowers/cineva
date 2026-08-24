package store

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
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
