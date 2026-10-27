package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/jellyfin"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/store"
)

// The Jellyfin-compatible API (server/docs/jellyfin.md): a shim over the
// same store, play state, next-up and playback decision the web client
// uses, so the Jellyfin apps on televisions and phones can browse and play
// a Cineva library. Mounted at the root, where those apps look, with the
// paths matched without case as Jellyfin matches them.

var jfLog = logging.For("jellyfin")

const (
	ctxJFAuth = "cineva.jellyfin.auth" // the parsed Authorization header
	// jfDefaultLimit bounds a listing a client asked for without a limit.
	jfDefaultLimit = 200
	jfMaxLimit     = 2000
)

// jfPrefixes are the first path segments the shim claims, lower-cased.
// Requests under them are routed without case, and an unknown one answers
// Jellyfin's 404 rather than the web client's page.
var jfPrefixes = []string{
	"/system/", "/system", "/users", "/userviews", "/useritems", "/items", "/videos/", "/audio/",
	"/shows/", "/sessions", "/branding/", "/localization/", "/displaypreferences/", "/plugins",
	"/scheduledtasks", "/genres", "/artists", "/search/", "/socket", "/quickconnect/",
	"/livetv/", "/channels", "/collections", "/studios", "/years", "/notifications", "/syncplay/",
	"/devices", "/library/", "/persons", "/packages", "/playlists", "/musicgenres", "/auth/keys",
	"/startup/", "/environment/", "/dlna/", "/images/", "/tasks", "/repositories", "/apikeys",
	"/web/", "/trailers", "/movies/", "/albums/", "/videos",
}

// jfServerID is the server's id as Jellyfin clients key it: a hash of the
// identity key, stable across restarts.
func (s *server) jfServerID(ctx context.Context) string {
	s.jfIDOnce.Do(func() {
		id, err := s.store.EnsureIdentity(ctx)
		if err != nil {
			jfLog.Warn("reading the server identity failed", "err", err)
			s.jfID = "cineva"
			return
		}
		sum := sha256.Sum256(id.PublicKey)
		s.jfID = hex.EncodeToString(sum[:16])
	})
	return s.jfID
}

// jfDeviceSecret is the device secret for a Jellyfin client's DeviceId: an
// HMAC under the identity key, so the same app on the same screen is the
// same device every time and nothing else can name it.
func (s *server) jfDeviceSecret(ctx context.Context, deviceID string) (string, error) {
	id, err := s.store.EnsureIdentity(ctx)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, id.PrivateKey.Seed())
	mac.Write([]byte("jellyfin-device:" + deviceID))
	return "jf_" + hex.EncodeToString(mac.Sum(nil)), nil
}

// jfState is what the shim keeps on the server: the id, computed once.
type jfState struct {
	jfIDOnce sync.Once
	jfID     string
}

// jellyfinRoutes mounts the shim.
func (s *server) jellyfinRoutes(e *echo.Echo) {
	// Jellyfin's paths are matched without case and some clients add an
	// /emby prefix; the routes below are registered in lower case.
	e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			p := c.Request().URL.Path
			lower := strings.ToLower(p)
			lower = strings.TrimPrefix(lower, "/emby")
			if lower == "" {
				lower = "/"
			}
			if lower != p && jfClaims(lower) {
				c.Request().URL.Path = lower
			}
			return next(c)
		}
	})
	// Each handler carries its own auth rather than a group's middleware:
	// an Echo group with middleware also registers catch-all routes for
	// its prefix, and at the root prefix those swallowed every path nothing
	// else claimed, the web client's front page included, answering 401.
	pub := jfRouter{e, s.jellyfinAuth(false)}
	pub.GET("/system/info/public", s.jfSystemInfoPublic)
	pub.GET("/system/ping", s.jfPing)
	pub.POST("/system/ping", s.jfPing)
	pub.GET("/branding/configuration", s.jfBranding)
	pub.GET("/branding/css", s.jfBrandingCSS)
	pub.GET("/branding/css.css", s.jfBrandingCSS)
	pub.GET("/users/public", s.jfUsersPublic)
	pub.POST("/users/authenticatebyname", s.jfAuthenticate)
	pub.GET("/quickconnect/enabled", func(c echo.Context) error { return c.JSON(http.StatusOK, false) })
	pub.GET("/items/:id/images/:type", s.jfItemImage)
	pub.GET("/items/:id/images/:type/:index", s.jfItemImage)
	pub.HEAD("/items/:id/images/:type", s.jfItemImage)
	pub.GET("/users/:id/images/:type", s.jfUserImage)
	pub.GET("/users/:id/images/:type/:index", s.jfUserImage)
	pub.GET("/socket", s.jfSocket)
	pub.GET("/videos/:id/hls/:sid/:file", s.jfHLSFile)
	pub.GET("/videos/:id/:source/subtitles/:index/stream.vtt", s.jfSubtitle)
	pub.GET("/videos/:id/:source/subtitles/:index/:start/stream.vtt", s.jfSubtitle)

	g := jfRouter{e, s.jellyfinAuth(true)}
	g.GET("/system/info", s.jfSystemInfo)
	g.GET("/system/endpoint", s.jfEndpoint)
	g.GET("/system/configuration", s.jfNotFoundJSON)
	g.GET("/localization/cultures", s.jfEmptyList)
	g.GET("/localization/countries", s.jfEmptyList)
	g.GET("/localization/parentalratings", s.jfEmptyList)
	g.GET("/localization/options", s.jfEmptyList)
	g.GET("/displaypreferences/:id", s.jfDisplayPrefs)
	g.POST("/displaypreferences/:id", s.jfSetDisplayPrefs)
	g.GET("/plugins", s.jfEmptyList)
	g.GET("/scheduledtasks", s.jfEmptyList)
	g.GET("/packages", s.jfEmptyList)
	g.GET("/users/me", s.jfMe)
	g.GET("/users/:id", s.jfUser)
	g.GET("/users", s.jfUsers)
	g.POST("/sessions/logout", s.jfLogout)
	g.POST("/sessions/capabilities", s.jfCapabilities)
	g.POST("/sessions/capabilities/full", s.jfCapabilities)
	g.GET("/sessions", s.jfSessions)
	g.POST("/sessions/playing", s.jfPlaying)
	g.POST("/sessions/playing/progress", s.jfProgress)
	g.POST("/sessions/playing/stopped", s.jfStopped)
	g.POST("/sessions/playing/ping", s.jfPlayingPing)
	g.GET("/users/:id/views", s.jfViews)
	g.GET("/userviews", s.jfViews)
	g.GET("/users/:id/items", s.jfItems)
	g.GET("/items", s.jfItems)
	g.GET("/users/:id/items/latest", s.jfLatest)
	g.GET("/items/latest", s.jfLatest)
	g.GET("/users/:id/items/resume", s.jfResume)
	g.GET("/useritems/resume", s.jfResume)
	g.GET("/items/resume", s.jfResume)
	g.GET("/users/:id/items/:item", s.jfItem)
	g.GET("/items/:id", s.jfItem)
	g.GET("/items/:id/ancestors", s.jfAncestors)
	g.GET("/items/:id/similar", s.jfSimilar)
	g.GET("/movies/:id/similar", s.jfSimilar)
	g.GET("/shows/:id/similar", s.jfSimilar)
	g.GET("/users/:id/items/:item/intros", s.jfEmptyQuery)
	g.GET("/items/:id/intros", s.jfEmptyQuery)
	g.GET("/items/:id/thememedia", s.jfThemeMedia)
	g.GET("/items/:id/themesongs", s.jfEmptyQuery)
	g.GET("/items/:id/themevideos", s.jfEmptyQuery)
	g.GET("/users/:id/items/:item/specialfeatures", s.jfEmptyList)
	g.GET("/items/:id/specialfeatures", s.jfEmptyList)
	g.GET("/users/:id/items/:item/localtrailers", s.jfEmptyList)
	g.GET("/items/:id/localtrailers", s.jfEmptyList)
	g.GET("/shows/nextup", s.jfNextUp)
	g.GET("/shows/upcoming", s.jfEmptyQuery)
	g.GET("/shows/:id/seasons", s.jfSeasons)
	g.GET("/shows/:id/episodes", s.jfEpisodes)
	g.GET("/genres", s.jfGenres)
	g.GET("/musicgenres", s.jfGenres)
	g.GET("/items/filters", s.jfFilters)
	g.GET("/items/filters2", s.jfFilters2)
	g.GET("/search/hints", s.jfSearchHints)
	g.GET("/persons", s.jfEmptyQuery)
	g.GET("/studios", s.jfEmptyQuery)
	g.GET("/years", s.jfEmptyQuery)
	g.GET("/artists", s.jfArtists)
	g.GET("/artists/albumartists", s.jfArtists)
	g.GET("/collections", s.jfEmptyQuery)
	g.GET("/channels", s.jfEmptyQuery)
	g.GET("/livetv/info", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"Services": []any{}, "IsEnabled": false, "EnabledUsers": []any{}})
	})
	g.GET("/livetv/programs", s.jfEmptyQuery)
	g.GET("/livetv/channels", s.jfEmptyQuery)
	g.GET("/livetv/recordings", s.jfEmptyQuery)
	g.GET("/livetv/recordings/folders", s.jfEmptyQuery)
	g.GET("/livetv/timers", s.jfEmptyQuery)
	g.GET("/livetv/seriestimers", s.jfEmptyQuery)
	g.GET("/notifications/:id", s.jfEmptyList)
	g.GET("/notifications/:id/summary", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]int{"UnreadCount": 0, "MaxUnreadNotificationLevel": 0})
	})
	g.GET("/syncplay/list", s.jfEmptyList)
	g.GET("/devices", s.jfEmptyQuery)
	g.GET("/playlists/:id/items", s.jfEmptyQuery)
	// Playback (stage 2) and the state calls.
	g.POST("/items/:id/playbackinfo", s.jfPlaybackInfo)
	g.GET("/items/:id/playbackinfo", s.jfPlaybackInfo)
	g.GET("/videos/:id/stream", s.jfVideoStream)
	g.GET("/videos/:id/stream.:ext", s.jfVideoStream)
	g.HEAD("/videos/:id/stream", s.jfVideoStream)
	g.GET("/videos/:id/master.m3u8", s.jfMaster)
	g.GET("/videos/:id/main.m3u8", s.jfMaster)
	g.GET("/videos/:id/live.m3u8", s.jfMaster)
	g.GET("/items/:id/download", s.jfDownload)
	g.POST("/users/:id/playeditems/:item", s.jfMarkPlayed)
	g.DELETE("/users/:id/playeditems/:item", s.jfMarkUnplayed)
	g.POST("/useritems/:id/userdata", s.jfSetUserData)
	g.GET("/useritems/:id/userdata", s.jfGetUserData)
	g.GET("/items/:id/userdata", s.jfGetUserData)
	g.POST("/users/:id/favoriteitems/:item", s.jfFavourite)
	g.DELETE("/users/:id/favoriteitems/:item", s.jfUnfavourite)
	g.POST("/useritems/:id/userdata/favorite", s.jfFavourite)

	// Everything else under the shim's prefixes is not served: 404 in
	// Jellyfin's shape, so a client treats it as absent rather than
	// reading the web client's page as an answer. A path and method that
	// already have a handler keep it: registering over them would replace
	// it.
	taken := map[string]bool{}
	for _, r := range e.Routes() {
		taken[r.Method+" "+r.Path] = true
	}
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodHead}
	for _, p := range jfPrefixes {
		p = strings.TrimSuffix(p, "/")
		if p == "" {
			continue
		}
		for _, path := range []string{p + "/*", p} {
			for _, m := range methods {
				if !taken[m+" "+path] {
					e.Add(m, path, s.jfNotFoundJSON)
					taken[m+" "+path] = true
				}
			}
		}
	}
}

// jfClaims reports whether a (lower-cased) path is the shim's.
// jfRequest says a request comes from a Jellyfin client rather than a
// browser: it carries the shim's headers, token or /emby prefix. An unknown
// path from one gets Jellyfin's 404 shape rather than the web client's
// page, which a browser gets for a deep link.
func jfRequest(c echo.Context) bool {
	r := c.Request()
	if strings.HasPrefix(strings.ToLower(r.URL.Path), "/emby/") {
		return true
	}
	if r.Header.Get("X-Emby-Authorization") != "" || r.Header.Get("X-Emby-Token") != "" ||
		r.Header.Get("X-MediaBrowser-Token") != "" || r.URL.Query().Get("api_key") != "" {
		return true
	}
	return strings.HasPrefix(r.Header.Get(echo.HeaderAuthorization), "MediaBrowser")
}

// jfRouter registers a shim route with the shim's auth wrapped around the
// handler itself (see jellyfinRoutes).
type jfRouter struct {
	e    *echo.Echo
	wrap echo.MiddlewareFunc
}

func (r jfRouter) GET(path string, h echo.HandlerFunc)    { r.e.GET(path, r.wrap(h)) }
func (r jfRouter) POST(path string, h echo.HandlerFunc)   { r.e.POST(path, r.wrap(h)) }
func (r jfRouter) HEAD(path string, h echo.HandlerFunc)   { r.e.HEAD(path, r.wrap(h)) }
func (r jfRouter) DELETE(path string, h echo.HandlerFunc) { r.e.DELETE(path, r.wrap(h)) }

func jfClaims(p string) bool {
	for _, pre := range jfPrefixes {
		if strings.HasPrefix(p, pre) || p == strings.TrimSuffix(pre, "/") {
			return true
		}
	}
	return false
}

// jfProblem answers an error as Jellyfin does (RFC 7807).
func jfProblem(c echo.Context, status int, title string) error {
	return c.JSON(status, jellyfin.Problem{
		Type:  "https://tools.ietf.org/html/rfc7231#section-6.5." + map[int]string{404: "4", 400: "1", 403: "3"}[status],
		Title: title, Status: status,
	})
}

func (s *server) jfNotFoundJSON(c echo.Context) error {
	return jfProblem(c, http.StatusNotFound, "Not Found")
}

func (s *server) jfEmptyList(c echo.Context) error { return c.JSON(http.StatusOK, []any{}) }
func (s *server) jfEmptyQuery(c echo.Context) error {
	return c.JSON(http.StatusOK, jellyfin.EmptyResult())
}

// jfToken is the token a request carries, from any of the places Jellyfin
// clients put it.
func jfToken(c echo.Context) (jellyfin.Auth, string) {
	var a jellyfin.Auth
	for _, h := range []string{"Authorization", "X-Emby-Authorization"} {
		if v := c.Request().Header.Get(h); v != "" && (strings.HasPrefix(strings.ToLower(v), "mediabrowser") || strings.HasPrefix(strings.ToLower(v), "emby")) {
			a = jellyfin.ParseAuth(v)
			break
		}
	}
	token := a.Token
	for _, h := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		if token == "" {
			token = c.Request().Header.Get(h)
		}
	}
	for _, q := range []string{"api_key", "ApiKey", "apikey"} {
		if token == "" {
			token = c.QueryParam(q)
		}
	}
	return a, token
}

// jellyfinAuth resolves the caller from the token: a Cineva session (the
// AccessToken that AuthenticateByName handed out) or a Cineva API key.
// Public routes carry on without one; the rest answer 401.
func (s *server) jellyfinAuth(required bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx := c.Request().Context()
			a, token := jfToken(c)
			c.Set(ctxJFAuth, a)
			if token != "" {
				hash := auth.HashToken(token)
				if sess, u, err := s.store.SessionByHash(ctx, hash); err == nil && !u.Disabled {
					_ = s.store.TouchSession(ctx, hash)
					c.Set(ctxUser, u)
					c.Set(ctxSession, hash)
					c.Set(ctxSessionID, sess.ID)
					return next(c)
				}
				if _, u, err := s.store.APIKeyByHash(ctx, hash); err == nil && !u.Disabled {
					c.Set(ctxUser, u)
					return next(c)
				}
				if required {
					return c.NoContent(http.StatusUnauthorized)
				}
			} else if required {
				return c.NoContent(http.StatusUnauthorized)
			}
			return next(c)
		}
	}
}

// ---- System ----

func (s *server) jfSystemInfoPublic(c echo.Context) error {
	return c.JSON(http.StatusOK, s.jfPublicInfo(c))
}

func (s *server) jfPublicInfo(c echo.Context) jellyfin.SystemInfoPublic {
	host, _ := os.Hostname()
	if host == "" {
		host = "Cineva"
	}
	return jellyfin.SystemInfoPublic{
		LocalAddress: c.Scheme() + "://" + c.Request().Host, ServerName: host,
		Version: jellyfin.Version, ProductName: jellyfin.ProductName, OperatingSystem: "Linux",
		ID: s.jfServerID(c.Request().Context()), StartupWizardCompleted: true,
	}
}

func (s *server) jfSystemInfo(c echo.Context) error {
	return c.JSON(http.StatusOK, jellyfin.SystemInfo{
		SystemInfoPublic: s.jfPublicInfo(c), OperatingSystemDisplayName: "Linux",
		SupportsLibraryMonitor: true, SystemArchitecture: "X64", CompletedInstallations: []any{},
	})
}

func (s *server) jfPing(c echo.Context) error {
	return c.JSON(http.StatusOK, "Cineva Server")
}

func (s *server) jfEndpoint(c echo.Context) error {
	return c.JSON(http.StatusOK, jellyfin.EndpointInfo{IsLocal: true, IsInNetwork: true})
}

func (s *server) jfBranding(c echo.Context) error {
	return c.JSON(http.StatusOK, jellyfin.BrandingOptions{})
}

func (s *server) jfBrandingCSS(c echo.Context) error {
	return c.Blob(http.StatusOK, "text/css", nil)
}

// Display preferences are kept per user as the client sent them, under a
// key naming the preference set and the client.
func jfDisplayPrefKey(c echo.Context) string {
	return "jellyfin.display." + strings.ToLower(c.Param("id")) + "." + strings.ToLower(c.QueryParam("client"))
}

func (s *server) jfDisplayPrefs(c echo.Context) error {
	prefs, err := s.store.Prefs(c.Request().Context(), userID(c))
	if err != nil {
		return err
	}
	if raw, ok := prefs[jfDisplayPrefKey(c)]; ok && raw != "" {
		return c.Blob(http.StatusOK, echo.MIMEApplicationJSON, []byte(raw))
	}
	return c.JSON(http.StatusOK, jellyfin.DefaultDisplayPreferences(c.Param("id"), c.QueryParam("client")))
}

func (s *server) jfSetDisplayPrefs(c echo.Context) error {
	var raw map[string]any
	if err := c.Bind(&raw); err != nil {
		return jfProblem(c, http.StatusBadRequest, "Bad Request")
	}
	body, _ := jsonMarshal(raw)
	if len(body) > 64<<10 {
		return jfProblem(c, http.StatusBadRequest, "Bad Request")
	}
	v := string(body)
	if err := s.store.SetPrefs(c.Request().Context(), userID(c), map[string]*string{jfDisplayPrefKey(c): &v}); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// ---- Users and sign-in ----

func (s *server) jfUserDto(c echo.Context, u store.User) jellyfin.User {
	return jellyfin.User{
		Name: u.DisplayName, ServerID: s.jfServerID(c.Request().Context()), ID: jellyfin.ID(u.ID),
		PrimaryImageTag: jellyfin.ImageTag(u.Avatar), HasPassword: u.HasPassword, HasConfiguredPassword: u.HasPassword,
		Configuration: jellyfin.DefaultConfiguration(), Policy: jellyfin.PolicyFor(u.IsAdmin(), u.Disabled),
	}
}

// GET /Users/Public — the profile picker.
func (s *server) jfUsersPublic(c echo.Context) error {
	users, err := s.store.Users(c.Request().Context())
	if err != nil {
		return err
	}
	out := []jellyfin.User{}
	for _, u := range users {
		if !u.Disabled {
			out = append(out, s.jfUserDto(c, u))
		}
	}
	return c.JSON(http.StatusOK, out)
}

// POST /Users/AuthenticateByName — the account password, through the
// sign-in throttle; a session on a device derived from the client's
// DeviceId (docs/jellyfin.md, Signing in).
func (s *server) jfAuthenticate(c echo.Context) error {
	ctx := c.Request().Context()
	var req struct {
		Username string `json:"Username"`
		Pw       string `json:"Pw"`
		Password string `json:"Password"`
	}
	if err := c.Bind(&req); err != nil {
		return jfProblem(c, http.StatusBadRequest, "Bad Request")
	}
	password := req.Pw
	if password == "" {
		password = req.Password
	}
	username := strings.TrimSpace(req.Username)
	key := "jellyfin:" + strings.ToLower(username)
	a, _ := c.Get(ctxJFAuth).(jellyfin.Auth)
	if ok, _ := s.throttle.Allow(key); !ok {
		return c.NoContent(http.StatusTooManyRequests)
	}
	u, err := s.store.UserByUsername(ctx, username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	hash := u.PasswordHash
	if hash == "" {
		hash = dummyHash
	}
	// An API key made for apps in Settings works as the password too.
	keyed := false
	if u.ID != "" && !u.Disabled && strings.HasPrefix(password, auth.APIKeyPrefix) {
		if _, ku, err := s.store.APIKeyByHash(ctx, auth.HashToken(password)); err == nil && ku.ID == u.ID {
			keyed = true
		}
	}
	if !keyed && (!auth.VerifyPassword(password, hash) || u.ID == "" || u.Disabled || u.PasswordHash == "") {
		s.throttle.Fail(key)
		jfLog.Warn("sign-in failed", "user", username, "client", a.Client, "device", a.Device, "ip", c.RealIP())
		s.audit(c, "login.failed", "user", "", username, "jellyfin")
		return c.NoContent(http.StatusUnauthorized)
	}
	s.throttle.Reset(key)

	sess, token, err := s.jfSignIn(c, u, a)
	if err != nil {
		return err
	}
	c.Set(ctxUser, u)
	s.audit(c, "login", "user", u.ID, u.Username, jfClientLabel(a))
	return c.JSON(http.StatusOK, jellyfin.AuthenticationResult{
		User: s.jfUserDto(c, u), SessionInfo: s.jfSessionInfo(c, u, sess, a),
		AccessToken: token, ServerID: s.jfServerID(ctx),
	})
}

// jfClientLabel names the client as the Devices page shows it.
func jfClientLabel(a jellyfin.Auth) string {
	parts := []string{}
	if a.Client != "" {
		parts = append(parts, a.Client)
	}
	if a.Version != "" {
		parts = append(parts, a.Version)
	}
	if len(parts) == 0 {
		return "Jellyfin client"
	}
	return strings.Join(parts, " ")
}

// jfSignIn starts a session for a Jellyfin client on the device its
// DeviceId derives: the device is found or made, never given the
// browser's cookie, and never one that signs in by itself.
func (s *server) jfSignIn(c echo.Context, u store.User, a jellyfin.Auth) (store.Session, string, error) {
	ctx := c.Request().Context()
	deviceID := a.DeviceID
	if deviceID == "" {
		deviceID = "anonymous:" + c.RealIP()
	}
	secret, err := s.jfDeviceSecret(ctx, deviceID)
	if err != nil {
		return store.Session{}, "", err
	}
	label := jfClientLabel(a)
	d, err := s.store.DeviceBySecret(ctx, auth.HashToken(secret))
	if err != nil {
		if d, err = s.store.CreateDevice(ctx, auth.HashToken(secret), label, c.RealIP()); err != nil {
			return store.Session{}, "", err
		}
		if a.Device != "" {
			if err := s.store.SetDeviceName(ctx, d.ID, a.Device); err != nil {
				jfLog.Warn("naming a device failed", "err", err)
			}
		}
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		return store.Session{}, "", err
	}
	expires := time.Now().UTC().Add(s.sessionTTL)
	sess, err := s.store.SignInOnDevice(ctx, d.ID, hash, u.ID, expires.Format(sessionTimeLayout), label, c.RealIP())
	if err != nil {
		return store.Session{}, "", err
	}
	return sess, token, nil
}

func (s *server) jfMe(c echo.Context) error {
	return c.JSON(http.StatusOK, s.jfUserDto(c, currentUser(c)))
}

// GET /Users/{id} — yourself, or anyone for an admin.
func (s *server) jfUser(c echo.Context) error {
	me := currentUser(c)
	id := jellyfin.ParseID(c.Param("id"))
	if id == me.ID || id == "me" {
		return c.JSON(http.StatusOK, s.jfUserDto(c, me))
	}
	if !me.IsAdmin() {
		return c.NoContent(http.StatusForbidden)
	}
	u, err := s.store.UserByID(c.Request().Context(), id)
	if err != nil {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	return c.JSON(http.StatusOK, s.jfUserDto(c, u))
}

// GET /Users — every account, for an admin; yourself otherwise.
func (s *server) jfUsers(c echo.Context) error {
	me := currentUser(c)
	if !me.IsAdmin() {
		return c.JSON(http.StatusOK, []jellyfin.User{s.jfUserDto(c, me)})
	}
	users, err := s.store.Users(c.Request().Context())
	if err != nil {
		return err
	}
	out := []jellyfin.User{}
	for _, u := range users {
		out = append(out, s.jfUserDto(c, u))
	}
	return c.JSON(http.StatusOK, out)
}

func (s *server) jfLogout(c echo.Context) error {
	if hash, ok := c.Get(ctxSession).(string); ok && hash != "" {
		_ = s.store.DeleteSession(c.Request().Context(), hash)
		s.audit(c, "logout", "user", userID(c), currentUser(c).Username, "jellyfin")
	}
	return c.NoContent(http.StatusNoContent)
}

// POST /Sessions/Capabilities — what the client can do; accepted and,
// for now, not acted on (remote control is a later stage).
func (s *server) jfCapabilities(c echo.Context) error {
	return c.NoContent(http.StatusNoContent)
}

// ---- Views and the item tree ----

// jfLibraries is the libraries the caller may see.
func (s *server) jfLibraries(c echo.Context) ([]store.Library, error) {
	libs, err := s.store.Libraries(c.Request().Context())
	if err != nil {
		return nil, err
	}
	me := currentUser(c)
	out := libs[:0]
	for _, l := range libs {
		if canSeeLibrary(me, l.ID) {
			out = append(out, l)
		}
	}
	return out, nil
}

func (s *server) jfMapper(c echo.Context) jellyfin.Mapper {
	return jellyfin.Mapper{ServerID: s.jfServerID(c.Request().Context()), Paths: currentUser(c).IsAdmin()}
}

func (s *server) jfViews(c echo.Context) error {
	libs, err := s.jfLibraries(c)
	if err != nil {
		return err
	}
	m := s.jfMapper(c)
	res := jellyfin.EmptyResult()
	for _, l := range libs {
		res.Items = append(res.Items, m.Library(l))
	}
	res.TotalRecordCount = len(res.Items)
	return c.JSON(http.StatusOK, res)
}

// jfItemCache remembers items looked up while answering one request, so
// an episode listing resolves each season and series once.
type jfItemCache struct {
	s     *server
	c     echo.Context
	items map[string]*store.Item
}

func (s *server) jfCache(c echo.Context) *jfItemCache {
	return &jfItemCache{s: s, c: c, items: map[string]*store.Item{}}
}

func (k *jfItemCache) get(id string) *store.Item {
	if id == "" {
		return nil
	}
	if it, ok := k.items[id]; ok {
		return it
	}
	it, err := k.s.store.Item(k.c.Request().Context(), id)
	if err != nil || !canSeeLibrary(currentUser(k.c), it.LibraryID) {
		k.items[id] = nil
		return nil
	}
	k.items[id] = &it
	return &it
}

func (k *jfItemCache) put(it store.Item) { cp := it; k.items[it.ID] = &cp }

// context is what an item's mapping needs from around it.
func (k *jfItemCache) context(it store.Item) jellyfin.Context {
	var cx jellyfin.Context
	switch it.Kind {
	case store.KindSeason:
		cx.Series = k.get(it.ParentID)
	case store.KindEpisode:
		if cx.Season = k.get(it.ParentID); cx.Season != nil {
			cx.Series = k.get(cx.Season.ParentID)
		}
	case store.KindAlbum:
		cx.Artist = k.get(it.ParentID)
	case store.KindTrack:
		if album := k.get(it.ParentID); album != nil {
			cx.Artist = k.get(album.ParentID)
		}
	}
	return cx
}

// jfQuery is a listing's request: what a client passed to /Items.
type jfQuery struct {
	parentID       string
	types          []store.ItemKind
	anyType        bool // no IncludeItemTypes: every kind
	recursive      bool
	sortBy         string
	desc           bool
	start, limit   int
	search         string
	genres         []string
	years          map[int]bool
	filters        []string
	nameStartsWith string
	ids            []string
	isFavorite     *bool
	isPlayed       *bool
}

func jfReadQuery(c echo.Context) jfQuery {
	q := jfQuery{
		parentID:       jellyfin.ParseID(c.QueryParam("ParentId")),
		recursive:      strings.EqualFold(c.QueryParam("Recursive"), "true"),
		search:         strings.TrimSpace(c.QueryParam("SearchTerm")),
		nameStartsWith: strings.ToLower(c.QueryParam("NameStartsWith")),
		start:          jellyfin.Atoi(c.QueryParam("StartIndex"), 0),
		limit:          jellyfin.Atoi(c.QueryParam("Limit"), jfDefaultLimit),
	}
	q.limit = min(max(q.limit, 1), jfMaxLimit)
	if sb := jellyfin.ParseList(c.QueryParam("SortBy")); len(sb) > 0 {
		q.sortBy = sb[0]
	}
	if so := jellyfin.ParseList(c.QueryParam("SortOrder")); len(so) > 0 {
		q.desc = strings.EqualFold(so[0], "Descending")
	}
	types := jellyfin.ParseList(c.QueryParam("IncludeItemTypes"))
	if len(types) == 0 {
		q.anyType = true
	}
	for _, t := range types {
		if k := jellyfin.Kind(t); k != "" {
			q.types = append(q.types, k)
		}
	}
	q.genres = jellyfin.ParseList(c.QueryParam("Genres"))
	for _, gid := range jellyfin.ParseList(c.QueryParam("GenreIds")) {
		q.genres = append(q.genres, "id:"+gid)
	}
	if ys := jellyfin.ParseList(c.QueryParam("Years")); len(ys) > 0 {
		q.years = map[int]bool{}
		for _, y := range ys {
			q.years[jellyfin.Atoi(y, 0)] = true
		}
	}
	q.filters = jellyfin.ParseList(c.QueryParam("Filters"))
	for _, id := range jellyfin.ParseList(c.QueryParam("Ids")) {
		q.ids = append(q.ids, jellyfin.ParseID(id))
	}
	if v := c.QueryParam("IsFavorite"); v != "" {
		b := strings.EqualFold(v, "true")
		q.isFavorite = &b
	}
	if v := c.QueryParam("IsPlayed"); v != "" {
		b := strings.EqualFold(v, "true")
		q.isPlayed = &b
	}
	return q
}

func (q jfQuery) wants(kind store.ItemKind) bool {
	if q.anyType {
		return true
	}
	for _, k := range q.types {
		if k == kind {
			return true
		}
	}
	return false
}

func (q jfQuery) wantsOnly(kinds ...store.ItemKind) bool {
	if q.anyType || len(q.types) == 0 {
		return false
	}
	for _, t := range q.types {
		ok := false
		for _, k := range kinds {
			if t == k {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// musicSort maps Jellyfin's sort names onto the music listings'.
func jfMusicSort(sortBy string, kind store.ItemKind) string {
	switch strings.ToLower(sortBy) {
	case "datecreated":
		return "added"
	case "playcount":
		return "plays"
	case "dateplayed", "datelastcontentadded":
		return "played"
	case "random":
		return "random"
	case "productionyear", "premieredate":
		return "year"
	case "albumartist", "artist":
		return "artist"
	case "album":
		return "album"
	case "communityrating", "criticrating":
		return "rating"
	}
	if kind == store.KindTrack {
		return "title"
	}
	return "name"
}

// jfListing resolves a listing: the items, after filters and sorting, and
// their total before paging.
func (s *server) jfListing(c echo.Context, q jfQuery) ([]store.Item, int, error) {
	ctx := c.Request().Context()
	me := currentUser(c)
	var items []store.Item
	if len(q.ids) > 0 {
		got, err := s.store.ItemsByIDs(ctx, q.ids)
		if err != nil {
			return nil, 0, err
		}
		for _, it := range got {
			if canSeeLibrary(me, it.LibraryID) {
				items = append(items, it)
			}
		}
		items = s.jfDecorate(c, items)
		return s.jfFinish(q, items)
	}
	libs, err := s.jfLibraries(c)
	if err != nil {
		return nil, 0, err
	}
	// The parent: a library, an item, or nothing (everything visible).
	var parentLib *store.Library
	var parent *store.Item
	if q.parentID != "" {
		for i := range libs {
			if libs[i].ID == q.parentID {
				parentLib = &libs[i]
			}
		}
		if parentLib == nil {
			it, err := s.store.Item(ctx, q.parentID)
			if err != nil || !canSeeLibrary(me, it.LibraryID) {
				return nil, 0, nil
			}
			parent = &it
		}
	}
	// A search with no parent across everything, through the search index.
	if parent == nil && parentLib == nil && q.search != "" {
		got, err := s.store.SearchItems(ctx, q.search, q.start+q.limit)
		if err != nil {
			return nil, 0, err
		}
		for _, it := range got {
			if canSeeLibrary(me, it.LibraryID) && q.wants(it.Kind) {
				items = append(items, it)
			}
		}
		items = s.jfDecorate(c, items)
		q.search = ""
		return s.jfFinish(q, items)
	}
	switch {
	case parent != nil:
		items, err = s.jfUnder(c, *parent, q)
	case parentLib != nil:
		items, err = s.jfInLibrary(c, *parentLib, q)
	default:
		for _, l := range libs {
			got, err := s.jfInLibrary(c, l, q)
			if err != nil {
				return nil, 0, err
			}
			items = append(items, got...)
		}
	}
	if err != nil {
		return nil, 0, err
	}
	return s.jfFinish(q, items)
}

// jfUnder lists what is under an item: a series' seasons (or every
// episode, recursively), a season's episodes, an artist's albums (or
// tracks), an album's tracks.
func (s *server) jfUnder(c echo.Context, parent store.Item, q jfQuery) ([]store.Item, error) {
	ctx := c.Request().Context()
	switch parent.Kind {
	case store.KindAlbum, store.KindArtist:
		if parent.Kind == store.KindArtist && !q.recursive && !q.wantsOnly(store.KindTrack) {
			albums, err := s.store.Albums(ctx, userID(c), parent.LibraryID, store.MusicQuery{Artist: parent.ID, Sort: "year", Page: 1, Size: 1000})
			if err != nil {
				return nil, err
			}
			return albums.Items, nil
		}
		return s.store.TracksUnder(ctx, userID(c), parent)
	case store.KindSeries:
		seasons, err := s.store.ItemsByParent(ctx, parent.ID)
		if err != nil {
			return nil, err
		}
		if !q.recursive || q.wantsOnly(store.KindSeason) {
			return s.jfDecorate(c, seasons), nil
		}
		var out []store.Item
		if q.wants(store.KindSeason) {
			out = append(out, seasons...)
		}
		if q.wants(store.KindEpisode) {
			for _, se := range seasons {
				eps, err := s.store.ItemsByParent(ctx, se.ID)
				if err != nil {
					return nil, err
				}
				out = append(out, eps...)
			}
		}
		return s.jfDecorate(c, out), nil
	case store.KindSeason:
		eps, err := s.store.ItemsByParent(ctx, parent.ID)
		if err != nil {
			return nil, err
		}
		return s.jfDecorate(c, eps), nil
	}
	return nil, nil
}

// jfInLibrary lists a library's items as the query asks: a music library
// through the paged listings (already decorated and sorted), a video
// library from its whole list, filtered here.
func (s *server) jfInLibrary(c echo.Context, lib store.Library, q jfQuery) ([]store.Item, error) {
	ctx := c.Request().Context()
	if lib.MediaType == store.MediaMusic {
		kind := store.KindArtist
		switch {
		case q.wantsOnly(store.KindAlbum):
			kind = store.KindAlbum
		case q.wantsOnly(store.KindTrack):
			kind = store.KindTrack
		case q.recursive && !q.wants(store.KindArtist) && q.wants(store.KindAlbum):
			kind = store.KindAlbum
		case q.recursive && !q.wants(store.KindArtist) && !q.wants(store.KindAlbum) && q.wants(store.KindTrack):
			kind = store.KindTrack
		}
		mq := store.MusicQuery{Query: q.search, Sort: jfMusicSort(q.sortBy, kind), Letter: jfLetter(q.nameStartsWith), Page: 1, Size: min(q.start+q.limit, 1000)}
		if q.desc && mq.Sort == "name" {
			mq.Sort = "name" // the store has no reverse name sort; handled in jfFinish
		}
		for _, g := range q.genres {
			mq.Genre = strings.TrimPrefix(g, "id:")
		}
		if len(q.genres) == 1 && strings.HasPrefix(q.genres[0], "id:") {
			mq.Genre = s.jfGenreName(c, lib.ID, strings.TrimPrefix(q.genres[0], "id:"))
		}
		for y := range q.years {
			mq.Year = y
		}
		var page store.MusicPage
		var err error
		switch kind {
		case store.KindAlbum:
			page, err = s.store.Albums(ctx, userID(c), lib.ID, mq)
		case store.KindTrack:
			page, err = s.store.Tracks(ctx, userID(c), lib.ID, mq)
		default:
			page, err = s.store.Artists(ctx, userID(c), lib.ID, mq)
		}
		if err != nil {
			return nil, err
		}
		return page.Items, nil
	}
	var items []store.Item
	var err error
	if len(q.genres) > 0 {
		seen := map[string]bool{}
		for _, g := range q.genres {
			name := g
			if strings.HasPrefix(g, "id:") {
				name = s.jfGenreName(c, lib.ID, strings.TrimPrefix(g, "id:"))
			}
			got, err := s.store.ItemsByGenre(ctx, lib.ID, name)
			if err != nil {
				return nil, err
			}
			for _, it := range got {
				if !seen[it.ID] {
					seen[it.ID] = true
					items = append(items, it)
				}
			}
		}
	} else {
		items, err = s.store.ItemsByLibrary(ctx, lib.ID)
		if err != nil {
			return nil, err
		}
	}
	out := items[:0]
	for _, it := range items {
		top := it.Kind == store.KindMovie || it.Kind == store.KindSeries
		if !q.recursive && !top {
			continue
		}
		if !q.wants(it.Kind) {
			continue
		}
		out = append(out, it)
	}
	return s.jfDecorate(c, out), nil
}

// jfGenreName finds the genre a derived id stands for.
func (s *server) jfGenreName(c echo.Context, libraryID, id string) string {
	genres, err := s.store.LibraryGenres(c.Request().Context(), libraryID)
	if err != nil {
		return ""
	}
	for _, g := range genres {
		if jellyfin.DerivedID("genre", g.Genre) == id {
			return g.Genre
		}
	}
	return ""
}

func jfLetter(prefix string) string {
	if prefix == "" {
		return ""
	}
	return prefix[:1]
}

// jfDecorate stamps the viewer's state onto items, library by library.
func (s *server) jfDecorate(c echo.Context, items []store.Item) []store.Item {
	byLib := map[string][]int{}
	for i, it := range items {
		byLib[it.LibraryID] = append(byLib[it.LibraryID], i)
	}
	for lib, idx := range byLib {
		part := make([]store.Item, 0, len(idx))
		for _, i := range idx {
			part = append(part, items[i])
		}
		part = s.withPlayState(c, lib, part)
		for n, i := range idx {
			items[i] = part[n]
		}
	}
	return items
}

// jfFinish applies the filters a listing shares, sorts, counts and pages.
func (s *server) jfFinish(q jfQuery, items []store.Item) ([]store.Item, int, error) {
	out := items[:0]
	search := strings.ToLower(store.Fold(q.search))
	for _, it := range items {
		if search != "" && !strings.Contains(strings.ToLower(store.Fold(it.Title)), search) {
			continue
		}
		if q.years != nil && !q.years[it.Year] {
			continue
		}
		if q.nameStartsWith != "" && !strings.HasPrefix(it.SortTitle, q.nameStartsWith) && !strings.HasPrefix(strings.ToLower(it.Title), q.nameStartsWith) {
			continue
		}
		if q.isFavorite != nil && (it.UserRating == 5) != *q.isFavorite {
			continue
		}
		if q.isPlayed != nil && it.Watched != *q.isPlayed {
			continue
		}
		skip := false
		for _, f := range q.filters {
			switch strings.ToLower(f) {
			case "isplayed":
				skip = skip || !it.Watched
			case "isunplayed":
				skip = skip || it.Watched
			case "isresumable":
				skip = skip || it.PositionSecs == 0 || it.Watched
			case "isfavorite":
				skip = skip || it.UserRating != 5
			case "isfolder":
				skip = skip || !jellyfin.IsFolder(it.Kind)
			case "isnotfolder":
				skip = skip || jellyfin.IsFolder(it.Kind)
			}
		}
		if skip {
			continue
		}
		out = append(out, it)
	}
	jfSort(out, q.sortBy, q.desc)
	total := len(out)
	if q.start >= len(out) {
		return []store.Item{}, total, nil
	}
	end := min(len(out), q.start+q.limit)
	return out[q.start:end], total, nil
}

// jfSort orders a listing by a Jellyfin sort name; "" keeps the store's
// order (a season's episodes by number).
func jfSort(items []store.Item, sortBy string, desc bool) {
	key := strings.ToLower(sortBy)
	if key == "" {
		if desc {
			for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
				items[i], items[j] = items[j], items[i]
			}
		}
		return
	}
	less := func(a, b store.Item) bool {
		switch key {
		case "datecreated":
			return a.AddedAt < b.AddedAt
		case "premieredate":
			if a.AirDate != b.AirDate {
				return a.AirDate < b.AirDate
			}
			return a.Year < b.Year
		case "productionyear":
			return a.Year < b.Year
		case "communityrating", "criticrating":
			return a.Rating < b.Rating
		case "playcount":
			return a.PlayCount < b.PlayCount
		case "dateplayed", "datelastcontentadded":
			return a.LastPlayedAt+a.WatchedAt < b.LastPlayedAt+b.WatchedAt
		case "runtime":
			return a.RuntimeSecs < b.RuntimeSecs
		case "indexnumber", "parentindexnumber,indexnumber":
			if a.SeasonNum != b.SeasonNum {
				return a.SeasonNum < b.SeasonNum
			}
			if a.EpisodeNum != b.EpisodeNum {
				return a.EpisodeNum < b.EpisodeNum
			}
			return a.TrackNum < b.TrackNum
		case "random":
			return false
		}
		ka, kb := a.SortTitle, b.SortTitle
		if ka == "" {
			ka = store.SortKey(a.Title)
		}
		if kb == "" {
			kb = store.SortKey(b.Title)
		}
		if ka != kb {
			return ka < kb
		}
		return a.ID < b.ID
	}
	if key == "random" {
		sort.SliceStable(items, func(i, j int) bool { return jfShuffleKey(items[i].ID) < jfShuffleKey(items[j].ID) })
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		if desc {
			return less(items[j], items[i])
		}
		return less(items[i], items[j])
	})
}

// jfShuffleKey gives a random listing a different order each minute
// without a random source in the comparator.
func jfShuffleKey(id string) string {
	sum := sha256.Sum256([]byte(id + strconv.FormatInt(time.Now().Unix()/60, 10)))
	return hex.EncodeToString(sum[:8])
}

// jfResult maps a listing into the response shape.
func (s *server) jfResult(c echo.Context, items []store.Item, total, start int) jellyfin.QueryResult {
	m := s.jfMapper(c)
	k := s.jfCache(c)
	res := jellyfin.QueryResult{Items: make([]jellyfin.BaseItem, 0, len(items)), TotalRecordCount: total, StartIndex: start}
	for _, it := range items {
		res.Items = append(res.Items, m.Item(it, k.context(it)))
	}
	return res
}

// GET /Users/{id}/Items and /Items — the listing.
func (s *server) jfItems(c echo.Context) error {
	q := jfReadQuery(c)
	items, total, err := s.jfListing(c, q)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, s.jfResult(c, items, total, q.start))
}

// GET /Artists and /Artists/AlbumArtists — the music libraries' artists.
func (s *server) jfArtists(c echo.Context) error {
	q := jfReadQuery(c)
	q.types, q.anyType, q.recursive = []store.ItemKind{store.KindArtist}, false, true
	libs, err := s.jfLibraries(c)
	if err != nil {
		return err
	}
	var items []store.Item
	for _, l := range libs {
		if l.MediaType != store.MediaMusic || (q.parentID != "" && q.parentID != l.ID) {
			continue
		}
		got, err := s.jfInLibrary(c, l, q)
		if err != nil {
			return err
		}
		items = append(items, got...)
	}
	q.parentID = ""
	items, total, err := s.jfFinish(q, items)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, s.jfResult(c, items, total, q.start))
}

// jfLoad is an item the caller may see, else nil.
func (s *server) jfLoad(c echo.Context, raw string) *store.Item {
	it, err := s.store.Item(c.Request().Context(), jellyfin.ParseID(raw))
	if err != nil || !canSeeLibrary(currentUser(c), it.LibraryID) {
		return nil
	}
	return &it
}

// jfItemParam is the item id a route carries: :item under /Users, else :id.
func jfItemParam(c echo.Context) string {
	if v := c.Param("item"); v != "" {
		return v
	}
	return c.Param("id")
}

// GET /Users/{id}/Items/{id} and /Items/{id} — one item in full, with its
// streams, genres, credits and, for a series or season, how many episodes
// are unplayed.
func (s *server) jfItem(c echo.Context) error {
	ctx := c.Request().Context()
	raw := jfItemParam(c)
	libs, err := s.jfLibraries(c)
	if err != nil {
		return err
	}
	for _, l := range libs {
		if l.ID == jellyfin.ParseID(raw) {
			return c.JSON(http.StatusOK, s.jfMapper(c).Library(l))
		}
	}
	it := s.jfLoad(c, raw)
	if it == nil {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	items := s.jfDecorate(c, []store.Item{*it})
	k := s.jfCache(c)
	cx := k.context(items[0])
	cx.Genres, _ = s.store.GenresForItem(ctx, it.ID)
	cx.Cast, cx.Crew, _ = s.store.CreditsForItem(ctx, it.ID)
	b := s.jfMapper(c).Item(items[0], cx)
	if it.Kind == store.KindMovie || it.Kind == store.KindEpisode {
		audio, _ := s.store.AudioTracksForItem(ctx, it.ID)
		subs, _ := s.store.SubtitlesForItem(ctx, it.ID)
		streams := jellyfin.Streams(*it, audio, subs, s.jfSubtitleURL(c, *it))
		b.MediaStreams = streams
		b.MediaSources = []jellyfin.MediaSource{jellyfin.Source(*it, streams, "")}
	}
	if it.Kind == store.KindSeries || it.Kind == store.KindSeason {
		if n, total, ok := s.jfUnplayed(c, *it); ok {
			b.UserData.UnplayedItemCount = &n
			b.UserData.Played = total > 0 && n == 0
			b.RecursiveItemCount = total
			if b.ChildCount == 0 {
				b.ChildCount = total
			}
		}
	}
	return c.JSON(http.StatusOK, b)
}

// jfUnplayed counts a series' or season's episodes the viewer has not
// watched, and all of them.
func (s *server) jfUnplayed(c echo.Context, it store.Item) (unplayed, total int, ok bool) {
	ctx := c.Request().Context()
	var eps []store.Item
	switch it.Kind {
	case store.KindSeason:
		eps, _ = s.store.ItemsByParent(ctx, it.ID)
	case store.KindSeries:
		seasons, _ := s.store.ItemsByParent(ctx, it.ID)
		for _, se := range seasons {
			got, _ := s.store.ItemsByParent(ctx, se.ID)
			eps = append(eps, got...)
		}
	default:
		return 0, 0, false
	}
	eps = s.withPlayState(c, it.LibraryID, eps)
	for _, e := range eps {
		if !e.Watched {
			unplayed++
		}
	}
	return unplayed, len(eps), true
}

// GET /Items/{id}/Ancestors — the chain above an item, nearest first,
// ending with its library.
func (s *server) jfAncestors(c echo.Context) error {
	it := s.jfLoad(c, c.Param("id"))
	if it == nil {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	m := s.jfMapper(c)
	k := s.jfCache(c)
	out := []jellyfin.BaseItem{}
	cur := it
	for i := 0; i < 4 && cur.ParentID != ""; i++ {
		cur = k.get(cur.ParentID)
		if cur == nil {
			break
		}
		out = append(out, m.Item(*cur, k.context(*cur)))
	}
	if lib, err := s.store.Library(c.Request().Context(), it.LibraryID); err == nil {
		out = append(out, m.Library(lib))
	}
	return c.JSON(http.StatusOK, out)
}

// GET /Users/{id}/Items/Latest — recently added, per library or across
// them. A plain list, as Jellyfin answers it.
func (s *server) jfLatest(c echo.Context) error {
	ctx := c.Request().Context()
	limit := min(max(jellyfin.Atoi(c.QueryParam("Limit"), 16), 1), 100)
	parent := jellyfin.ParseID(c.QueryParam("ParentId"))
	types := jfReadQuery(c)
	libs, err := s.jfLibraries(c)
	if err != nil {
		return err
	}
	var items []store.Item
	for _, l := range libs {
		if parent != "" && parent != l.ID {
			continue
		}
		got, err := s.store.RecentlyAdded(ctx, l, limit)
		if err != nil {
			return err
		}
		for _, it := range got {
			if types.wants(it.Kind) {
				items = append(items, it)
			}
		}
	}
	items = s.jfDecorate(c, items)
	if strings.EqualFold(c.QueryParam("IsPlayed"), "false") {
		kept := items[:0]
		for _, it := range items {
			if !it.Watched {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	jfSort(items, "DateCreated", true)
	if len(items) > limit {
		items = items[:limit]
	}
	return c.JSON(http.StatusOK, s.jfResult(c, items, len(items), 0).Items)
}

// GET /Users/{id}/Items/Resume — part-watched films and episodes.
func (s *server) jfResume(c echo.Context) error {
	me := currentUser(c)
	limit := min(max(jellyfin.Atoi(c.QueryParam("Limit"), 12), 1), 100)
	rows, err := s.store.ContinueWatching(c.Request().Context(), me.ID, visibleLibraryIDs(me), limit)
	if err != nil {
		return err
	}
	q := jfReadQuery(c)
	m := s.jfMapper(c)
	k := s.jfCache(c)
	res := jellyfin.EmptyResult()
	for _, r := range rows {
		if !q.wants(r.Kind) || (q.parentID != "" && q.parentID != r.LibraryID) {
			continue
		}
		it := r.Item
		it.PositionSecs = r.PositionSecs
		res.Items = append(res.Items, m.Item(it, k.context(it)))
	}
	res.TotalRecordCount = len(res.Items)
	return c.JSON(http.StatusOK, res)
}

// GET /Shows/NextUp — the next episode of a series, or of each series in
// progress, from Cineva's own next-up logic.
func (s *server) jfNextUp(c echo.Context) error {
	ctx := c.Request().Context()
	me := currentUser(c)
	limit := min(max(jellyfin.Atoi(c.QueryParam("Limit"), 12), 1), 100)
	m := s.jfMapper(c)
	k := s.jfCache(c)
	res := jellyfin.EmptyResult()
	add := func(seriesID string) error {
		series := k.get(seriesID)
		if series == nil {
			return nil
		}
		up, ok, err := s.store.NextUp(ctx, me.ID, seriesID)
		if err != nil || !ok || up.Reason == "restart" {
			return err
		}
		it := s.jfDecorate(c, []store.Item{up.Item})[0]
		it.PositionSecs = up.PositionSecs
		res.Items = append(res.Items, m.Item(it, k.context(it)))
		return nil
	}
	if sid := jellyfin.ParseID(c.QueryParam("SeriesId")); sid != "" {
		if err := add(sid); err != nil {
			return err
		}
	} else {
		series, err := s.store.RecentSeries(ctx, me.ID, limit)
		if err != nil {
			return err
		}
		for _, sr := range series {
			if !canSeeLibrary(me, sr.LibraryID) {
				continue
			}
			if err := add(sr.ID); err != nil {
				return err
			}
			if len(res.Items) >= limit {
				break
			}
		}
	}
	res.TotalRecordCount = len(res.Items)
	return c.JSON(http.StatusOK, res)
}

// GET /Shows/{id}/Seasons
func (s *server) jfSeasons(c echo.Context) error {
	series := s.jfLoad(c, c.Param("id"))
	if series == nil || series.Kind != store.KindSeries {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	seasons, err := s.store.ItemsByParent(c.Request().Context(), series.ID)
	if err != nil {
		return err
	}
	seasons = s.jfDecorate(c, seasons)
	k := s.jfCache(c)
	k.put(*series)
	m := s.jfMapper(c)
	res := jellyfin.EmptyResult()
	for _, se := range seasons {
		b := m.Item(se, k.context(se))
		if n, total, ok := s.jfUnplayed(c, se); ok {
			b.UserData.UnplayedItemCount = &n
			b.UserData.Played = total > 0 && n == 0
			b.ChildCount = total
		}
		res.Items = append(res.Items, b)
	}
	res.TotalRecordCount = len(res.Items)
	return c.JSON(http.StatusOK, res)
}

// GET /Shows/{id}/Episodes?SeasonId= — a season's episodes, or every
// episode of the series.
func (s *server) jfEpisodes(c echo.Context) error {
	ctx := c.Request().Context()
	series := s.jfLoad(c, c.Param("id"))
	if series == nil || series.Kind != store.KindSeries {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	var eps []store.Item
	var err error
	if sid := jellyfin.ParseID(c.QueryParam("SeasonId")); sid != "" {
		eps, err = s.store.ItemsByParent(ctx, sid)
	} else if n := c.QueryParam("Season"); n != "" {
		seasons, _ := s.store.ItemsByParent(ctx, series.ID)
		for _, se := range seasons {
			if se.SeasonNum == jellyfin.Atoi(n, -1) {
				eps, err = s.store.ItemsByParent(ctx, se.ID)
			}
		}
	} else {
		seasons, serr := s.store.ItemsByParent(ctx, series.ID)
		if serr != nil {
			return serr
		}
		for _, se := range seasons {
			got, gerr := s.store.ItemsByParent(ctx, se.ID)
			if gerr != nil {
				return gerr
			}
			eps = append(eps, got...)
		}
	}
	if err != nil {
		return err
	}
	eps = s.jfDecorate(c, eps)
	q := jfReadQuery(c)
	q.sortBy = ""
	items, total, err := s.jfFinish(q, eps)
	if err != nil {
		return err
	}
	k := s.jfCache(c)
	k.put(*series)
	m := s.jfMapper(c)
	res := jellyfin.QueryResult{Items: []jellyfin.BaseItem{}, TotalRecordCount: total, StartIndex: q.start}
	for _, e := range items {
		res.Items = append(res.Items, m.Item(e, k.context(e)))
	}
	return c.JSON(http.StatusOK, res)
}

// GET /Items/{id}/Similar — titles sharing the item's first genre.
func (s *server) jfSimilar(c echo.Context) error {
	ctx := c.Request().Context()
	it := s.jfLoad(c, c.Param("id"))
	if it == nil {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	limit := min(max(jellyfin.Atoi(c.QueryParam("Limit"), 12), 1), 50)
	genres, _ := s.store.GenresForItem(ctx, it.ID)
	res := jellyfin.EmptyResult()
	if len(genres) == 0 {
		return c.JSON(http.StatusOK, res)
	}
	got, err := s.store.ItemsByGenre(ctx, it.LibraryID, genres[0])
	if err != nil {
		return err
	}
	var items []store.Item
	for _, g := range got {
		if g.ID != it.ID && g.Kind == it.Kind {
			items = append(items, g)
		}
	}
	jfSort(items, "Random", false)
	if len(items) > limit {
		items = items[:limit]
	}
	items = s.jfDecorate(c, items)
	return c.JSON(http.StatusOK, s.jfResult(c, items, len(items), 0))
}

// GET /Genres?ParentId= — a library's genres as items.
func (s *server) jfGenres(c echo.Context) error {
	ctx := c.Request().Context()
	libs, err := s.jfLibraries(c)
	if err != nil {
		return err
	}
	parent := jellyfin.ParseID(c.QueryParam("ParentId"))
	seen := map[string]bool{}
	var names []string
	for _, l := range libs {
		if parent != "" && parent != l.ID {
			continue
		}
		genres, err := s.store.LibraryGenres(ctx, l.ID)
		if err != nil {
			return err
		}
		for _, g := range genres {
			if !seen[g.Genre] {
				seen[g.Genre] = true
				names = append(names, g.Genre)
			}
		}
	}
	sort.Strings(names)
	m := s.jfMapper(c)
	res := jellyfin.EmptyResult()
	for _, n := range names {
		res.Items = append(res.Items, m.Genre(n))
	}
	res.TotalRecordCount = len(res.Items)
	return c.JSON(http.StatusOK, res)
}

func (s *server) jfGenreNames(c echo.Context) ([]string, error) {
	ctx := c.Request().Context()
	libs, err := s.jfLibraries(c)
	if err != nil {
		return nil, err
	}
	parent := jellyfin.ParseID(c.QueryParam("ParentId"))
	seen := map[string]bool{}
	var names []string
	for _, l := range libs {
		if parent != "" && parent != l.ID {
			continue
		}
		genres, err := s.store.LibraryGenres(ctx, l.ID)
		if err != nil {
			return nil, err
		}
		for _, g := range genres {
			if !seen[g.Genre] {
				seen[g.Genre] = true
				names = append(names, g.Genre)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

// GET /Items/Filters2 and /Items/Filters — what a library can be filtered
// by: its genres.
func (s *server) jfFilters(c echo.Context) error {
	names, err := s.jfGenreNames(c)
	if err != nil {
		return err
	}
	out := jellyfin.QueryFilters{Genres: []jellyfin.NameGUIDPair{}, Tags: []string{}}
	for _, n := range names {
		out.Genres = append(out.Genres, jellyfin.NameGUIDPair{Name: n, ID: jellyfin.DerivedID("genre", n)})
	}
	return c.JSON(http.StatusOK, out)
}

func (s *server) jfFilters2(c echo.Context) error {
	names, err := s.jfGenreNames(c)
	if err != nil {
		return err
	}
	if names == nil {
		names = []string{}
	}
	return c.JSON(http.StatusOK, jellyfin.QueryFiltersLegacy{Genres: names, Tags: []string{}, OfficialRatings: []string{}, Years: []int{}})
}

// GET /Search/Hints?searchTerm=
func (s *server) jfSearchHints(c echo.Context) error {
	term := strings.TrimSpace(c.QueryParam("searchTerm"))
	if term == "" {
		term = strings.TrimSpace(c.QueryParam("SearchTerm"))
	}
	limit := min(max(jellyfin.Atoi(c.QueryParam("Limit"), 20), 1), 100)
	res := jellyfin.SearchHintResult{SearchHints: []jellyfin.SearchHint{}}
	if len(term) < 2 {
		return c.JSON(http.StatusOK, res)
	}
	q := jfReadQuery(c)
	got, err := s.store.SearchItems(c.Request().Context(), term, limit)
	if err != nil {
		return err
	}
	me := currentUser(c)
	m := s.jfMapper(c)
	k := s.jfCache(c)
	for _, it := range got {
		if !canSeeLibrary(me, it.LibraryID) || !q.wants(it.Kind) {
			continue
		}
		res.SearchHints = append(res.SearchHints, m.Hint(it, k.context(it)))
	}
	res.TotalRecordCount = len(res.SearchHints)
	return c.JSON(http.StatusOK, res)
}

func (s *server) jfThemeMedia(c echo.Context) error {
	return c.JSON(http.StatusOK, jellyfin.ThemeMedia{
		ThemeVideosResult: jellyfin.EmptyResult(), ThemeSongsResult: jellyfin.EmptyResult(), SoundtrackSongsResult: jellyfin.EmptyResult(),
	})
}

// ---- Images ----

// GET /Items/{id}/Images/{type} — the poster or backdrop file. Sizes are
// not applied yet: the apps accept any size.
func (s *server) jfItemImage(c echo.Context) error {
	it := s.jfLoad(c, c.Param("id"))
	var path string
	if it != nil {
		switch strings.ToLower(c.Param("type")) {
		case "primary":
			path = it.Poster
		case "backdrop", "thumb", "art", "banner", "screenshot":
			path = it.Backdrop
			if path == "" && strings.ToLower(c.Param("type")) == "thumb" {
				path = it.Poster
			}
		}
	} else if lib, err := s.store.Library(c.Request().Context(), jellyfin.ParseID(c.Param("id"))); err == nil && lib.ID != "" {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	file, ok := s.ssArtworkFile(path)
	if !ok {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	c.Response().Header().Set("Cache-Control", "public, max-age=86400")
	return c.File(file)
}

// GET /Users/{id}/Images/Primary — the avatar.
func (s *server) jfUserImage(c echo.Context) error {
	u, err := s.store.UserByID(c.Request().Context(), jellyfin.ParseID(c.Param("id")))
	if err != nil {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	file, ok := s.ssArtworkFile(u.Avatar)
	if !ok {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	return c.File(file)
}
