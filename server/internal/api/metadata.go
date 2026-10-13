package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

var metadataLog = logging.For("metadata")

// Metadata management for the admin UI: see what a title matched, search for
// the right one, apply it, or re-run matching.

type matchCandidate struct {
	TmdbID   int    `json:"tmdbId"`
	Title    string `json:"title"`
	Year     int    `json:"year,omitempty"`
	Overview string `json:"overview,omitempty"`
	Poster   string `json:"poster,omitempty"`
}

func candidates(results []metadata.VideoResult) []matchCandidate {
	out := make([]matchCandidate, 0, len(results))
	for _, r := range results {
		out = append(out, matchCandidate{
			TmdbID: r.TmdbID, Title: r.Title, Year: r.Year,
			Overview: r.Overview, Poster: r.PosterURL,
		})
	}
	return out
}

// metadataUnavailable turns a missing provider into an explanation rather
// than a bare failure: the operator needs to know it is a missing key, not a
// broken server.
func metadataUnavailable(err error) error {
	if errors.Is(err, library.ErrNoProvider) {
		return echo.NewHTTPError(http.StatusServiceUnavailable,
			"no TMDB key configured — set --tmdb-key, CINEVA_METADATA_TMDB_API_KEY, or metadata.tmdb.api_key")
	}
	if errors.Is(err, metadata.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "no match found")
	}
	return err
}

// GET /items/:id/matches?q=&year= (admin) — candidate matches for an item.
// Defaults to the item's own title and year, so the common case is one click.
func (s *server) itemMatches(c echo.Context) error {
	ctx := c.Request().Context()
	item, err := s.store.Item(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	title, year := c.QueryParam("q"), item.Year
	if title == "" {
		title = item.Title
	}
	if y, err := strconv.Atoi(c.QueryParam("year")); err == nil && y > 0 {
		year = y
	}
	results, err := s.manager.SearchMovies(ctx, title, year)
	if err != nil {
		return metadataUnavailable(err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"query": title, "year": year, "results": candidates(results),
	})
}

// PUT /items/:id/match (admin) — pin an item to a chosen TMDB title.
func (s *server) putItemMatch(c echo.Context) error {
	var req struct {
		TmdbID int `json:"tmdbId"`
	}
	if err := c.Bind(&req); err != nil || req.TmdbID <= 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "tmdbId is required")
	}
	saved, err := s.manager.ApplyMatch(c.Request().Context(), c.Param("id"), req.TmdbID)
	if err != nil {
		return metadataUnavailable(err)
	}
	s.audit(c, "item.match", "item", saved.ID, saved.Title, "tmdb "+strconv.Itoa(req.TmdbID))
	s.events.Publish(events.Event{Type: "items", Data: map[string]string{"libraryId": saved.LibraryID}})
	return c.JSON(http.StatusOK, saved)
}

// POST /items/:id/refresh (admin) — re-run matching from the file's own name.
func (s *server) refreshItem(c echo.Context) error {
	saved, err := s.manager.RefreshItem(c.Request().Context(), c.Param("id"))
	if err != nil {
		return metadataUnavailable(err)
	}
	s.audit(c, "item.refresh", "item", saved.ID, saved.Title, "")
	s.events.Publish(events.Event{Type: "items", Data: map[string]string{"libraryId": saved.LibraryID}})
	return c.JSON(http.StatusOK, saved)
}

// Library-wide metadata refresh. Long-running, so it is started in the
// background and reports over the event stream, like a scan. One per
// library at a time: a second start would double the request rate and the
// two runs would fight over the same rows.
type refreshJob struct {
	cancel   context.CancelFunc
	progress library.RefreshProgress
}

// POST /libraries/:id/refresh (admin)
func (s *server) refreshLibrary(c echo.Context) error {
	lib, err := s.store.Library(c.Request().Context(), c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	if !s.manager.HasVideoProvider() {
		return metadataUnavailable(library.ErrNoProvider)
	}
	// The admin either sends ids, or the Manage list's filter (q and
	// unmatched, as GET /libraries/:id/manage takes them) so "refresh" means
	// everything the filter matches across all its pages. Neither means the
	// whole library.
	var req struct {
		ItemIDs   []string `json:"itemIds"`
		Query     string   `json:"q"`
		Unmatched bool     `json:"unmatched"`
	}
	_ = c.Bind(&req)
	if f := manageFilter(req.Query, req.Unmatched); len(req.ItemIDs) == 0 && (f.Query != "" || f.Unmatched) {
		ids, err := s.store.ManageItemIDs(c.Request().Context(), lib.ID, f)
		if err != nil {
			return err
		}
		// An empty list would otherwise mean the whole library — the
		// opposite of what a filter that matches nothing asked for.
		if len(ids) == 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "nothing matches that filter")
		}
		req.ItemIDs = ids
	}

	s.refreshMu.Lock()
	if _, running := s.refreshes[lib.ID]; running {
		s.refreshMu.Unlock()
		return echo.NewHTTPError(http.StatusConflict, "a refresh is already running for this library")
	}
	// Deliberately not the request context: the job outlives the request
	// that started it, and is cancelled by the admin, not by the browser
	// navigating away.
	ctx, cancel := context.WithCancel(context.Background())
	s.refreshes[lib.ID] = &refreshJob{cancel: cancel}
	s.refreshMu.Unlock()

	s.audit(c, "library.refresh", "library", lib.ID, lib.Name, "")
	go func() {
		defer cancel()
		err := s.manager.RefreshLibrary(ctx, lib.ID, req.ItemIDs, func(p library.RefreshProgress) {
			s.refreshMu.Lock()
			if job, ok := s.refreshes[lib.ID]; ok {
				job.progress = p
			}
			s.refreshMu.Unlock()
			s.events.Publish(events.Event{Type: "refresh", Data: p})
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			metadataLog.Error("metadata refresh failed", "library", lib.Name, "err", err)
		}
		s.refreshMu.Lock()
		delete(s.refreshes, lib.ID)
		s.refreshMu.Unlock()
		// The grid and the manage list both change wholesale.
		s.events.Publish(events.Event{Type: "items", Data: map[string]string{"libraryId": lib.ID}})
		s.events.Publish(events.Event{Type: "libraries"})
	}()
	return c.JSON(http.StatusAccepted, library.RefreshProgress{LibraryID: lib.ID, Running: true})
}

// DELETE /libraries/:id/refresh (admin) — stop a running refresh.
func (s *server) cancelRefresh(c echo.Context) error {
	s.refreshMu.Lock()
	job, ok := s.refreshes[c.Param("id")]
	s.refreshMu.Unlock()
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "no refresh running for this library")
	}
	job.cancel()
	return c.NoContent(http.StatusNoContent)
}

// GET /libraries/:id/refresh (admin) — current progress, for a page that
// loads while a refresh is already under way.
func (s *server) refreshStatus(c echo.Context) error {
	s.refreshMu.Lock()
	job, ok := s.refreshes[c.Param("id")]
	var p library.RefreshProgress
	if ok {
		p = job.progress
	}
	s.refreshMu.Unlock()
	p.LibraryID = c.Param("id")
	p.Running = ok
	return c.JSON(http.StatusOK, p)
}

// PATCH /items/:id (admin) — edit an item's metadata by hand.
//
// Every field sent is locked, so the next scan or "Refresh metadata" leaves
// it alone. Fields not sent are untouched and stay the provider's to update.
func (s *server) updateItemMetadata(c echo.Context) error {
	ctx := c.Request().Context()
	item, err := s.store.Item(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	var edit store.ItemEdit
	if err := c.Bind(&edit); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	updated, err := s.store.UpdateItem(ctx, item.ID, edit)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if genres, gerr := s.store.GenresForItem(ctx, updated.ID); gerr == nil {
		updated.Genres = genres
	}
	s.audit(c, "item.edit", "item", updated.ID, updated.Title, strings.Join(updated.LockedFields, ","))
	s.events.Publish(events.Event{Type: "items"})
	return c.JSON(http.StatusOK, updated)
}

// DELETE /items/:id/locks (admin) — clear every manual lock, handing the
// item back to the provider. The field values stay as they are until the
// next refresh overwrites them.
func (s *server) clearItemLocks(c echo.Context) error {
	ctx := c.Request().Context()
	item, err := s.store.Item(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	if err := s.store.SetLockedFields(ctx, item.ID, nil); err != nil {
		return err
	}
	s.audit(c, "item.unlock", "item", item.ID, item.Title, "")
	s.events.Publish(events.Event{Type: "items"})
	return c.NoContent(http.StatusNoContent)
}
