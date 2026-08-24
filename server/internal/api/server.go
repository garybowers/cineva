package api

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/store"
	"github.com/garybowers/cineva/server/internal/version"
	"github.com/garybowers/cineva/server/ui"
)

type Config struct {
	Host       string
	Port       int
	ArtworkDir string
}

type server struct {
	store   *store.Store
	manager *library.Manager
}

func Serve(cfg Config, st *store.Store, manager *library.Manager) error {
	s := &server{store: st, manager: manager}

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

	e.StaticFS("/themes", ui.Themes())
	if cfg.ArtworkDir != "" {
		e.Static("/artwork", cfg.ArtworkDir)
	}

	// Admin UI shares the main port at /admin. Do not alias /settings here —
	// that path belongs to the client app's own settings page.
	e.StaticFS("/admin", ui.Admin())
	e.StaticFS("/", ui.Client())

	return e.Start(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
}

func health(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "cineva",
		"version": version.Version,
	})
}
