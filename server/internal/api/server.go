package api

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/playback"
	"github.com/garybowers/cineva/server/internal/segments"
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
	HWAccel      string // "auto" | "none" | "vaapi" | "nvenc" | "qsv"
	TranscodeMax int64  // segment-cache ceiling in bytes; 0 disables the cap
	SampleSecs   int    // host CPU/RAM sampling interval for the admin dashboard
	StatsHours   int    // how long persisted dashboard samples are kept
	SessionDays  int
	SecureCookie bool
	LoginMode    string // "picker" | "form" default for the sign-in screen
	// CertRegion is the ISO 3166-1 country whose content rating is stored.
	// Config supplies the default; the admin setting overrides it.
	CertRegion string
	// ScanInterval rescans every library on a timer; zero disables it.
	ScanInterval time.Duration
	// RetainDays caps how much audit and play history is kept; zero keeps
	// everything.
	RetainDays int
	// IntroDetect runs intro detection over TV libraries in the background.
	IntroDetect bool
}

type server struct {
	store             *store.Store
	manager           *library.Manager
	sessions          *media.SessionManager
	events            *events.Bus
	sampler           *stats.Sampler
	cfgStatsHours     int
	playbacks         *playback.Registry
	sessionTTL        time.Duration
	throttle          *auth.Throttle
	defaultLoginMode  string // from config; the database setting overrides
	defaultCertRegion string
	secureCookies     bool
	subtitleDir       string
	dlCacheDir        string
	trickplayDir      string
	trickplay         *trickplayJobs
	segments          *segments.Detector // nil when intro detection is off
	storage           *library.StorageMonitor

	// Library-wide metadata refreshes in flight, keyed by library id.
	refreshMu sync.Mutex
	refreshes map[string]*refreshJob
}

func Serve(cfg Config, st *store.Store, manager *library.Manager) error {
	s := &server{
		store:             st,
		manager:           manager,
		sessions:          media.NewSessionManager(cfg.TranscodeDir, cfg.SegmentSecs),
		events:            events.NewBus(),
		sampler:           stats.New(time.Duration(cfg.SampleSecs) * time.Second),
		cfgStatsHours:     cfg.StatsHours,
		playbacks:         playback.NewRegistry(45 * time.Second),
		sessionTTL:        time.Duration(max(cfg.SessionDays, 1)) * 24 * time.Hour,
		secureCookies:     cfg.SecureCookie,
		throttle:          auth.NewThrottle(),
		defaultLoginMode:  cfg.LoginMode,
		defaultCertRegion: cfg.CertRegion,
		subtitleDir:       filepath.Join(cfg.TranscodeDir, "subtitles"),
		dlCacheDir:        filepath.Join(cfg.TranscodeDir, "dlcache"),
		trickplayDir:      filepath.Join(cfg.TranscodeDir, "trickplay"),
		trickplay:         newTrickplayJobs(),
		refreshes:         map[string]*refreshJob{},
	}
	// Transcode sessions survive a restart: the segments are on disk and
	// expensive to remake, so the stored descriptions are re-registered and
	// the directories they own are spared by the sweep below. A restored
	// session transcodes nothing until a segment is asked for.
	s.sessions.Persist = func(state media.SessionState) {
		if err := st.SaveTranscodeSession(context.Background(), toStoreSession(state)); err != nil {
			log.Printf("persist session: %v", err)
		}
	}
	s.sessions.Forget = func(id string) {
		if err := st.DeleteTranscodeSession(context.Background(), id); err != nil {
			log.Printf("forget session: %v", err)
		}
	}
	var keep []string
	if stored, err := st.TranscodeSessions(context.Background()); err != nil {
		log.Printf("restore sessions: %v", err)
	} else {
		states := make([]media.SessionState, 0, len(stored))
		for _, t := range stored {
			states = append(states, fromStoreSession(t))
		}
		keep = s.sessions.Restore(states)
	}

	// Everything else in the transcode directory is an orphan from a
	// previous run: sessions died with the process, their segments did not.
	if n, freed, err := media.SweepOrphans(cfg.TranscodeDir, keep...); err != nil {
		log.Printf("transcode sweep: %v", err)
	} else if n > 0 {
		log.Printf("transcode: removed %d orphaned session directory(ies), freed %d MiB", n, freed>>20)
	}

	// Probe for a hardware transcoder once at startup rather than per
	// session: opening a GPU device is slow, and the result cannot change
	// while the process runs. A nil result means software, which is a
	// supported outcome — DetectHW logs which path was taken and why.
	s.sessions.MaxBytes = cfg.TranscodeMax

	hw := media.DetectHW(cfg.HWAccel)
	defer hw.Close()
	s.sessions.HW = hw
	// The admin switch is persisted, so honour it from the first session
	// rather than only after someone visits the settings page.
	if v, err := st.Setting(context.Background(), settingHWAccel, "true"); err == nil {
		s.sessions.SetHWEnabled(v != "false")
	}

	// The scanner stamps the configured region's content rating onto items
	// as it matches them, so it has to know the setting before the first
	// scan rather than only after someone opens the settings page.
	if v, err := st.Setting(context.Background(), settingCertRegion, cfg.CertRegion); err == nil && v != "" {
		manager.CertRegion = v
	} else {
		manager.CertRegion = defaultCertRegion
	}

	// Scans run by this server publish progress onto the same bus the
	// /events stream reads from. CLI scans leave manager.Events nil.
	manager.Events = s.events

	// Host resource sampling for the dashboard; each sample is also pushed
	// over the event stream so the graphs move live.
	samplerCtx, stopSampler := context.WithCancel(context.Background())
	s.sampler.OnSample = func(smp stats.Sample) { s.events.Publish(events.Event{Type: "stats", Data: smp}) }
	// Persist the coarse series so the dashboard's charts survive a restart.
	s.sampler.Store = statsStore{s.store}
	s.sampler.Retain = time.Duration(max(s.cfgStatsHours, 1)) * time.Hour
	go s.sampler.Run(samplerCtx)

	// Periodic rescans and history pruning. Both are off by default: a
	// server nobody has configured should not start rewriting rows on a
	// timer. Shares the sampler's context, so shutdown stops it too.
	go (&library.Scheduler{
		Manager: manager, Store: st,
		Interval: cfg.ScanInterval, Retain: cfg.RetainDays,
	}).Run(samplerCtx)

	// Storage is watched so requests can fail at once, with a reason, when a
	// library's share is down — and so every client is told when one goes
	// or comes back, through the library list it already refetches.
	s.storage = library.NewStorageMonitor(st)
	s.storage.OnChange = func(lib store.Library, st library.StorageStatus) {
		s.events.Publish(events.Event{Type: "libraries"})
		// Detection skipped this library while it was down; catch up.
		if st.Available && lib.MediaType == store.MediaTV && s.segments != nil {
			s.segments.Enqueue(lib.ID)
		}
	}
	manager.Storage = s.storage
	go s.storage.Run(samplerCtx)

	// Intro detection follows every completed TV scan, and runs once at
	// startup so a library scanned before it existed (or while the server
	// was down) is caught up. Incremental either way: only episodes not
	// yet analysed are decoded.
	if cfg.IntroDetect {
		s.segments = segments.NewDetector(st)
		s.segments.Available = func(libraryID string) bool { return s.storage.Status(libraryID).Available }
		// Detection reads a lot from the same share playback streams from,
		// so it gives way to anyone watching and to scans.
		s.segments.Busy = func() string {
			if len(s.playbacks.List()) > 0 {
				return "playing"
			}
			if manager.AnyScanning() {
				return "scanning"
			}
			return ""
		}
		if v, err := st.Setting(context.Background(), settingDetectionPaused, "false"); err == nil {
			s.segments.SetPaused(v == "true")
		}
		s.segments.SetRateLimit(int64(s.detectionRateMB(context.Background())) * 1_000_000)
		s.segments.OnProgress = func(status segments.Status) {
			s.events.Publish(events.Event{Type: "segments", Data: status})
		}
		manager.OnScanDone = func(lib store.Library) {
			if lib.MediaType == store.MediaTV {
				s.segments.Enqueue(lib.ID)
			}
		}
		if libs, err := st.Libraries(context.Background()); err == nil {
			for _, lib := range libs {
				if lib.MediaType == store.MediaTV {
					s.segments.Enqueue(lib.ID)
				}
			}
		}
		go s.segments.Run(samplerCtx)
	}

	// Active playbacks: any change (start, heartbeat, stop, idle reap) tells
	// the UIs to refetch the sessions list.
	s.playbacks.OnChange = func() { s.events.Publish(events.Event{Type: "sessions"}) }
	// Closes each playback's history row, so the dashboard can tell a play
	// that was watched from one that was abandoned after ten seconds.
	s.playbacks.OnEnd = s.onPlaybackEnd
	go s.playbacks.Run(samplerCtx)

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover(), middleware.Logger())

	// Compression. JSON and the UI bundles are the bulk of what a browser
	// fetches and they shrink by roughly three quarters; a library's item
	// list goes from 1.1 MB to under 300 KB, which is the difference between
	// usable and not on a phone. Media and artwork are skipped: they are
	// already compressed, so deflating them again burns CPU the transcoder
	// wants for nothing.
	e.Use(middleware.GzipWithConfig(middleware.GzipConfig{
		Level: 5, // compresses JSON within a few per cent of best, far cheaper
		// Below about a packet there is nothing to win and the gzip header
		// can make the response larger.
		MinLength: 1024,
		Skipper:   incompressible,
	}))

	// Public: liveness, themes, and the sign-in / first-run endpoints.
	pub := e.Group("/api/v1")
	pub.GET("/health", health)
	pub.GET("/themes", listThemes)
	pub.GET("/auth/status", s.authStatusHandler)
	pub.POST("/auth/login", s.authLogin)
	pub.POST("/auth/setup", s.authSetup)
	pub.GET("/auth/profiles", s.authProfiles)
	pub.POST("/auth/pin", s.authPINLogin)
	// Sonarr and Radarr deliver here, signing in with the connection's own
	// webhook secret rather than as a user.
	pub.POST("/integrations/:id/webhook", s.integrationWebhook)

	// Signed in (session cookie or API key). Library-scoped handlers also
	// check the caller's grants.
	v1 := e.Group("/api/v1", s.authenticate)
	v1.POST("/auth/logout", s.authLogout)
	v1.GET("/auth/me", s.authMe)
	v1.POST("/auth/password", s.authPassword)
	v1.POST("/auth/pin/set", s.authSetPIN)
	v1.GET("/auth/prefs", s.getPrefs)
	v1.PUT("/auth/prefs", s.putPrefs)
	v1.GET("/auth/keys", s.listAPIKeys)
	v1.POST("/auth/keys", s.createAPIKey)
	v1.DELETE("/auth/keys/:id", s.deleteAPIKey)
	v1.GET("/libraries", s.listLibraries)
	v1.GET("/libraries/:id", s.getLibrary)
	v1.GET("/libraries/:id/items", s.libraryItems)
	v1.GET("/libraries/:id/genres", s.libraryGenres)
	v1.GET("/libraries/:id/recommended", s.libraryRecommended)
	v1.GET("/libraries/:id/collections", s.libraryCollections)
	v1.GET("/libraries/:id/collections/:key", s.collectionItems)
	v1.GET("/people/:id", s.getPerson)
	v1.GET("/items/:id", s.getItem)
	v1.GET("/search", s.search)
	v1.GET("/events", s.eventStream)

	// Playback (milestone 3)
	v1.POST("/items/:id/play", s.playItem)
	v1.GET("/items/:id/stream", s.streamItem)
	v1.GET("/items/:id/download", s.downloadItem)
	v1.POST("/items/:id/progress", s.postProgress)
	v1.PUT("/items/:id/watched", s.putWatched)
	v1.DELETE("/items/:id/playback", s.stopPlayback)
	v1.POST("/items/:id/playback/stop", s.stopPlayback) // sendBeacon-friendly alias
	v1.GET("/items/:id/next", s.nextUp)
	v1.GET("/items/:id/next-up", s.nextUpFor)
	v1.GET("/items/:id/prev", s.prevEpisode)
	v1.GET("/items/:id/subtitles", s.itemSubtitles)
	v1.GET("/items/:id/trickplay.vtt", s.trickplayVTT)
	v1.GET("/items/:id/trickplay.jpg", s.trickplaySheet)
	v1.GET("/subtitles/:file", s.subtitleVTT) // "<track-id>.vtt"
	v1.GET("/continue-watching", s.continueWatching)
	v1.GET("/home", s.home)
	v1.GET("/play/:sid/index.m3u8", s.hlsPlaylist)
	v1.GET("/play/:sid/:file", s.hlsFile)

	// Admin only.
	adm := v1.Group("", requireAdmin)
	adm.POST("/libraries", s.createLibrary)
	adm.PATCH("/libraries/:id", s.updateLibrary)
	adm.DELETE("/libraries/:id", s.deleteLibrary)
	adm.POST("/libraries/:id/scan", s.scanLibrary)
	adm.GET("/scans", s.listScans)
	adm.GET("/segments/status", s.segmentStatus)
	adm.POST("/segments/pause", s.pauseSegments)
	adm.POST("/segments/resume", s.resumeSegments)
	adm.POST("/scans/:id/cancel", s.cancelScan)
	adm.GET("/fs/dirs", s.listDirs)
	adm.GET("/integrations", s.listIntegrations)
	adm.POST("/integrations", s.createIntegration)
	adm.PATCH("/integrations/:id", s.updateIntegration)
	adm.DELETE("/integrations/:id", s.deleteIntegration)
	// Metadata management: inspect a match, search for the right title,
	// apply it, or re-run matching.
	// Collection management: override the provider's grouping — rename,
	// add/remove films, reorder, or create one outright.
	adm.POST("/libraries/:id/collections", s.createCollection)
	adm.PATCH("/libraries/:id/collections/:key", s.renameCollection)
	adm.PUT("/libraries/:id/collections/:key/items", s.setCollectionItems)
	adm.DELETE("/libraries/:id/collections/:key", s.deleteCollection)
	adm.GET("/items/:id/matches", s.itemMatches)
	adm.PUT("/items/:id/match", s.putItemMatch)
	adm.POST("/items/:id/refresh", s.refreshItem)
	adm.PATCH("/items/:id", s.updateItemMetadata)
	adm.DELETE("/items/:id/locks", s.clearItemLocks)
	adm.POST("/libraries/:id/refresh", s.refreshLibrary)
	adm.DELETE("/libraries/:id/refresh", s.cancelRefresh)
	adm.GET("/libraries/:id/refresh", s.refreshStatus)
	adm.GET("/stats/system", s.systemStats)
	adm.GET("/stats/plays", s.playStats)
	adm.GET("/stats/plays/totals", s.playTotals)
	adm.GET("/stats/disks", s.diskStats)
	adm.GET("/logs", s.auditLog)
	adm.GET("/play/sessions", s.listSessions)
	adm.DELETE("/play/:sid", s.stopSession)
	adm.DELETE("/playbacks/:userId/:itemId", s.stopPlaybackAdmin)
	adm.GET("/settings", s.getSettings)
	adm.PUT("/settings", s.putSettings)
	adm.GET("/users", s.listUsers)
	adm.POST("/users", s.createUser)
	adm.PATCH("/users/:id", s.updateUser)
	adm.DELETE("/users/:id", s.deleteUser)
	adm.GET("/groups", s.listGroups)
	adm.POST("/groups", s.createGroup)
	adm.PUT("/groups/:id", s.updateGroup)
	adm.DELETE("/groups/:id", s.deleteGroup)
	adm.GET("/libraries/:id/access", s.getLibraryAccess)
	adm.PUT("/libraries/:id/access", s.setLibraryAccess)

	e.StaticFS("/themes", ui.Themes())
	if cfg.ArtworkDir != "" {
		e.Static("/artwork", cfg.ArtworkDir) // posters only; the UI needs them before sign-in resolves
	}

	// Admin UI shares the main port at /admin. Do not alias /settings here —
	// that path belongs to the client app's own settings page.
	e.StaticFS("/admin", ui.Admin())
	e.StaticFS("/", ui.Client())

	// SPA fallback. Both apps own their URL routing — the client at
	// /libraries/..., the admin at /admin/libraries/... — so an unknown GET
	// that is not asking for a static asset returns the owning app's
	// index.html and lets its router read the path. Without this a deep link
	// or a reload anywhere but the root 404s.
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		he, ok := err.(*echo.HTTPError)
		p := c.Request().URL.Path
		if ok && he.Code == http.StatusNotFound && c.Request().Method == http.MethodGet &&
			!looksLikeAsset(p) &&
			!hasAnyPrefix(p, "/api/", "/themes/", "/artwork/") {
			app, name := ui.Client(), "client"
			if hasAnyPrefix(p, "/admin") {
				app, name = ui.Admin(), "admin"
			}
			if data, ferr := fs.ReadFile(app, "index.html"); ferr == nil {
				_ = c.HTMLBlob(http.StatusOK, data)
				return
			}
			log.Printf("spa fallback: %s index.html unreadable", name)
		}
		e.DefaultHTTPErrorHandler(err, c)
	}

	// Graceful shutdown: SIGINT/SIGTERM stops HTTP, then kills transcode
	// sessions and removes their temp dirs.
	shutdownDone := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 2)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		got := <-sig
		name := "SIGTERM"
		if got == os.Interrupt {
			name = "Ctrl-C"
		}
		fmt.Printf("\n%s received — shutting down… (press Ctrl-C again to force)\n", name)
		// A second signal skips the graceful path.
		go func() {
			<-sig
			fmt.Println("forced exit")
			os.Exit(1)
		}()
		if n := len(s.playbacks.List()); n > 0 {
			fmt.Printf("  %d active playback(s) will be cut off\n", n)
		}
		fmt.Print("  stopping HTTP server (waiting up to 10 s for requests)… ")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := e.Shutdown(ctx); err != nil {
			fmt.Printf("%v\n", err)
		} else {
			fmt.Println("done")
		}
		stopSampler()
		if n := len(s.sessions.Sessions()); n > 0 {
			fmt.Printf("  stopping %d transcode session(s) and removing temp files… ", n)
			s.sessions.Close()
			fmt.Println("done")
		} else {
			s.sessions.Close()
		}
		fmt.Println("bye")
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

// toStoreSession / fromStoreSession translate between the media package's
// view of a session and the stored row. Two types rather than one shared
// struct, so the media package needs no knowledge of the store.
func toStoreSession(st media.SessionState) store.TranscodeSession {
	return store.TranscodeSession{
		ID: st.ID, ItemID: st.ItemID, Title: st.Title, InputPath: st.InputPath,
		Dir: st.Dir, SegmentSecs: st.SegmentSecs, NumSegments: st.NumSegments,
		RuntimeSecs: st.RuntimeSecs, QualityLabel: st.Quality.Label,
		QualityHeight: st.Quality.Height, QualityKbps: st.Quality.BitrateKbps,
		AudioTrack: st.AudioTrack, HDR: st.HDR, SegSecsReal: st.SegSecsReal,
	}
}

func fromStoreSession(t store.TranscodeSession) media.SessionState {
	return media.SessionState{
		ID: t.ID, ItemID: t.ItemID, Title: t.Title, InputPath: t.InputPath,
		Dir: t.Dir, SegmentSecs: t.SegmentSecs, NumSegments: t.NumSegments,
		RuntimeSecs: t.RuntimeSecs,
		Quality: media.Quality{
			Label: t.QualityLabel, Height: t.QualityHeight, BitrateKbps: t.QualityKbps,
		},
		AudioTrack: t.AudioTrack, HDR: t.HDR, SegSecsReal: t.SegSecsReal,
	}
}

// Go's mime table has no entry for .webmanifest, so the PWA manifest would
// be served as text/plain. Chrome tolerates that but the spec does not, and
// a manifest served as the wrong type is one of the quieter ways for an app
// to simply not offer to install.
func init() {
	if err := mime.AddExtensionType(".webmanifest", "application/manifest+json"); err != nil {
		log.Printf("register .webmanifest mime type: %v", err)
	}
}

// assetExts are the file types the embedded UIs actually serve. A request
// for one of these that misses is a genuine 404 — a broken asset reference,
// not a route — so the SPA fallback leaves it alone.
var assetExts = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".map": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
	".webp": true, ".avif": true, ".ico": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".json": true, ".webmanifest": true, ".txt": true, ".xml": true,
	".wasm": true, ".m3u8": true, ".m4s": true, ".mp4": true, ".vtt": true,
}

// looksLikeAsset reports whether a path is asking for a static file rather
// than an app route.
//
// It tests the extension against the list above rather than asking whether
// the last segment contains a dot, because film titles contain dots: "Dr.
// No", "E.T. the Extra-Terrestrial" and "Guardians of the Galaxy Vol. 2"
// are routes, and the dot rule sent all three to a JSON 404 on reload or a
// shared link. An unknown extension is treated as a route, which is the
// safe way round — a wrong guess here costs a route that renders the app
// shell, while the other way costs the viewer their page.
func looksLikeAsset(p string) bool {
	return assetExts[strings.ToLower(path.Ext(path.Base(p)))]
}

// incompressibleExts are already-compressed payloads. Gzipping them costs
// CPU and returns bytes that are no smaller, sometimes larger.
var incompressibleExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".avif": true, ".woff": true, ".woff2": true, ".mp4": true, ".m4s": true,
	".mkv": true, ".webm": true, ".mp3": true, ".m4a": true, ".flac": true,
	".ogg": true, ".opus": true, ".zip": true, ".cbz": true, ".cbr": true,
	".epub": true, ".pdf": true,
}

// incompressible reports whether a response should bypass gzip.
//
// Range requests are excluded as well as media types: gzip and a byte range
// do not mix — the range addresses the original bytes, and compressing the
// body after the fact would make Content-Range a lie. Direct play, audio
// streaming and downloads all depend on ranges.
func incompressible(c echo.Context) bool {
	if c.Request().Header.Get("Range") != "" {
		return true
	}
	p := c.Request().URL.Path
	if incompressibleExts[strings.ToLower(path.Ext(path.Base(p)))] {
		return true
	}
	// Artwork and transcode segments are served by path rather than by a
	// name the extension test would catch.
	return hasAnyPrefix(p, "/artwork/")
}
