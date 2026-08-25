package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User is an account. PasswordHash never leaves the store package's
// callers unhashed and is omitted from JSON.
type User struct {
	ID           string   `json:"id"`
	Username     string   `json:"username"`
	DisplayName  string   `json:"displayName"`
	PasswordHash string   `json:"-"`
	Role         string   `json:"role"`
	Disabled     bool     `json:"disabled"`
	CreatedAt    string   `json:"createdAt"`
	LibraryIDs   []string `json:"libraryIds"` // grants; admins: every library (informational)
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// Session is a browser login. Token is never stored — only its hash.
type Session struct {
	TokenHash  string `json:"-"`
	UserID     string `json:"userId"`
	CreatedAt  string `json:"createdAt"`
	ExpiresAt  string `json:"expiresAt"`
	LastSeenAt string `json:"lastSeenAt"`
	Client     string `json:"client"`
	ClientIP   string `json:"clientIp"`
}

type APIKey struct {
	ID         string `json:"id"`
	UserID     string `json:"userId"`
	Name       string `json:"name"`
	KeyHash    string `json:"-"`
	CreatedAt  string `json:"createdAt"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
}

var (
	ErrNotFound     = errors.New("not found")
	ErrUsernameUsed = errors.New("username already taken")
)

type UserRepo interface {
	CreateUser(ctx context.Context, username, displayName, passwordHash, role string) (User, error)
	UserByID(ctx context.Context, id string) (User, error)
	UserByUsername(ctx context.Context, username string) (User, error)
	Users(ctx context.Context) ([]User, error)
	CountUsers(ctx context.Context) (int, error)
	UpdateUser(ctx context.Context, id, displayName, role string, disabled bool) error
	SetPasswordHash(ctx context.Context, id, hash string) error
	DeleteUser(ctx context.Context, id string) error
	SetLibraryGrants(ctx context.Context, userID string, libraryIDs []string) error
	AdoptLocalState(ctx context.Context, userID string) error

	CreateSession(ctx context.Context, tokenHash, userID, expiresAt, client, ip string) error
	SessionByHash(ctx context.Context, tokenHash string) (Session, User, error)
	TouchSession(ctx context.Context, tokenHash string) error
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteUserSessions(ctx context.Context, userID string) error

	CreateAPIKey(ctx context.Context, userID, name, keyHash string) (APIKey, error)
	APIKeyByHash(ctx context.Context, keyHash string) (APIKey, User, error)
	APIKeys(ctx context.Context, userID string) ([]APIKey, error)
	DeleteAPIKey(ctx context.Context, userID, id string) error
}

var _ UserRepo = (*Store)(nil)

const userCols = `id, username, display_name, password_hash, role, disabled, created_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var disabled int
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.Role, &disabled, &u.CreatedAt)
	u.Disabled = disabled == 1
	u.LibraryIDs = []string{}
	return u, err
}

func (s *Store) CreateUser(ctx context.Context, username, displayName, passwordHash, role string) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return User{}, fmt.Errorf("username is required")
	}
	if role != RoleAdmin && role != RoleUser {
		return User{}, fmt.Errorf("invalid role %q", role)
	}
	if displayName == "" {
		displayName = username
	}
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx, `INSERT INTO users (id, username, display_name, password_hash, role)
		VALUES (?, ?, ?, ?, ?)`, id, username, displayName, passwordHash, role)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, ErrUsernameUsed
		}
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return s.UserByID(ctx, id)
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("user %s: %w", id, err)
	}
	u.LibraryIDs, err = s.grantsFor(ctx, id)
	return u, err
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ?`, strings.TrimSpace(username)))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("user %q: %w", username, err)
	}
	u.LibraryIDs, err = s.grantsFor(ctx, u.ID)
	return u, err
}

func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("users: %w", err)
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("user row: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].LibraryIDs, err = s.grantsFor(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

func (s *Store) UpdateUser(ctx context.Context, id, displayName, role string, disabled bool) error {
	if role != RoleAdmin && role != RoleUser {
		return fmt.Errorf("invalid role %q", role)
	}
	d := 0
	if disabled {
		d = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET display_name = ?, role = ?, disabled = ? WHERE id = ?`,
		displayName, role, d, id)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetPasswordHash(ctx context.Context, id, hash string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) grantsFor(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT library_id FROM library_grants WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("grants: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetLibraryGrants replaces a user's library grants wholesale.
func (s *Store) SetLibraryGrants(ctx context.Context, userID string, libraryIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("grants tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_grants WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("clear grants: %w", err)
	}
	for _, lib := range libraryIDs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO library_grants (user_id, library_id) VALUES (?, ?)`, userID, lib); err != nil {
			return fmt.Errorf("grant %s: %w", lib, err)
		}
	}
	return tx.Commit()
}

// AdoptLocalState hands everything recorded under the pre-auth placeholder
// user to a real account — run once, for the first admin.
func (s *Store) AdoptLocalState(ctx context.Context, userID string) error {
	for _, table := range []string{"play_state", "play_events", "audit_log"} {
		if _, err := s.db.ExecContext(ctx, `UPDATE `+table+` SET user_id = ? WHERE user_id = ?`, userID, LocalUser); err != nil {
			return fmt.Errorf("adopt %s: %w", table, err)
		}
	}
	return nil
}

// ---- sessions ----

func (s *Store) CreateSession(ctx context.Context, tokenHash, userID, expiresAt, client, ip string) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at, client, client_ip)
		VALUES (?, ?, ?, ?, ?)`, tokenHash, userID, expiresAt, client, ip); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// SessionByHash resolves a live (unexpired) session and its user.
func (s *Store) SessionByHash(ctx context.Context, tokenHash string) (Session, User, error) {
	var sess Session
	err := s.db.QueryRowContext(ctx, `SELECT token_hash, user_id, created_at, expires_at, last_seen_at, client, client_ip
		FROM sessions WHERE token_hash = ? AND expires_at > datetime('now')`, tokenHash).
		Scan(&sess.TokenHash, &sess.UserID, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt, &sess.Client, &sess.ClientIP)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, User{}, ErrNotFound
	}
	if err != nil {
		return Session{}, User{}, fmt.Errorf("session: %w", err)
	}
	u, err := s.UserByID(ctx, sess.UserID)
	return sess, u, err
}

func (s *Store) TouchSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = datetime('now') WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// ---- API keys ----

func (s *Store) CreateAPIKey(ctx context.Context, userID, name, keyHash string) (APIKey, error) {
	k := APIKey{ID: uuid.NewString(), UserID: userID, Name: strings.TrimSpace(name), KeyHash: keyHash}
	if k.Name == "" {
		k.Name = "API key"
	}
	if err := s.db.QueryRowContext(ctx, `INSERT INTO api_keys (id, user_id, name, key_hash) VALUES (?, ?, ?, ?)
		RETURNING created_at`, k.ID, k.UserID, k.Name, k.KeyHash).Scan(&k.CreatedAt); err != nil {
		return APIKey{}, fmt.Errorf("create api key: %w", err)
	}
	return k, nil
}

func (s *Store) APIKeyByHash(ctx context.Context, keyHash string) (APIKey, User, error) {
	var k APIKey
	var last sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, name, key_hash, created_at, last_used_at FROM api_keys WHERE key_hash = ?`, keyHash).
		Scan(&k.ID, &k.UserID, &k.Name, &k.KeyHash, &k.CreatedAt, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, User{}, ErrNotFound
	}
	if err != nil {
		return APIKey{}, User{}, fmt.Errorf("api key: %w", err)
	}
	k.LastUsedAt = last.String
	_, _ = s.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = datetime('now') WHERE id = ?`, k.ID)
	u, err := s.UserByID(ctx, k.UserID)
	return k, u, err
}

func (s *Store) APIKeys(ctx context.Context, userID string) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, name, key_hash, created_at, last_used_at FROM api_keys
		WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("api keys: %w", err)
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		var last sql.NullString
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.KeyHash, &k.CreatedAt, &last); err != nil {
			return nil, err
		}
		k.LastUsedAt = last.String
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAPIKey(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete api key: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
