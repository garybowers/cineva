package api

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/playback"
	"github.com/garybowers/cineva/server/internal/stats"
	"github.com/garybowers/cineva/server/internal/store"
	"github.com/garybowers/cineva/server/internal/version"
	"github.com/garybowers/cineva/server/ui"
)

type Config struct {
	Host         string
	Port         int
	ArtworkDir   string
	TranscodeDir string
	SegmentSecs  int
	SampleSecs   int // host CPU/RAM sampling interval for the admin dashboard
	SessionDays  int
	SecureCookie bool
	LoginMode    string // "picker" | "form" default for the sign-in screen
}

type server struct {
	store            *store.Store
	manager          *library.Manager
	sessions         *media.SessionManager
	events           *events.Bus
	sampler          *stats.Sampler
	playbacks        *playback.Registry
	sessionTTL       time.Duration
	throttle         *auth.Throttle
	defaultLoginMode string // from config; the database setting overrides
	secureCookies    bool
	subtitleDir      string
	dlCacheDir       string
}

func Serve(cfg Config, st *store.Store, manager *library.Manager) error {
	s := &server{
		store:            st,
		manager:          manager,
		sessions:         media.NewSessionManager(cfg.TranscodeDir, cfg.SegmentSecs),
		events:           events.NewBus(),
		sampler:          stats.New(time.Duration(cfg.SampleSecs) * time.Second),
		playbacks:        playback.NewRegistry(45 * time.Second),
		sessionTTL:       time.Duration(max(cfg.SessionDays, 1)) * 24 * time.Hour,
		secureCookies:    cfg.SecureCookie,
		throttle:         auth.NewThrottle(),
		defaultLoginMode: cfg.LoginMode,
		subtitleDir:      filepath.Join(cfg.TranscodeDir, "subtitles"),
		dlCacheDir:       filepath.Join(cfg.TranscodeDir, "dlcache"),
	}
	// Scans run by this server publish progress onto the same bus the
	// /events stream reads from. CLI scans leave manager.Events nil.
	manager.Events = s.events

	// Host resource sampling for the dashboard; each sample is also pushed
	// over the event stream so the graphs move live.
	samplerCtx, stopSampler := context.WithCancel(context.Background())
	s.sampler.OnSample = func(smp stats.Sample) { s.events.Publish(events.Event{Type: "stats", Data: smp}) }
	go s.sampler.Run(samplerCtx)

	// Active playbacks: any change (start, heartbeat, stop, idle reap) tells
	// the UIs to refetch the sessions list.
	s.playbacks.OnChange = func() { s.events.Publish(events.Event{Type: "sessions"}) }
	go s.playbacks.Run(samplerCtx)

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover(), middleware.Logger())

	// Public: liveness, themes, and the sign-in / first-run endpoints.
	pub := e.Group("/api/v1")
	pub.GET("/health", health)
	pub.GET("/themes", listThemes)
	pub.GET("/auth/status", s.authStatusHandler)
	pub.POST("/auth/login", s.authLogin)
	pub.POST("/auth/setup", s.authSetup)
	pub.GET("/auth/profiles", s.authProfiles)
	pub.POST("/auth/pin", s.authPINLogin)

	// Signed in (session cookie or API key). Library-scoped handlers also
	// check the caller's grants.
	v1 := e.Group("/api/v1", s.authenticate)
	v1.POST("/auth/logout", s.authLogout)
	v1.GET("/auth/me", s.authMe)
	v1.POST("/auth/password", s.authPassword)
	v1.POST("/auth/pin/set", s.authSetPIN)
	v1.GET("/auth/keys", s.listAPIKeys)
	v1.POST("/auth/keys", s.createAPIKey)
	v1.DELETE("/auth/keys/:id", s.deleteAPIKey)
	v1.GET("/libraries", s.listLibraries)
	v1.GET("/libraries/:id", s.getLibrary)
	v1.GET("/libraries/:id/items", s.libraryItems)
	v1.GET("/items/:id", s.getItem)
	v1.GET("/search", s.search)
	v1.GET("/events", s.eventStream)

	// Playback (milestone 3)
	v1.POST("/items/:id/play", s.playItem)
	v1.GET("/items/:id/stream", s.streamItem)
	v1.GET("/items/:id/download", s.downloadItem)
	v1.POST("/items/:id/progress", s.postProgress)
	v1.DELETE("/items/:id/playback", s.stopPlayback)
	v1.POST("/items/:id/playback/stop", s.stopPlayback) // sendBeacon-friendly alias
	v1.GET("/items/:id/next", s.nextUp)
	v1.GET("/items/:id/subtitles", s.itemSubtitles)
	v1.GET("/subtitles/:file", s.subtitleVTT) // "<track-id>.vtt"
	v1.GET("/continue-watching", s.continueWatching)
	v1.GET("/play/:sid/index.m3u8", s.hlsPlaylist)
	v1.GET("/play/:sid/:file", s.hlsFile)

	// Admin only.
	adm := v1.Group("", requireAdmin)
	adm.POST("/libraries", s.createLibrary)
	adm.DELETE("/libraries/:id", s.deleteLibrary)
	adm.POST("/libraries/:id/scan", s.scanLibrary)
	adm.GET("/scans", s.listScans)
	adm.POST("/scans/:id/cancel", s.cancelScan)
	adm.GET("/fs/dirs", s.listDirs)
	adm.GET("/stats/system", s.systemStats)
	adm.GET("/stats/plays", s.playStats)
	adm.GET("/stats/disks", s.diskStats)
	adm.GET("/logs", s.auditLog)
	adm.GET("/play/sessions", s.listSessions)
	adm.DELETE("/play/:sid", s.stopSession)
	adm.GET("/settings", s.getSettings)
	adm.PUT("/settings", s.putSettings)
	adm.GET("/users", s.listUsers)
	adm.POST("/users", s.createUser)
	adm.PATCH("/users/:id", s.updateUser)
	adm.DELETE("/users/:id", s.deleteUser)

	e.StaticFS("/themes", ui.Themes())
	if cfg.ArtworkDir != "" {
		e.Static("/artwork", cfg.ArtworkDir) // posters only; the UI needs them before sign-in resolves
	}

	// Admin UI shares the main port at /admin. Do not alias /settings here —
	// that path belongs to the client app's own settings page.
	e.StaticFS("/admin", ui.Admin())
	e.StaticFS("/", ui.Client())

	// SPA fallback: the client owns URL routing (/libraries/..., /settings),
	// so unknown extension-less GETs outside the API get index.html.
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		he, ok := err.(*echo.HTTPError)
		p := c.Request().URL.Path
		if ok && he.Code == http.StatusNotFound && c.Request().Method == http.MethodGet &&
			!strings.ContainsRune(path.Base(p), '.') &&
			!hasAnyPrefix(p, "/api/", "/admin", "/themes/", "/artwork/") {
			if data, ferr := fs.ReadFile(ui.Client(), "index.html"); ferr == nil {
				_ = c.HTMLBlob(http.StatusOK, data)
				return
			}
		}
		e.DefaultHTTPErrorHandler(err, c)
	}

	// Graceful shutdown: SIGINT/SIGTERM stops HTTP, then kills transcode
	// sessions and removes their temp dirs.
	shutdownDone := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := e.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		stopSampler()
		s.sessions.Close()
		close(shutdownDone)
	}()

	err := e.Start(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	if err == http.ErrServerClosed {
		<-shutdownDone
		return nil
	}
	return err
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func health(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "cineva",
		"version": version.Version,
	})
}
