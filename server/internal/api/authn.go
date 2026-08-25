package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/playback"
	"github.com/garybowers/cineva/server/internal/store"
)

const (
	sessionCookie = "cineva_session"
	ctxUser       = "cineva.user"
	ctxSession    = "cineva.session" // token hash, when authenticated by cookie
)

// authenticate resolves the caller from the session cookie or an API key
// (Authorization: Bearer cnv_… / X-Api-Key) and stores the user on the
// context. Requests with neither, or with a dead credential, get 401.
// Until the first account exists every request is anonymous-admin so the
// setup screen and CLI-less bootstrap work; nothing else is reachable
// then anyway because the UI forces setup.
func (s *server) authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		if key := bearerKey(c); key != "" {
			_, u, err := s.store.APIKeyByHash(ctx, auth.HashToken(key))
			if err != nil || u.Disabled {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid API key")
			}
			c.Set(ctxUser, u)
			return next(c)
		}
		if ck, err := c.Cookie(sessionCookie); err == nil && ck.Value != "" {
			hash := auth.HashToken(ck.Value)
			_, u, err := s.store.SessionByHash(ctx, hash)
			if err != nil || u.Disabled {
				clearSessionCookie(c)
				return echo.NewHTTPError(http.StatusUnauthorized, "session expired")
			}
			_ = s.store.TouchSession(ctx, hash)
			c.Set(ctxUser, u)
			c.Set(ctxSession, hash)
			return next(c)
		}
		if n, err := s.store.CountUsers(ctx); err == nil && n == 0 {
			c.Set(ctxUser, store.User{ID: store.LocalUser, Username: "setup", Role: store.RoleAdmin})
			return next(c)
		}
		return echo.NewHTTPError(http.StatusUnauthorized, "sign in required")
	}
}

func bearerKey(c echo.Context) string {
	if k := c.Request().Header.Get("X-Api-Key"); k != "" {
		return k
	}
	if h := c.Request().Header.Get(echo.HeaderAuthorization); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// requireAdmin gates admin-only routes.
func requireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !currentUser(c).IsAdmin() {
			return echo.NewHTTPError(http.StatusForbidden, "admin only")
		}
		return next(c)
	}
}

func currentUser(c echo.Context) store.User {
	if u, ok := c.Get(ctxUser).(store.User); ok {
		return u
	}
	return store.User{}
}

func userID(c echo.Context) string { return currentUser(c).ID }

// canSeeLibrary: admins see everything; users only granted libraries.
func canSeeLibrary(u store.User, libraryID string) bool {
	if u.IsAdmin() {
		return true
	}
	for _, id := range u.LibraryIDs {
		if id == libraryID {
			return true
		}
	}
	return false
}

func (s *server) setSessionCookie(c echo.Context, token string, expires time.Time) {
	c.SetCookie(&http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secureCookies,
	})
}

func clearSessionCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func clientLabel(c echo.Context) string {
	browser, osName := playback.ParseUserAgent(c.Request().UserAgent())
	if osName != "" {
		return browser + " · " + osName
	}
	return browser
}

// ---- /auth ----

type authStatus struct {
	SetupRequired bool        `json:"setupRequired"`
	User          *store.User `json:"user,omitempty"`
}

// GET /auth/status — public: does the server need its first admin, and
// who (if anyone) is the caller.
func (s *server) authStatusHandler(c echo.Context) error {
	ctx := c.Request().Context()
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return err
	}
	out := authStatus{SetupRequired: n == 0}
	if n > 0 {
		// Resolve the caller without failing the request when anonymous.
		if ck, err := c.Cookie(sessionCookie); err == nil && ck.Value != "" {
			if _, u, err := s.store.SessionByHash(ctx, auth.HashToken(ck.Value)); err == nil && !u.Disabled {
				out.User = &u
			}
		} else if key := bearerKey(c); key != "" {
			if _, u, err := s.store.APIKeyByHash(ctx, auth.HashToken(key)); err == nil && !u.Disabled {
				out.User = &u
			}
		}
	}
	return c.JSON(http.StatusOK, out)
}

type credentials struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
}

// POST /auth/setup — create the first admin. Refused once any user exists.
func (s *server) authSetup(c echo.Context) error {
	ctx := c.Request().Context()
	if n, err := s.store.CountUsers(ctx); err != nil {
		return err
	} else if n > 0 {
		return echo.NewHTTPError(http.StatusConflict, "setup already completed")
	}
	var req credentials
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := auth.CheckPasswordPolicy(req.Password); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return err
	}
	u, err := s.store.CreateUser(ctx, req.Username, req.DisplayName, hash, store.RoleAdmin)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if err := s.store.AdoptLocalState(ctx, u.ID); err != nil {
		return err
	}
	if err := s.startSession(c, u); err != nil {
		return err
	}
	c.Set(ctxUser, u)
	s.audit(c, "user.setup", "user", u.ID, u.Username, "first admin created")
	return c.JSON(http.StatusCreated, u)
}

// POST /auth/login
func (s *server) authLogin(c echo.Context) error {
	ctx := c.Request().Context()
	var req credentials
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	u, err := s.store.UserByUsername(ctx, req.Username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	// Verify against a dummy hash on unknown users so timing does not
	// reveal which usernames exist.
	hash := u.PasswordHash
	if hash == "" {
		hash = dummyHash
	}
	if !auth.VerifyPassword(req.Password, hash) || u.ID == "" || u.Disabled {
		c.Set(ctxUser, store.User{ID: "anonymous", Username: "anonymous"})
		s.audit(c, "login.failed", "user", "", strings.TrimSpace(req.Username), "")
		return echo.NewHTTPError(http.StatusUnauthorized, "incorrect username or password")
	}
	if err := s.startSession(c, u); err != nil {
		return err
	}
	c.Set(ctxUser, u)
	s.audit(c, "login", "user", u.ID, u.Username, "")
	return c.JSON(http.StatusOK, u)
}

var dummyHash, _ = auth.HashPassword("cineva-dummy-timing-equaliser")

func (s *server) startSession(c echo.Context, u store.User) error {
	token, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	expires := time.Now().UTC().Add(s.sessionTTL)
	if err := s.store.CreateSession(c.Request().Context(), hash, u.ID,
		expires.Format("2006-01-02 15:04:05"), clientLabel(c), c.RealIP()); err != nil {
		return err
	}
	s.setSessionCookie(c, token, expires)
	return nil
}

// POST /auth/logout
func (s *server) authLogout(c echo.Context) error {
	if hash, ok := c.Get(ctxSession).(string); ok {
		_ = s.store.DeleteSession(c.Request().Context(), hash)
	}
	clearSessionCookie(c)
	s.audit(c, "logout", "user", userID(c), currentUser(c).Username, "")
	return c.NoContent(http.StatusNoContent)
}

// GET /auth/me
func (s *server) authMe(c echo.Context) error {
	return c.JSON(http.StatusOK, currentUser(c))
}

// POST /auth/password {current, new} — change own password; other
// sessions are signed out.
func (s *server) authPassword(c echo.Context) error {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	u := currentUser(c)
	if !auth.VerifyPassword(req.Current, u.PasswordHash) {
		return echo.NewHTTPError(http.StatusForbidden, "current password is incorrect")
	}
	if err := auth.CheckPasswordPolicy(req.New); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if err := s.store.SetPasswordHash(ctx, u.ID, hash); err != nil {
		return err
	}
	_ = s.store.DeleteUserSessions(ctx, u.ID)
	if err := s.startSession(c, u); err != nil {
		return err
	}
	s.audit(c, "user.password", "user", u.ID, u.Username, "changed own password")
	return c.NoContent(http.StatusNoContent)
}

// ---- API keys (own) ----

func (s *server) listAPIKeys(c echo.Context) error {
	keys, err := s.store.APIKeys(c.Request().Context(), userID(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, keys)
}

// POST /auth/keys {name} — the key itself is returned exactly once.
func (s *server) createAPIKey(c echo.Context) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	token, _, err := auth.NewToken()
	if err != nil {
		return err
	}
	key := auth.APIKeyPrefix + token
	k, err := s.store.CreateAPIKey(c.Request().Context(), userID(c), req.Name, auth.HashToken(key))
	if err != nil {
		return err
	}
	s.audit(c, "apikey.create", "apikey", k.ID, k.Name, "")
	return c.JSON(http.StatusCreated, struct {
		store.APIKey
		Key string `json:"key"`
	}{k, key})
}

func (s *server) deleteAPIKey(c echo.Context) error {
	if err := s.store.DeleteAPIKey(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "key not found")
	}
	s.audit(c, "apikey.delete", "apikey", c.Param("id"), "", "")
	return c.NoContent(http.StatusNoContent)
}

// ---- /users (admin) ----

type userRequest struct {
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Password    string   `json:"password"`
	Role        string   `json:"role"`
	Disabled    bool     `json:"disabled"`
	LibraryIDs  []string `json:"libraryIds"`
}

func (s *server) listUsers(c echo.Context) error {
	users, err := s.store.Users(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, users)
}

func (s *server) createUser(c echo.Context) error {
	var req userRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := auth.CheckPasswordPolicy(req.Password); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if req.Role == "" {
		req.Role = store.RoleUser
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	u, err := s.store.CreateUser(ctx, req.Username, req.DisplayName, hash, req.Role)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if len(req.LibraryIDs) > 0 {
		if err := s.store.SetLibraryGrants(ctx, u.ID, req.LibraryIDs); err != nil {
			return err
		}
		u.LibraryIDs = req.LibraryIDs
	}
	s.audit(c, "user.create", "user", u.ID, u.Username, u.Role)
	s.events.Publish(events.Event{Type: "users"})
	return c.JSON(http.StatusCreated, u)
}

// PATCH /users/:id — display name, role, disabled, library grants, and
// optionally a new password (admin reset; signs the user out everywhere).
func (s *server) updateUser(c echo.Context) error {
	ctx := c.Request().Context()
	u, err := s.store.UserByID(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	req := userRequest{DisplayName: u.DisplayName, Role: u.Role, Disabled: u.Disabled, LibraryIDs: u.LibraryIDs}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	me := currentUser(c)
	if u.ID == me.ID && (req.Role != store.RoleAdmin || req.Disabled) {
		return echo.NewHTTPError(http.StatusBadRequest, "you cannot demote or disable yourself")
	}
	if err := s.store.UpdateUser(ctx, u.ID, req.DisplayName, req.Role, req.Disabled); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if err := s.store.SetLibraryGrants(ctx, u.ID, req.LibraryIDs); err != nil {
		return err
	}
	detail := req.Role
	if req.Password != "" {
		if err := auth.CheckPasswordPolicy(req.Password); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			return err
		}
		if err := s.store.SetPasswordHash(ctx, u.ID, hash); err != nil {
			return err
		}
		_ = s.store.DeleteUserSessions(ctx, u.ID)
		detail += " · password reset"
	}
	if req.Disabled {
		_ = s.store.DeleteUserSessions(ctx, u.ID)
		detail += " · disabled"
	}
	s.audit(c, "user.update", "user", u.ID, u.Username, detail)
	s.events.Publish(events.Event{Type: "users"})
	updated, err := s.store.UserByID(ctx, u.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, updated)
}

func (s *server) deleteUser(c echo.Context) error {
	ctx := c.Request().Context()
	if c.Param("id") == userID(c) {
		return echo.NewHTTPError(http.StatusBadRequest, "you cannot delete yourself")
	}
	u, err := s.store.UserByID(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	if err := s.store.DeleteUser(ctx, u.ID); err != nil {
		return err
	}
	s.audit(c, "user.delete", "user", u.ID, u.Username, "")
	s.events.Publish(events.Event{Type: "users"})
	return c.NoContent(http.StatusNoContent)
}
