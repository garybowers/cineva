package api

import (
	"math/rand/v2"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/store"
)

// The paged music listings (server/docs/music.md, part 1). The store
// filters, sorts and decorates; these read the query, check the library
// is a visible music one and clamp the page.

// Page sizes for the music listings: a grid asks for a screenful at a
// time; a track list may ask for enough to play a genre or a random
// thousand of the library in one request.
const (
	musicDefaultSize   = 60
	musicMaxSize       = 200
	musicMaxTrackSize  = 1000
	librarySearchFew   = 5
	librarySearchMany  = 20
	librarySearchMinim = 2
)

// musicLibrary loads a library the caller may see and checks it is music.
func (s *server) musicLibrary(c echo.Context) (store.Library, error) {
	lib, err := s.store.Library(c.Request().Context(), c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), lib.ID) {
		return store.Library{}, echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	if lib.MediaType != store.MediaMusic {
		return store.Library{}, echo.NewHTTPError(http.StatusBadRequest, "not a music library")
	}
	return lib, nil
}

// musicQuery reads a listing's filters, sort and page from the request.
func musicQuery(c echo.Context, maxSize int) store.MusicQuery {
	q := store.MusicQuery{
		Query:  c.QueryParam("q"),
		Artist: c.QueryParam("artist"),
		Genre:  c.QueryParam("genre"),
		Sort:   c.QueryParam("sort"),
		Letter: c.QueryParam("letter"),
	}
	q.Year, _ = strconv.Atoi(c.QueryParam("year"))
	q.Decade, _ = strconv.Atoi(c.QueryParam("decade"))
	q.Compilations, _ = strconv.ParseBool(c.QueryParam("compilations"))
	q.Page, _ = strconv.Atoi(c.QueryParam("page"))
	q.Page = max(q.Page, 1)
	size, err := strconv.Atoi(c.QueryParam("size"))
	if err != nil {
		size = musicDefaultSize
	}
	q.Size = min(max(size, 1), maxSize)
	return q
}

// GET /libraries/:id/artists — one page of a music library's artists.
func (s *server) libraryArtists(c echo.Context) error {
	lib, err := s.musicLibrary(c)
	if err != nil {
		return err
	}
	page, err := s.store.Artists(c.Request().Context(), userID(c), lib.ID, musicQuery(c, musicMaxSize))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, page)
}

// GET /libraries/:id/albums — one page of a music library's albums.
func (s *server) libraryAlbums(c echo.Context) error {
	lib, err := s.musicLibrary(c)
	if err != nil {
		return err
	}
	page, err := s.store.Albums(c.Request().Context(), userID(c), lib.ID, musicQuery(c, musicMaxSize))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, page)
}

// GET /libraries/:id/tracks — one page of a music library's tracks.
func (s *server) libraryTracks(c echo.Context) error {
	lib, err := s.musicLibrary(c)
	if err != nil {
		return err
	}
	page, err := s.store.Tracks(c.Request().Context(), userID(c), lib.ID, musicQuery(c, musicMaxTrackSize))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, page)
}

// GET /libraries/:id/search?q= — a music library's search box: a few
// artists and albums and some tracks matching every word typed.
func (s *server) librarySearch(c echo.Context) error {
	lib, err := s.musicLibrary(c)
	if err != nil {
		return err
	}
	q := c.QueryParam("q")
	if len(q) < librarySearchMinim {
		return c.JSON(http.StatusOK, store.LibrarySearch{Artists: []store.Item{}, Albums: []store.Item{}, Tracks: []store.Item{}})
	}
	res, err := s.store.SearchLibrary(c.Request().Context(), userID(c), lib.ID, q, librarySearchFew, librarySearchMany)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, res)
}

// GET /items/:id/tracks — every track under an artist or album in play
// order (?shuffle=true shuffles them), with the viewer's plays and
// ratings: what Play, Shuffle and Add to queue send to the queue.
func (s *server) itemTracks(c echo.Context) error {
	ctx := c.Request().Context()
	item, err := s.store.Item(ctx, c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), item.LibraryID) {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	if item.Kind != store.KindArtist && item.Kind != store.KindAlbum {
		return echo.NewHTTPError(http.StatusBadRequest, "item is not an artist or album")
	}
	tracks, err := s.store.TracksUnder(ctx, userID(c), item)
	if err != nil {
		return err
	}
	if shuffle, _ := strconv.ParseBool(c.QueryParam("shuffle")); shuffle {
		rand.Shuffle(len(tracks), func(i, j int) { tracks[i], tracks[j] = tracks[j], tracks[i] })
	}
	return c.JSON(http.StatusOK, tracks)
}

// GET /items/:id/lyrics — a track's lyrics, from its tags or an .lrc file
// beside it; 404 when it has none.
func (s *server) itemLyrics(c echo.Context) error {
	ctx := c.Request().Context()
	item, err := s.store.Item(ctx, c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), item.LibraryID) {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	l, err := s.store.LyricsFor(ctx, item.ID)
	if err != nil {
		return err
	}
	if l == nil {
		return echo.NewHTTPError(http.StatusNotFound, "no lyrics")
	}
	return c.JSON(http.StatusOK, l)
}
