package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/library"
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
	// Scanning says a scan is running, with no detail about it — enough for
	// a client to say "scanning…" instead of telling a non-admin to start
	// one, and not enough to leak anything the scan events withhold.
	Scanning bool `json:"scanning"`
	// Storage is whether the library's folders can be reached, so a client
	// can say so instead of offering titles that cannot play.
	Storage library.StorageStatus `json:"storage"`
}

// PATCH /libraries/:id (admin) — rename, or change which paths it covers.
//
// The media type is not changeable: everything already scanned keys off it.
func (s *server) updateLibrary(c echo.Context) error {
	var req struct {
		Name  *string   `json:"name"`
		Paths *[]string `json:"paths"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	cur, err := s.store.Library(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	name, paths := cur.Name, cur.Paths
	if req.Name != nil {
		name = *req.Name
	}
	if req.Paths != nil {
		paths = *req.Paths
	}
	lib, err := s.store.UpdateLibrary(ctx, cur.ID, name, paths)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "library not found")
		}
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	s.audit(c, "library.update", "library", lib.ID, lib.Name, strings.Join(lib.Paths, ", "))
	s.events.Publish(events.Event{Type: "libraries"})
	return c.JSON(http.StatusOK, libraryResponse{Library: lib})
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
	me := currentUser(c)
	out := make([]libraryResponse, 0, len(libs))
	for _, lib := range libs {
		if !canSeeLibrary(me, lib.ID) {
			continue
		}
		resp := libraryResponse{Library: lib}
		// Whether a scan is running, so a client that loads mid-scan shows
		// the right empty state without waiting for the next event.
		resp.Scanning = s.manager.Scanning(lib.ID)
		if s.storage != nil {
			resp.Storage = s.storage.Status(lib.ID)
		}
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
	if err != nil || !canSeeLibrary(currentUser(c), lib.ID) {
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
	// Incremental unless asked otherwise: only files changed since the last
	// scan are read and matched again.
	full := c.QueryParam("full") == "true"
	scan, err := s.manager.StartScan(ctx, lib, full)
	if err != nil {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	detail := ""
	if full {
		detail = "full rescan"
	}
	s.audit(c, "scan.start", "library", lib.ID, lib.Name, detail)
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
	if !canSeeLibrary(currentUser(c), c.Param("id")) {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	// ?genre=Drama narrows to one genre, for the Genres tab.
	if items, byGenre, err := s.libraryItemsByGenre(c); byGenre {
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, s.withPlayState(c, c.Param("id"), items))
	}
	// ?since=<mark> returns only what changed, for polling during a scan.
	// The response then carries the next mark in X-Items-Mark rather than
	// wrapping the array, so the plain listing keeps its shape.
	if since := c.QueryParam("since"); since != "" {
		items, mark, err := s.store.ItemsByLibrarySince(c.Request().Context(), c.Param("id"), since)
		if err != nil {
			return err
		}
		c.Response().Header().Set("X-Items-Mark", mark)
		return c.JSON(http.StatusOK, s.withPlayState(c, c.Param("id"), items))
	}
	items, err := s.store.ItemsByLibrary(c.Request().Context(), c.Param("id"))
	if err != nil {
		return err
	}
	// The mark a subsequent ?since= call should start from.
	var mark string
	for _, it := range items {
		if it.UpdatedAt > mark {
			mark = it.UpdatedAt
		}
	}
	c.Response().Header().Set("X-Items-Mark", mark)
	return c.JSON(http.StatusOK, s.withPlayState(c, c.Param("id"), items))
}

// Page sizes for the admin's Manage list: big enough to work through, small
// enough that a library of tens of thousands never arrives in one response.
const (
	manageDefaultSize = 100
	manageMaxSize     = 500
)

type managePageResponse struct {
	Items []store.Item `json:"items"`
	// Total is how many items match the filter; Matched and All are the
	// whole library's, for the "X of N matched" line.
	Total   int `json:"total"`
	Matched int `json:"matched"`
	All     int `json:"all"`
	Page    int `json:"page"`
	Size    int `json:"size"`
}

// manageFilter reads the Manage list's filter from a request, shared by the
// listing and by a filtered refresh so both mean the same items.
func manageFilter(q string, unmatched bool) store.ManageFilter {
	return store.ManageFilter{Query: strings.TrimSpace(q), Unmatched: unmatched}
}

// GET /libraries/:id/manage (admin) — one page of the Manage list, filtered
// by ?q= (title or path) and ?unmatched=true on the server.
func (s *server) libraryManage(c echo.Context) error {
	ctx := c.Request().Context()
	lib, err := s.store.Library(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	page, _ := strconv.Atoi(c.QueryParam("page"))
	page = max(page, 1)
	size, err := strconv.Atoi(c.QueryParam("size"))
	if err != nil {
		size = manageDefaultSize
	}
	size = min(max(size, 1), manageMaxSize)
	unmatched, _ := strconv.ParseBool(c.QueryParam("unmatched"))

	res, err := s.store.ManageItems(ctx, lib.ID,
		manageFilter(c.QueryParam("q"), unmatched), (page-1)*size, size)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, managePageResponse{
		Items: res.Items, Total: res.Total, Matched: res.Matched, All: res.All,
		Page: page, Size: size,
	})
}

// withPlayState stamps the viewer's own watched flag and resume position onto
// a list of items, so the grid can mark what has been seen and how far in
// without a request per tile. One query covers the whole library.
//
// A failure here is not worth failing the listing for: the grid still works
// without the markers, so the items are returned undecorated.
func (s *server) withPlayState(c echo.Context, libraryID string, items []store.Item) []store.Item {
	uid := userID(c)
	if uid == "" || len(items) == 0 {
		return items
	}
	states, err := s.store.PlayStatesByLibrary(c.Request().Context(), uid, libraryID)
	if err != nil || len(states) == 0 {
		return items
	}
	for i := range items {
		if ps, ok := states[items[i].ID]; ok {
			items[i].Watched = ps.Watched
			items[i].PositionSecs = ps.PositionSecs
			items[i].WatchedAt = ps.UpdatedAt
		}
	}
	return items
}

// GET /libraries/:id/recommended — the Recommended tab's shelves.
//
// Built server-side for the same reason next-up is: the rules should not be
// reimplemented, slightly differently, in every client. Recently added leads,
// ranked as on the home screen: a series by its newest episode, which a
// client holding only series rows cannot work out for itself.
func (s *server) libraryRecommended(c echo.Context) error {
	if !canSeeLibrary(currentUser(c), c.Param("id")) {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	ctx := c.Request().Context()
	lib, err := s.store.Library(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	recs, err := s.store.Recommendations(ctx, userID(c), lib.ID)
	if err != nil {
		return err
	}
	rows := make([]store.RecRow, 0, len(recs)+1)
	// Not every media type has a recently added listing yet; those simply
	// go without the row, as they do on the home screen.
	if recent, err := s.store.RecentlyAdded(ctx, lib, homeRowSize); err == nil && len(recent) > 0 {
		rows = append(rows, store.RecRow{Kind: "recent", Title: "Recently added", Items: recent})
	}
	rows = append(rows, recs...)
	// Each row's films carry the viewer's own state, like any other listing.
	for i := range rows {
		rows[i].Items = s.withPlayState(c, c.Param("id"), rows[i].Items)
	}
	return c.JSON(http.StatusOK, rows)
}

// GET /libraries/:id/collections — the collections this library holds more
// than one film from, for the grid's collection row.
func (s *server) libraryCollections(c echo.Context) error {
	if !canSeeLibrary(currentUser(c), c.Param("id")) {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	cols, err := s.store.LibraryCollections(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, cols)
}

// collectionKey reads the :key path parameter.
//
// Echo hands back the raw path segment, so a client that percent-encodes the
// key (encodeURIComponent turns the "tmdb:123" prefix into "tmdb%3A123")
// would never match. Decoding here accepts both forms rather than making
// every caller agree not to encode a value that sits in a URL path.
func collectionKey(c echo.Context) string {
	raw := c.Param("key")
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

// GET /libraries/:id/collections/:key — one collection's films, in the
// collection's own order.
func (s *server) collectionItems(c echo.Context) error {
	if !canSeeLibrary(currentUser(c), c.Param("id")) {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	ctx := c.Request().Context()
	col, ok, err := s.store.CollectionByKey(ctx, userID(c), c.Param("id"), collectionKey(c))
	if err != nil {
		return err
	}
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "collection not found")
	}
	items, err := s.store.ItemsByCollection(ctx, c.Param("id"), col.Key)
	if err != nil {
		return err
	}
	// The summary rides along with the films so the client can show
	// progress and a "carry on" button without a second request, and so the
	// next-up decision stays server-side — same reason the episode next-up
	// does, so native clients behave identically.
	return c.JSON(http.StatusOK, collectionDetail{
		Collection: col,
		Items:      s.withPlayState(c, c.Param("id"), items),
	})
}

// collectionDetail is one collection plus its films, in order.
type collectionDetail struct {
	Collection store.Collection `json:"collection"`
	Items      []store.Item     `json:"items"`
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
	me := currentUser(c)
	visible := items[:0]
	for _, it := range items {
		if canSeeLibrary(me, it.LibraryID) {
			visible = append(visible, it)
		}
	}
	return c.JSON(http.StatusOK, visible)
}

func (s *server) getItem(c echo.Context) error {
	ctx := c.Request().Context()
	item, err := s.store.Item(ctx, c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), item.LibraryID) {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	children, err := s.store.ItemsByParent(ctx, item.ID)
	if err != nil {
		return err
	}
	var resp0Cast, resp0Crew []store.Person
	// Genres are not loaded by the grid queries — thousands of rows that do
	// not use them — so the detail page fetches them for the one item.
	if g, err := s.store.GenresForItem(ctx, item.ID); err == nil {
		item.Genres = g
	}
	if cast, crew, err := s.store.CreditsForItem(ctx, item.ID); err == nil {
		if len(cast) > 0 {
			resp0Cast = cast
		}
		if len(crew) > 0 {
			resp0Crew = crew
		}
	}
	// A season's episodes carry the viewer's progress so each row can show
	// how far in they are without opening it.
	if item.Kind == store.KindSeason {
		children = s.withPlayState(c, item.LibraryID, children)
	}
	resp := map[string]any{"item": item, "children": children}
	if len(resp0Cast) > 0 {
		resp["cast"] = resp0Cast
	}
	if len(resp0Crew) > 0 {
		resp["crew"] = resp0Crew
	}
	if item.Kind == store.KindMovie || item.Kind == store.KindEpisode {
		if ps, err := s.store.PlayStateFor(ctx, userID(c), item.ID); err == nil {
			resp["playState"] = ps
		}
	}
	// Alternative versions of the same film, so the detail page can offer a
	// picker. Asked for from either side: opening a version directly (from
	// a search result, say) should still show its siblings, so the primary
	// is resolved first.
	if item.Kind == store.KindMovie {
		primary := item.ID
		if item.VersionOf != "" {
			primary = item.VersionOf
		}
		versions, err := s.store.VersionsOf(ctx, primary)
		if err != nil {
			return err
		}
		if len(versions) > 0 {
			if p, err := s.store.Item(ctx, primary); err == nil {
				resp["versions"] = append([]store.Item{p}, versions...)
			}
		}
	}
	return c.JSON(http.StatusOK, resp)
}

// GET /libraries/:id/genres — the genres this library actually holds, with
// counts, most common first.
//
// Derived from the films rather than the provider's fixed vocabulary: a
// library of documentaries should not offer an empty "Western" row, and one
// that gains a genre gets it without a code change.
func (s *server) libraryGenres(c echo.Context) error {
	ctx := c.Request().Context()
	lib, err := s.store.Library(ctx, c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), lib.ID) {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	genres, err := s.store.LibraryGenres(ctx, lib.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, genres)
}

// GET /libraries/:id/items?genre=Drama narrows the grid to one genre.
func (s *server) libraryItemsByGenre(c echo.Context) ([]store.Item, bool, error) {
	genre := c.QueryParam("genre")
	if genre == "" {
		return nil, false, nil
	}
	items, err := s.store.ItemsByGenre(c.Request().Context(), c.Param("id"), genre)
	return items, true, err
}

// GET /people/:id — one person and the films in the library they are
// credited on. This is what makes a cast member a way into the collection
// rather than a dead name: tapping one shows everything else they are in.
//
// Only films the viewer may see are returned, so a person credited on a
// library this user has no grant for does not leak its contents.
func (s *server) getPerson(c echo.Context) error {
	ctx := c.Request().Context()
	prof, err := s.store.PersonPage(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "person not found")
	}
	// Filter after assembling, so a person credited on a library this user
	// has no grant for does not leak its contents — but recount, or the
	// summary would describe films they cannot see.
	user := currentUser(c)
	visible := make([]store.PersonCredit, 0, len(prof.Credits))
	for _, cr := range prof.Credits {
		if canSeeLibrary(user, cr.LibraryID) {
			visible = append(visible, cr)
		}
	}
	if len(visible) != len(prof.Credits) {
		prof = recountProfile(prof, visible)
	}
	prof.Credits = visible
	return c.JSON(http.StatusOK, prof)
}

// recountProfile rebuilds the summary counts after credits were filtered out
// by library grants.
func recountProfile(p store.PersonProfile, visible []store.PersonCredit) store.PersonProfile {
	p.CastCount, p.CrewCount, p.FirstYear, p.LastYear = 0, 0, 0, 0
	for _, c := range visible {
		if c.CreditKind == "cast" {
			p.CastCount++
		} else {
			p.CrewCount++
		}
		if y := c.Year; y > 0 {
			if p.FirstYear == 0 || y < p.FirstYear {
				p.FirstYear = y
			}
			if y > p.LastYear {
				p.LastYear = y
			}
		}
	}
	return p
}

// ---- Collection management (admin) ----
//
// Every mutation adopts a provider collection first: editing TMDB's grouping
// means taking it over, and adopting copies the membership and order already
// on screen so an edit starts from what the operator was looking at.

// POST /libraries/:id/collections — create an operator-defined collection.
func (s *server) createCollection(c echo.Context) error {
	var req struct {
		Name    string   `json:"name"`
		ItemIDs []string `json:"itemIds"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	col, err := s.store.CreateCollection(ctx, c.Param("id"), req.Name)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if len(req.ItemIDs) > 0 {
		if err := s.store.SetCollectionItems(ctx, col.Key, req.ItemIDs); err != nil {
			return err
		}
	}
	s.audit(c, "collection.create", "collection", col.Key, col.Name, "")
	s.events.Publish(events.Event{Type: "items"})
	return c.JSON(http.StatusCreated, col)
}

// PATCH /libraries/:id/collections/:key — rename.
func (s *server) renameCollection(c echo.Context) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	id, err := s.store.AdoptCollection(ctx, c.Param("id"), collectionKey(c))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "collection not found")
	}
	if err := s.store.RenameCollection(ctx, id, req.Name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "collection not found")
		}
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	s.audit(c, "collection.rename", "collection", id, req.Name, "")
	s.events.Publish(events.Event{Type: "items"})
	return s.collectionAfterEdit(c, id)
}

// PUT /libraries/:id/collections/:key/items — set membership and order. The
// body is the list the operator wants, so adding, removing and reordering
// are one operation rather than three endpoints.
func (s *server) setCollectionItems(c echo.Context) error {
	var req struct {
		ItemIDs []string `json:"itemIds"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	id, err := s.store.AdoptCollection(ctx, c.Param("id"), collectionKey(c))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "collection not found")
	}
	if err := s.store.SetCollectionItems(ctx, id, req.ItemIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "collection not found")
		}
		return err
	}
	s.audit(c, "collection.items", "collection", id, "", strconv.Itoa(len(req.ItemIDs))+" films")
	s.events.Publish(events.Event{Type: "items"})
	return s.collectionAfterEdit(c, id)
}

// DELETE /libraries/:id/collections/:key — remove a managed collection.
// Where it overrode a provider one, the provider's grouping reappears, so
// this doubles as "revert to the provider's answer".
func (s *server) deleteCollection(c echo.Context) error {
	_, managedID := store.ParseCollectionKey(collectionKey(c))
	if managedID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "only a managed collection can be deleted")
	}
	if err := s.store.DeleteCollection(c.Request().Context(), managedID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "collection not found")
		}
		return err
	}
	s.audit(c, "collection.delete", "collection", managedID, "", "")
	s.events.Publish(events.Event{Type: "items"})
	return c.NoContent(http.StatusNoContent)
}

// collectionAfterEdit returns the edited collection in the same shape a GET
// would, so the admin can replace its row without a refetch.
func (s *server) collectionAfterEdit(c echo.Context, id string) error {
	ctx := c.Request().Context()
	col, ok, err := s.store.CollectionByKey(ctx, userID(c), c.Param("id"), id)
	if err != nil {
		return err
	}
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "collection not found")
	}
	items, err := s.store.ItemsByCollection(ctx, c.Param("id"), id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, collectionDetail{Collection: col, Items: s.withPlayState(c, c.Param("id"), items)})
}
