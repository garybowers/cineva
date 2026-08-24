package api

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/garybowers/cineva/server/internal/store"
	"github.com/garybowers/cineva/server/internal/version"
	"github.com/garybowers/cineva/server/ui"
)

type Config struct {
	Host string
	Port int
}

// Serve starts the HTTP server. st is kept on the server for the handlers
// that arrive with milestone 2 (library CRUD).
func Serve(cfg Config, st *store.Store) error {
	_ = st
	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover(), middleware.Logger())

	v1 := e.Group("/api/v1")
	v1.GET("/health", health)
	v1.GET("/themes", listThemes)

	e.StaticFS("/themes", ui.Themes())

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
