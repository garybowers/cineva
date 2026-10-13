package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/requests"
	"github.com/garybowers/cineva/server/internal/store"
)

// Requests: finding titles outside the library, asking for them, and the
// admin's queue. The work is in internal/requests; these translate.

// requestError maps the service's errors onto HTTP. Anything it does not
// recognise came from Sonarr, Radarr or TMDB.
func requestError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, requests.ErrNotAllowed):
		return echo.NewHTTPError(http.StatusForbidden, err.Error())
	case errors.Is(err, requests.ErrNoTMDB):
		return echo.NewHTTPError(http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, requests.ErrInvalid), errors.Is(err, requests.ErrNoConnection):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, requests.ErrInLibrary), errors.Is(err, requests.ErrState), errors.Is(err, requests.ErrNotManaged):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNotFound), errors.Is(err, metadata.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "not found")
	default:
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
}

// requestStatus is the viewer's request status, refusing those with none.
func (s *server) requestStatus(c echo.Context) (requests.Status, error) {
	st, err := s.requests.StatusFor(c.Request().Context(), currentUser(c))
	if err != nil {
		return st, err
	}
	if st.Level == store.RequestNone {
		return st, requestError(requests.ErrNotAllowed)
	}
	return st, nil
}

// GET /requests/status — whether the viewer can request, and what.
func (s *server) getRequestStatus(c echo.Context) error {
	st, err := s.requests.StatusFor(c.Request().Context(), currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, st)
}

// GET /discover/search?q= — films and series outside the viewer's
// libraries, for the "Not in your library" row.
func (s *server) discoverSearch(c echo.Context) error {
	st, err := s.requestStatus(c)
	if err != nil {
		return err
	}
	q := strings.TrimSpace(c.QueryParam("q"))
	if len(q) < 2 {
		return c.JSON(http.StatusOK, []requests.Found{})
	}
	me := currentUser(c)
	found, err := s.requests.Search(c.Request().Context(), st, q, func(lib string) bool { return canSeeLibrary(me, lib) })
	if err != nil {
		return requestError(err)
	}
	return c.JSON(http.StatusOK, found)
}

// GET /discover/:type/:tmdbId — a title page from TMDB.
func (s *server) discoverTitle(c echo.Context) error {
	st, err := s.requestStatus(c)
	if err != nil {
		return err
	}
	mediaType := c.Param("type")
	if mediaType != store.RequestMovie && mediaType != store.RequestTV {
		return echo.NewHTTPError(http.StatusNotFound, "type must be movie or tv")
	}
	id, err := strconv.Atoi(c.Param("tmdbId"))
	if err != nil || id <= 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "tmdbId must be a number")
	}
	me := currentUser(c)
	t, err := s.requests.TitleFor(c.Request().Context(), st, mediaType, id, s.certRegion(c),
		func(lib string) bool { return canSeeLibrary(me, lib) })
	if err != nil {
		return requestError(err)
	}
	return c.JSON(http.StatusOK, t)
}

// GET /requests?all=1&status=requested,approved — the viewer's requests,
// or everyone's for an admin asking for all.
func (s *server) listRequests(c echo.Context) error {
	me := currentUser(c)
	if me.EffectiveRequestLevel() == store.RequestNone {
		return requestError(requests.ErrNotAllowed)
	}
	var statuses []string
	if v := strings.TrimSpace(c.QueryParam("status")); v != "" {
		statuses = strings.Split(v, ",")
	}
	all := c.QueryParam("all") == "1" || c.QueryParam("all") == "true"
	list, err := s.requests.List(c.Request().Context(), me, all, statuses)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, list)
}

// POST /requests — ask for a film, a series, some seasons or episodes.
func (s *server) createRequest(c echo.Context) error {
	var in requests.NewRequest
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	r, created, err := s.requests.Create(c.Request().Context(), currentUser(c), in)
	if !created {
		if err != nil {
			return requestError(err)
		}
		return c.JSON(http.StatusOK, r)
	}
	// Recorded either way; a send that failed waits for an admin, and the
	// request carries the reason.
	s.audit(c, "request.create", "request", r.ID, r.Title, describeScope(r))
	return c.JSON(http.StatusCreated, r)
}

// describeScope is the audit detail for a request: what of the title.
func describeScope(r store.MediaRequest) string {
	parts := []string{r.MediaType}
	if len(r.Seasons) > 0 {
		var ss []string
		for _, n := range r.Seasons {
			ss = append(ss, strconv.Itoa(n))
		}
		parts = append(parts, "seasons "+strings.Join(ss, ", "))
	}
	if len(r.Episodes) > 0 {
		parts = append(parts, strconv.Itoa(len(r.Episodes))+" episodes")
	}
	parts = append(parts, r.Status)
	return strings.Join(parts, " · ")
}

// POST /requests/:id/approve (admin) — send it, optionally with another
// instance, profile or folder.
func (s *server) approveRequest(c echo.Context) error {
	var ov requests.Override
	if err := c.Bind(&ov); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	r, err := s.requests.Approve(c.Request().Context(), currentUser(c), c.Param("id"), ov)
	if err != nil {
		// A send that failed is still worth a line in the trail: the
		// request now carries the reason.
		if r.ID != "" && !errors.Is(err, requests.ErrState) {
			s.audit(c, "request.failed", "request", r.ID, r.Title, err.Error())
		}
		return requestError(err)
	}
	s.audit(c, "request.approve", "request", r.ID, r.Title, ov.Note)
	return c.JSON(http.StatusOK, r)
}

// POST /requests/:id/decline (admin)
func (s *server) declineRequest(c echo.Context) error {
	var body struct {
		Note string `json:"note"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	r, err := s.requests.Decline(c.Request().Context(), currentUser(c), c.Param("id"), strings.TrimSpace(body.Note))
	if err != nil {
		return requestError(err)
	}
	s.audit(c, "request.decline", "request", r.ID, r.Title, r.Note)
	return c.JSON(http.StatusOK, r)
}

// DELETE /requests/:id — withdraw a waiting request (its owner), or remove
// any (an admin).
func (s *server) deleteRequest(c echo.Context) error {
	r, err := s.requests.Withdraw(c.Request().Context(), currentUser(c), c.Param("id"))
	if err != nil {
		return requestError(err)
	}
	s.audit(c, "request.delete", "request", r.ID, r.Title, r.Status)
	return c.NoContent(http.StatusNoContent)
}

// seriesFor loads a series the viewer can see.
func (s *server) seriesFor(c echo.Context) (store.Item, error) {
	item, err := s.store.Item(c.Request().Context(), c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), item.LibraryID) {
		return store.Item{}, echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	if item.Kind != store.KindSeries {
		return store.Item{}, echo.NewHTTPError(http.StatusBadRequest, "only a series has missing episodes")
	}
	return item, nil
}

// GET /items/:id/missing — a series' episodes Sonarr knows of that are not
// on disk, and those still to air.
func (s *server) itemMissing(c echo.Context) error {
	if _, err := s.requestStatus(c); err != nil {
		return err
	}
	item, err := s.seriesFor(c)
	if err != nil {
		return err
	}
	m, err := s.requests.MissingFor(c.Request().Context(), item)
	if err != nil {
		return requestError(err)
	}
	return c.JSON(http.StatusOK, m)
}

// POST /items/:id/missing/search — search Sonarr now (admins and users
// whose requests go straight through).
func (s *server) searchMissing(c echo.Context) error {
	item, err := s.seriesFor(c)
	if err != nil {
		return err
	}
	var body struct {
		Seasons  []int              `json:"seasons"`
		Episodes []store.EpisodeRef `json:"episodes"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := s.requests.SearchMissing(c.Request().Context(), currentUser(c), item, body.Seasons, body.Episodes); err != nil {
		return requestError(err)
	}
	s.audit(c, "request.search", "item", item.ID, item.Title,
		describeScope(store.MediaRequest{MediaType: store.RequestTV, Seasons: body.Seasons, Episodes: body.Episodes, Status: "search"}))
	return c.NoContent(http.StatusAccepted)
}
