package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/store"
)

// Music beyond the item tree: artist pages, listening history rows and
// playlists. The queries are in the store; these scope them to what the
// viewer may see.

// Row sizes for the listening rows and an artist's top tracks.
const (
	listeningRowSize = 20
	artistTopTracks  = 10
)

// artistPage is everything an artist view shows, in one response.
type artistPage struct {
	Artist store.Item `json:"artist"`
	// Albums are the artist's own, oldest first; AppearsOn are other
	// artists' albums — compilations, soundtracks — with a track of
	// theirs on.
	Albums    []store.Item `json:"albums"`
	AppearsOn []store.Item `json:"appearsOn"`
	// TopTracks are ranked by the viewer's plays, then the household's.
	TopTracks []store.Item      `json:"topTracks"`
	Stats     store.ArtistStats `json:"stats"`
	// BioURL is where the biography (the artist's overview) came from.
	BioURL string `json:"bioUrl,omitempty"`
	// LookingUp is true while a picture and biography are being fetched;
	// the client can ask again shortly (an "items" event also says when).
	LookingUp bool `json:"lookingUp,omitempty"`
}

// GET /items/:id/artist — an artist's page. Opening it is also what starts
// a lookup of the artist's picture and biography, when one is due.
func (s *server) getArtist(c echo.Context) error {
	ctx := c.Request().Context()
	artist, err := s.store.Item(ctx, c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), artist.LibraryID) {
		return echo.NewHTTPError(http.StatusNotFound, "artist not found")
	}
	if artist.Kind != store.KindArtist {
		return echo.NewHTTPError(http.StatusBadRequest, "item is not an artist")
	}
	page := artistPage{Artist: artist}
	if page.Albums, err = s.store.ItemsByParent(ctx, artist.ID); err != nil {
		return err
	}
	sort.SliceStable(page.Albums, func(i, j int) bool {
		a, b := page.Albums[i], page.Albums[j]
		if a.Year != b.Year {
			return a.Year < b.Year
		}
		return a.Title < b.Title
	})
	if page.AppearsOn, err = s.store.ArtistAppearsOn(ctx, artist); err != nil {
		return err
	}
	if page.TopTracks, err = s.store.ArtistTopTracks(ctx, userID(c), artist, artistTopTracks); err != nil {
		return err
	}
	page.TopTracks = s.withRatings(c, page.TopTracks)
	if page.Stats, err = s.store.ArtistStats(ctx, userID(c), artist.ID); err != nil {
		return err
	}
	info, err := s.store.ArtistInfoFor(ctx, artist.ID)
	if err != nil {
		return err
	}
	page.BioURL = info.BioURL
	if s.manager != nil {
		due, err := s.manager.ArtistLookupDue(ctx, artist)
		if err != nil {
			return err
		}
		if due {
			s.manager.LookUpArtistInBackground(artist)
			page.LookingUp = true
		}
	}
	return c.JSON(http.StatusOK, page)
}

// listening is a music library's history rows for the viewer.
type listening struct {
	RecentlyPlayed   []store.Item `json:"recentlyPlayed"`
	MostPlayed       []store.Item `json:"mostPlayed"`
	MostPlayedAlbums []store.Item `json:"mostPlayedAlbums"`
}

// GET /libraries/:id/listening — the viewer's recently played albums and
// most played tracks and albums in a music library.
func (s *server) libraryListening(c echo.Context) error {
	ctx := c.Request().Context()
	lib, err := s.store.Library(ctx, c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), lib.ID) {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	if lib.MediaType != store.MediaMusic {
		return echo.NewHTTPError(http.StatusBadRequest, "not a music library")
	}
	var out listening
	uid := userID(c)
	if out.RecentlyPlayed, err = s.store.RecentlyPlayedAlbums(ctx, uid, lib.ID, listeningRowSize); err != nil {
		return err
	}
	if out.MostPlayed, err = s.store.MostPlayedTracks(ctx, uid, lib.ID, listeningRowSize); err != nil {
		return err
	}
	out.MostPlayed = s.withRatings(c, out.MostPlayed)
	if out.MostPlayedAlbums, err = s.store.MostPlayedAlbums(ctx, uid, lib.ID, listeningRowSize); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// playlistError maps the store's playlist errors onto HTTP.
func playlistError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "playlist not found")
	case errors.Is(err, store.ErrInvalidPlaylist):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return err
}

// playlistBody is a create or update request. On update, absent fields
// are left alone.
type playlistBody struct {
	Name  *string           `json:"name"`
	Smart *store.SmartRules `json:"smart"`
}

// GET /playlists — the viewer's playlists.
// GET /playlists — the user's playlists by name; with ?for=<track> the
// ones to add that track to, most recently added to first, each saying
// whether it holds the track already (server/docs/mix.md).
func (s *server) listPlaylists(c echo.Context) error {
	libs := visibleLibraryIDs(currentUser(c))
	var lists []store.Playlist
	var err error
	if forTrack := c.QueryParam("for"); forTrack != "" {
		lists, err = s.store.PlaylistsFor(c.Request().Context(), userID(c), libs, forTrack)
	} else {
		lists, err = s.store.Playlists(c.Request().Context(), userID(c), libs)
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, lists)
}

// DELETE /playlists/:id/items/last?count=n — undo an append: the last n
// entries (1 by default) go. Answers how many went.
func (s *server) removeLastPlaylistItems(c echo.Context) error {
	n, err := strconv.Atoi(c.QueryParam("count"))
	if err != nil || n < 1 {
		n = 1
	}
	removed, err := s.store.RemoveLastPlaylistTracks(c.Request().Context(), userID(c), c.Param("id"), min(n, 1000))
	if err != nil {
		return playlistError(err)
	}
	return c.JSON(http.StatusOK, map[string]int{"removed": removed})
}

// POST /playlists — a new playlist: manual, or smart when rules are given.
func (s *server) createPlaylist(c echo.Context) error {
	var body playlistBody
	if err := c.Bind(&body); err != nil || body.Name == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "a playlist needs a name")
	}
	p, err := s.store.CreatePlaylist(c.Request().Context(), userID(c), *body.Name, body.Smart)
	if err != nil {
		return playlistError(err)
	}
	return c.JSON(http.StatusCreated, p)
}

// playlistDetail is a playlist and its tracks, in order.
type playlistDetail struct {
	Playlist store.Playlist `json:"playlist"`
	Tracks   []store.Item   `json:"tracks"`
}

func (s *server) playlistDetail(c echo.Context, status int) error {
	p, tracks, err := s.store.PlaylistWithTracks(c.Request().Context(), userID(c), c.Param("id"),
		visibleLibraryIDs(currentUser(c)))
	if err != nil {
		return playlistError(err)
	}
	return c.JSON(status, playlistDetail{Playlist: p, Tracks: tracks})
}

// GET /playlists/:id — a playlist with its tracks; a smart one evaluated
// now.
func (s *server) getPlaylist(c echo.Context) error {
	return s.playlistDetail(c, http.StatusOK)
}

// PATCH /playlists/:id — rename, or change a smart playlist's rules.
func (s *server) updatePlaylist(c echo.Context) error {
	var body playlistBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := s.store.UpdatePlaylist(c.Request().Context(), userID(c), c.Param("id"), body.Name, body.Smart); err != nil {
		return playlistError(err)
	}
	return s.playlistDetail(c, http.StatusOK)
}

// DELETE /playlists/:id
func (s *server) deletePlaylist(c echo.Context) error {
	if err := s.store.DeletePlaylist(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return playlistError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// visibleTracks expands the ids in a request into the tracks they stand
// for (an album its tracks, an artist their albums), keeping only those
// the viewer may see.
func (s *server) visibleTracks(c echo.Context) ([]string, error) {
	var body struct {
		ItemIDs []string `json:"itemIds"`
	}
	if err := c.Bind(&body); err != nil || body.ItemIDs == nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "itemIds is required")
	}
	tracks, err := s.store.ExpandTracks(c.Request().Context(), body.ItemIDs)
	if err != nil {
		return nil, err
	}
	me := currentUser(c)
	ids := make([]string, 0, len(tracks))
	for _, t := range tracks {
		if canSeeLibrary(me, t.LibraryID) {
			ids = append(ids, t.ID)
		}
	}
	return ids, nil
}

// POST /playlists/:id/items — add tracks, albums or artists to the end of
// a manual playlist.
func (s *server) appendPlaylistItems(c echo.Context) error {
	ids, err := s.visibleTracks(c)
	if err != nil {
		return err
	}
	if err := s.store.AppendPlaylistTracks(c.Request().Context(), userID(c), c.Param("id"), ids); err != nil {
		return playlistError(err)
	}
	return s.playlistDetail(c, http.StatusOK)
}

// PUT /playlists/:id/items — replace a manual playlist's tracks with these,
// in this order: how a client reorders or removes.
func (s *server) setPlaylistItems(c echo.Context) error {
	ids, err := s.visibleTracks(c)
	if err != nil {
		return err
	}
	if err := s.store.SetPlaylistTracks(c.Request().Context(), userID(c), c.Param("id"), ids); err != nil {
		return playlistError(err)
	}
	return s.playlistDetail(c, http.StatusOK)
}
