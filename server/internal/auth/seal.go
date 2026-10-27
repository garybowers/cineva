package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// A secret the server must be able to read back (an API key a music app
// signs in with by salted token, see server/docs/subsonic.md) is sealed
// with AES-256-GCM under a key made once per server and kept beside its
// identity. Sealing is not a defence against someone holding the database
// and the key; it keeps the secret out of plain sight in backups and logs,
// and is what the design settles on rather than storing it in clear.

// SecretKeySize is the AES-256 key length.
const SecretKeySize = 32

// NewSecretKey makes a sealing key.
func NewSecretKey() ([]byte, error) {
	key := make([]byte, SecretKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("secret key: %w", err)
	}
	return key, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	if len(key) != SecretKeySize {
		return nil, errors.New("seal: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext under key; the nonce leads the result.
func Seal(key []byte, plaintext string) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	return g.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Open decrypts what Seal produced.
func Open(key, sealed []byte) (string, error) {
	g, err := gcm(key)
	if err != nil {
		return "", err
	}
	if len(sealed) < g.NonceSize() {
		return "", errors.New("open: sealed value too short")
	}
	out, err := g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	return string(out), nil
}
