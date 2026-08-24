package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/media"
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
}

type server struct {
	store    *store.Store
	manager  *library.Manager
	sessions *media.SessionManager
}

func Serve(cfg Config, st *store.Store, manager *library.Manager) error {
	s := &server{
		store:    st,
		manager:  manager,
		sessions: media.NewSessionManager(cfg.TranscodeDir, cfg.SegmentSecs),
	}

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover(), middleware.Logger())

	v1 := e.Group("/api/v1")
	v1.GET("/health", health)
	v1.GET("/themes", listThemes)
	v1.GET("/libraries", s.listLibraries)
	v1.POST("/libraries", s.createLibrary)
	v1.GET("/libraries/:id", s.getLibrary)
	v1.DELETE("/libraries/:id", s.deleteLibrary)
	v1.POST("/libraries/:id/scan", s.scanLibrary)
	v1.GET("/libraries/:id/items", s.libraryItems)
	v1.GET("/items/:id", s.getItem)
	v1.GET("/scans", s.listScans)

	// Playback (milestone 3)
	v1.POST("/items/:id/play", s.playItem)
	v1.GET("/items/:id/stream", s.streamItem)
	v1.GET("/items/:id/download", s.downloadItem)
	v1.POST("/items/:id/progress", s.postProgress)
	v1.GET("/items/:id/next", s.nextUp)
	v1.GET("/continue-watching", s.continueWatching)
	v1.GET("/play/sessions", s.listSessions)
	v1.GET("/play/:sid/index.m3u8", s.hlsPlaylist)
	v1.GET("/play/:sid/:file", s.hlsFile)
	v1.DELETE("/play/:sid", s.stopSession)

	e.StaticFS("/themes", ui.Themes())
	if cfg.ArtworkDir != "" {
		e.Static("/artwork", cfg.ArtworkDir)
	}

	// Admin UI shares the main port at /admin. Do not alias /settings here —
	// that path belongs to the client app's own settings page.
	e.StaticFS("/admin", ui.Admin())
	e.StaticFS("/", ui.Client())

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

func health(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "cineva",
		"version": version.Version,
	})
}
