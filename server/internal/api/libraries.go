package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

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
		for _, kind := range []store.ItemKind{store.KindMovie, store.KindEpisode} {
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
	if err := s.store.DeleteLibrary(c.Request().Context(), c.Param("id")); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
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
	return c.JSON(http.StatusAccepted, scan)
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
	return c.JSON(http.StatusOK, map[string]any{"item": item, "children": children})
}
