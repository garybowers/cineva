package api

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/store"
)

type createLibraryRequest struct {
	Name      string   `json:"name"`
	MediaType string   `json:"mediaType"`
	Paths     []string `json:"paths"`
}

type libraryResponse struct {
	store.Library
	ItemCount int `json:"itemCount"`
}

func (s *server) createLibrary(c echo.Context) error {
	var req createLibraryRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	lib, err := s.store.CreateLibrary(c.Request().Context(), req.Name, store.MediaType(req.MediaType), req.Paths)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	s.events.Publish(events.Event{Type: "libraries"})
	s.audit(c, "library.create", "library", lib.ID, lib.Name, string(lib.MediaType)+" · "+strings.Join(lib.Paths, ", "))
	return c.JSON(http.StatusCreated, libraryResponse{Library: lib})
}

func (s *server) listLibraries(c echo.Context) error {
	ctx := c.Request().Context()
	libs, err := s.store.Libraries(ctx)
	if err != nil {
		return err
	}
	out := make([]libraryResponse, 0, len(libs))
	for _, lib := range libs {
		resp := libraryResponse{Library: lib}
		for _, kind := range []store.ItemKind{store.KindMovie, store.KindEpisode, store.KindTrack} {
			n, err := s.store.CountItems(ctx, lib.ID, kind)
			if err != nil {
				return err
			}
			resp.ItemCount += n
		}
		out = append(out, resp)
	}
	return c.JSON(http.StatusOK, out)
}

func (s *server) getLibrary(c echo.Context) error {
	lib, err := s.store.Library(c.Request().Context(), c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	return c.JSON(http.StatusOK, libraryResponse{Library: lib})
}

func (s *server) deleteLibrary(c echo.Context) error {
	lib, _ := s.store.Library(c.Request().Context(), c.Param("id"))
	if err := s.store.DeleteLibrary(c.Request().Context(), c.Param("id")); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	s.events.Publish(events.Event{Type: "libraries"})
	s.audit(c, "library.delete", "library", c.Param("id"), lib.Name, "")
	return c.NoContent(http.StatusNoContent)
}

func (s *server) scanLibrary(c echo.Context) error {
	ctx := c.Request().Context()
	lib, err := s.store.Library(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	scan, err := s.manager.StartScan(ctx, lib)
	if err != nil {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	s.audit(c, "scan.start", "library", lib.ID, lib.Name, "")
	return c.JSON(http.StatusAccepted, scan)
}

// POST /scans/:id/cancel — stop a running scan (or clear a stale one).
func (s *server) cancelScan(c echo.Context) error {
	if err := s.manager.CancelScan(c.Request().Context(), c.Param("id")); err != nil {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	s.audit(c, "scan.cancel", "scan", c.Param("id"), "", "")
	return c.NoContent(http.StatusNoContent)
}

func (s *server) listScans(c echo.Context) error {
	scans, err := s.store.Scans(c.Request().Context(), 50)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, scans)
}

func (s *server) libraryItems(c echo.Context) error {
	items, err := s.store.ItemsByLibrary(c.Request().Context(), c.Param("id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, items)
}

// GET /search?q= — universal title search.
func (s *server) search(c echo.Context) error {
	q := c.QueryParam("q")
	if len(q) < 2 {
		return c.JSON(http.StatusOK, []store.Item{})
	}
	items, err := s.store.SearchItems(c.Request().Context(), q, 20)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, items)
}

func (s *server) getItem(c echo.Context) error {
	ctx := c.Request().Context()
	item, err := s.store.Item(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	children, err := s.store.ItemsByParent(ctx, item.ID)
	if err != nil {
		return err
	}
	resp := map[string]any{"item": item, "children": children}
	if item.Kind == store.KindMovie || item.Kind == store.KindEpisode {
		if ps, err := s.store.PlayStateFor(ctx, store.LocalUser, item.ID); err == nil {
			resp["playState"] = ps
		}
	}
	return c.JSON(http.StatusOK, resp)
}
