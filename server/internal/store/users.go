package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	ID           string `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"displayName"`
	PasswordHash string `json:"-"`
	PINHash      string `json:"-"`
	HasPassword  bool   `json:"hasPassword"`
	HasPin       bool   `json:"hasPin"`
	Role         string `json:"role"`
	Disabled     bool   `json:"disabled"`
	CreatedAt    string `json:"createdAt"`
	// Avatar is an uploaded picture's /artwork/ path, '' for none.
	// AvatarColour is the theme token the initial is drawn on without one
	// (see AvatarColours), '' for the default.
	Avatar       string `json:"avatar,omitempty"`
	AvatarColour string `json:"avatarColour,omitempty"`
	// LibraryIDs is every library this user can see: the open ones, plus
	// restricted ones naming them or a group they are in (all of them for
	// an admin). Computed on load, not stored; access is set per library.
	LibraryIDs []string `json:"libraryIds"`
	// RequestLevel is what happens when this user asks for something not
	// in the library: RequestNone, RequestApproval or RequestAuto. An
	// admin's stored level is ignored; see EffectiveRequestLevel.
	RequestLevel string `json:"requestLevel"`
	// SegmentEditor marks a trusted editor: a user who may correct intro
	// and credits times and start their detection on the titles they can
	// see. Admins may whatever it says; see CanEditSegments.
	SegmentEditor bool `json:"segmentEditor"`
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// CanEditSegments reports whether the user may set intro and credits times
// by hand and send titles back to detection.
func (u User) CanEditSegments() bool { return u.IsAdmin() || u.SegmentEditor }

// EffectiveRequestLevel is the level that applies: admins are always
// trusted to send requests straight to Sonarr and Radarr.
func (u User) EffectiveRequestLevel() string {
	if u.IsAdmin() {
		return RequestAuto
	}
	if u.RequestLevel == "" {
		return RequestNone
	}
	return u.RequestLevel
}

// AvatarColours are the theme tokens a user may draw their initial on.
// Tokens rather than colours, so each theme supplies its own.
var AvatarColours = []string{"accent-2", "chart-1", "chart-2", "chart-3", "ok", "err"}

// Session is a browser login, and so a device. Token is never stored —
// only its hash; ID is the public handle for listing and revoking it.
type Session struct {
	ID         string `json:"id"`
	TokenHash  string `json:"-"`
	UserID     string `json:"userId"`
	Name       string `json:"name"` // set by the user: "Living-room TV"
	CreatedAt  string `json:"createdAt"`
	ExpiresAt  string `json:"expiresAt"`
	LastSeenAt string `json:"lastSeenAt"`
	Client     string `json:"client"` // "Chrome · Linux", from the User-Agent
	ClientIP   string `json:"clientIp"`
	// Queue is the device's play queue as JSON (see SessionQueue).
	Queue          string `json:"-"`
	QueueUpdatedAt string `json:"-"`
	// AreaID is the Home Assistant area the device is in, its room; "" for
	// none (see docs/homeassistant.md).
	AreaID string `json:"areaId"`
	// DeviceSecretHash is the hash of the device cookie's secret; '' until
	// the browser has been given one. Never sent.
	DeviceSecretHash string `json:"-"`
}

// SessionQueue is a device's video play queue, as stored on its session.
type SessionQueue struct {
	ItemIDs []string `json:"itemIds"`
	Index   int      `json:"index"`
}

// MaxQueueItems bounds a stored queue, matching the client's own cap.
const MaxQueueItems = 500

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
	SetDisplayName(ctx context.Context, id, displayName string) error
	SetAvatar(ctx context.Context, id, path string) error
	SetAvatarColour(ctx context.Context, id, colour string) error
	SetRequestLevel(ctx context.Context, id, level string) error
	SetSegmentEditor(ctx context.Context, id string, on bool) error
	SetPasswordHash(ctx context.Context, id, hash string) error
	SetPINHash(ctx context.Context, id, hash string) error
	Profiles(ctx context.Context) ([]User, error)
	DeleteUser(ctx context.Context, id string) error
	AdoptLocalState(ctx context.Context, userID string) error

	CreateSession(ctx context.Context, tokenHash, userID, expiresAt, client, ip, name string) (Session, error)
	SessionByHash(ctx context.Context, tokenHash string) (Session, User, error)
	TouchSession(ctx context.Context, tokenHash string) error
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteUserSessions(ctx context.Context, userID string) error
	UserSessions(ctx context.Context, userID string) ([]Session, error)
	UserSession(ctx context.Context, userID, id string) (Session, error)
	DeleteUserSession(ctx context.Context, userID, id string) error
	DeleteOtherSessions(ctx context.Context, userID, keepHash string) (int, error)
	SetSessionName(ctx context.Context, tokenHash, name string) error
	SetSessionQueue(ctx context.Context, tokenHash string, q SessionQueue) error
	PruneSessions(ctx context.Context) (int, error)

	CreateAPIKey(ctx context.Context, userID, name, keyHash string) (APIKey, error)
	APIKeyByHash(ctx context.Context, keyHash string) (APIKey, User, error)
	APIKeys(ctx context.Context, userID string) ([]APIKey, error)
	DeleteAPIKey(ctx context.Context, userID, id string) error
}

var _ UserRepo = (*Store)(nil)

const userCols = `id, username, display_name, password_hash, pin_hash, role, disabled, created_at, avatar, avatar_colour, request_level, segment_editor`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var disabled int
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.PINHash, &u.Role, &disabled, &u.CreatedAt,
		&u.Avatar, &u.AvatarColour, &u.RequestLevel, &u.SegmentEditor)
	u.Disabled = disabled == 1
	u.HasPassword = u.PasswordHash != ""
	u.HasPin = u.PINHash != ""
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
	u.LibraryIDs, err = s.visibleLibraries(ctx, u)
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
	u.LibraryIDs, err = s.visibleLibraries(ctx, u)
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
		if out[i].LibraryIDs, err = s.visibleLibraries(ctx, out[i]); err != nil {
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

// SetDisplayName changes only the name shown for a user, which is what a
// user may change about themselves; role and access stay with the admin.
func (s *Store) SetDisplayName(ctx context.Context, id, displayName string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET display_name = ? WHERE id = ?`, displayName, id)
	if err != nil {
		return fmt.Errorf("set display name: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("set display name: %w", err)
	} else if n == 0 {
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

// SetPINHash sets ("" clears) a user's quick sign-in PIN.
func (s *Store) SetPINHash(ctx context.Context, id, hash string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET pin_hash = ? WHERE id = ?`, hash, id)
	if err != nil {
		return fmt.Errorf("set pin: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Profiles lists enabled users who can sign in with a PIN — what the
// sign-in screen's profile picker shows before anyone is signed in.
func (s *Store) Profiles(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users
		WHERE disabled = 0 AND pin_hash != '' ORDER BY display_name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("profiles: %w", err)
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
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

// AdoptLocalState hands everything recorded under the pre-auth placeholder
// user to a real account — run once, for the first admin.
func (s *Store) AdoptLocalState(ctx context.Context, userID string) error {
	for _, table := range []string{"play_state", "play_events", "play_counts", "audit_log"} {
		if _, err := s.db.ExecContext(ctx, `UPDATE `+table+` SET user_id = ? WHERE user_id = ?`, userID, LocalUser); err != nil {
			return fmt.Errorf("adopt %s: %w", table, err)
		}
	}
	return nil
}

// ---- avatars ----

// SetAvatar records an uploaded picture's /artwork/ path (” removes it).
func (s *Store) SetAvatar(ctx context.Context, id, path string) error {
	return s.setUserField(ctx, id, "avatar", path)
}

// SetRequestLevel sets what happens to the user's requests.
func (s *Store) SetRequestLevel(ctx context.Context, id, level string) error {
	if !slices.Contains(RequestLevels, level) {
		return fmt.Errorf("unknown request level %q: want none, approval or auto", level)
	}
	return s.setUserField(ctx, id, "request_level", level)
}

// SetSegmentEditor makes the user a trusted editor of intro and credits
// times, or stops them being one.
func (s *Store) SetSegmentEditor(ctx context.Context, id string, on bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET segment_editor = ? WHERE id = ?`, on, id)
	if err != nil {
		return fmt.Errorf("set segment editor: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAvatarColour sets the token the initial is drawn on; ” is the default.
func (s *Store) SetAvatarColour(ctx context.Context, id, colour string) error {
	if colour != "" && !slices.Contains(AvatarColours, colour) {
		return fmt.Errorf("unknown avatar colour %q", colour)
	}
	return s.setUserField(ctx, id, "avatar_colour", colour)
}

func (s *Store) setUserField(ctx context.Context, id, column, value string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET `+column+` = ? WHERE id = ?`, value, id)
	if err != nil {
		return fmt.Errorf("set %s: %w", column, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- sessions ----

// A session is read with its device: the name and room are the screen's
// (see docs/devices.md), and the session shares the device's id.
const sessionCols = `s.id, s.token_hash, s.user_id, d.name, s.created_at, s.expires_at, s.last_seen_at,
	s.client, s.client_ip, s.queue, s.queue_updated_at, d.ha_area, d.secret_hash`

const sessionFrom = `sessions s JOIN devices d ON d.id = s.id`

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var sess Session
	err := row.Scan(&sess.ID, &sess.TokenHash, &sess.UserID, &sess.Name, &sess.CreatedAt, &sess.ExpiresAt,
		&sess.LastSeenAt, &sess.Client, &sess.ClientIP, &sess.Queue, &sess.QueueUpdatedAt, &sess.AreaID, &sess.DeviceSecretHash)
	return sess, err
}

// CreateSession signs a user in on a new device called name. Sign-in in a
// browser goes through SignInOnDevice, onto the device its cookie names;
// this is the shorthand for a device that is new anyway.
func (s *Store) CreateSession(ctx context.Context, tokenHash, userID, expiresAt, client, ip, name string) (Session, error) {
	d, err := s.CreateDevice(ctx, "", client, ip)
	if err != nil {
		return Session{}, err
	}
	if name != "" {
		if _, err := s.db.ExecContext(ctx, `UPDATE devices SET name = ? WHERE id = ?`, strings.TrimSpace(name), d.ID); err != nil {
			return Session{}, fmt.Errorf("name device: %w", err)
		}
	}
	return s.SignInOnDevice(ctx, d.ID, tokenHash, userID, expiresAt, client, ip)
}

// SignInOnDevice starts a session on a device. A device holds one session
// at a time, so whoever was signed in on it is signed out: choosing a
// profile on the living-room TV replaces the last person's session there.
func (s *Store) SignInOnDevice(ctx context.Context, deviceID, tokenHash, userID, expiresAt, client, ip string) (Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("sign in: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, deviceID); err != nil {
		return Session{}, fmt.Errorf("end the device's last session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, token_hash, user_id, expires_at, client, client_ip)
		VALUES (?, ?, ?, ?, ?, ?)`, deviceID, tokenHash, userID, expiresAt, client, ip); err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE devices SET client = ?, client_ip = ?, last_seen_at = datetime('now')
		WHERE id = ?`, client, ip, deviceID); err != nil {
		return Session{}, fmt.Errorf("update device: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("sign in: %w", err)
	}
	sess, err := scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM `+sessionFrom+` WHERE s.token_hash = ?`, tokenHash))
	if err != nil {
		return Session{}, fmt.Errorf("read new session: %w", err)
	}
	return sess, nil
}

// SessionByHash resolves a live (unexpired) session and its user.
func (s *Store) SessionByHash(ctx context.Context, tokenHash string) (Session, User, error) {
	sess, err := scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionCols+`
		FROM `+sessionFrom+` WHERE s.token_hash = ? AND s.expires_at > datetime('now')`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, User{}, ErrNotFound
	}
	if err != nil {
		return Session{}, User{}, fmt.Errorf("session: %w", err)
	}
	u, err := s.UserByID(ctx, sess.UserID)
	return sess, u, err
}

// ExtendSession moves a session's expiry, for sliding expiry: a device in
// use stays signed in.
func (s *Store) ExtendSession(ctx context.Context, tokenHash, expiresAt string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, expiresAt, tokenHash); err != nil {
		return fmt.Errorf("extend session: %w", err)
	}
	return nil
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

// UserSessions lists a user's live sessions, most recently active first.
func (s *Store) UserSessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionCols+` FROM `+sessionFrom+`
		WHERE s.user_id = ? AND s.expires_at > datetime('now') ORDER BY s.last_seen_at DESC, s.created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("sessions: %w", err)
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// UserSession is one of a user's live sessions by public id. Scoped to the
// user, so one account can never address another's devices.
func (s *Store) UserSession(ctx context.Context, userID, id string) (Session, error) {
	sess, err := scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM `+sessionFrom+`
		WHERE s.user_id = ? AND s.id = ? AND s.expires_at > datetime('now')`, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("session %s: %w", id, err)
	}
	return sess, nil
}

// DeleteUserSession signs one device out.
func (s *Store) DeleteUserSession(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOtherSessions signs every device out but the one keepHash belongs
// to, returning how many went.
func (s *Store) DeleteOtherSessions(ctx context.Context, userID, keepHash string) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, userID, keepHash)
	if err != nil {
		return 0, fmt.Errorf("delete other sessions: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// SetSessionName names the device a session is on.
func (s *Store) SetSessionName(ctx context.Context, tokenHash, name string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE devices SET name = ?
		WHERE id = (SELECT id FROM sessions WHERE token_hash = ?)`, strings.TrimSpace(name), tokenHash); err != nil {
		return fmt.Errorf("name device: %w", err)
	}
	return nil
}

// SetSessionQueue records the device's play queue. An empty queue clears it.
func (s *Store) SetSessionQueue(ctx context.Context, tokenHash string, q SessionQueue) error {
	if len(q.ItemIDs) > MaxQueueItems {
		return fmt.Errorf("a queue holds at most %d items", MaxQueueItems)
	}
	raw := ""
	if len(q.ItemIDs) > 0 {
		q.Index = max(0, min(q.Index, len(q.ItemIDs)-1))
		b, err := json.Marshal(q)
		if err != nil {
			return fmt.Errorf("encode queue: %w", err)
		}
		raw = string(b)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET queue = ?, queue_updated_at = datetime('now')
		WHERE token_hash = ?`, raw, tokenHash); err != nil {
		return fmt.Errorf("save queue: %w", err)
	}
	return nil
}

// ParsedQueue decodes the session's stored queue; empty when it has none.
func (sess Session) ParsedQueue() (SessionQueue, error) {
	var q SessionQueue
	if sess.Queue == "" {
		return q, nil
	}
	if err := json.Unmarshal([]byte(sess.Queue), &q); err != nil {
		return SessionQueue{}, fmt.Errorf("session %s queue: %w", sess.ID, err)
	}
	return q, nil
}

// PruneSessions removes expired sessions, which otherwise stay forever.
func (s *Store) PruneSessions(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= datetime('now')`)
	if err != nil {
		return 0, fmt.Errorf("prune sessions: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
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
