package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/importer"
)

// Import from Plex and Jellyfin (see docs/import.md). The source's token
// is used for the request or run it came with and never stored.

type importSourceRequest struct {
	Kind  string `json:"kind"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

func (r importSourceRequest) check() error {
	if r.Kind != "plex" && r.Kind != "jellyfin" {
		return echo.NewHTTPError(http.StatusBadRequest, "kind must be plex or jellyfin")
	}
	if !httpURL(strings.TrimSpace(r.URL)) {
		return echo.NewHTTPError(http.StatusBadRequest, "url must be the server's http(s) address")
	}
	if strings.TrimSpace(r.Token) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "a token is required")
	}
	return nil
}

// POST /import/check (admin) — connect to a source and list its users.
func (s *server) importCheck(c echo.Context) error {
	var req importSourceRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := req.check(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
	defer cancel()
	info, err := s.importer.Check(ctx, req.Kind, strings.TrimSpace(req.URL), strings.TrimSpace(req.Token))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	return c.JSON(http.StatusOK, info)
}

// POST /import/runs (admin) — start a dry run or an import.
func (s *server) startImport(c echo.Context) error {
	var req importer.Request
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	src := importSourceRequest{Kind: req.Kind, URL: req.URL, Token: req.Token}
	if err := src.check(); err != nil {
		return err
	}
	if !req.Options.WatchState && !req.Options.Playlists {
		return echo.NewHTTPError(http.StatusBadRequest, "choose something to bring: watch state, playlists or both")
	}
	req.URL, req.Token = strings.TrimSpace(req.URL), strings.TrimSpace(req.Token)
	run, err := s.importer.Start(req)
	if errors.Is(err, importer.ErrBusy) {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	} else if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	detail := req.Kind + " " + req.URL
	if req.DryRun {
		detail = "dry run · " + detail
	}
	s.audit(c, "import.start", "import", run.ID, req.Kind, detail)
	return c.JSON(http.StatusAccepted, run)
}

// GET /import/runs/current (admin) — the running or last run.
func (s *server) currentImport(c echo.Context) error {
	run, ok := s.importer.Current()
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "no import has run")
	}
	return c.JSON(http.StatusOK, run)
}

// startImporter wires the import runner's progress to admins' event
// streams.
func (s *server) startImporter() {
	s.importer.OnProgress = func(run importer.Run) {
		s.events.Publish(events.Event{Type: "import", Data: run})
		if run.Status != "running" {
			s.events.Publish(events.Event{Type: "users"})
		}
	}
}
