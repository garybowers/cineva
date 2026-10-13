package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/store"
)

const (
	homeRowSize = 20
	// Recommendation rows per library. The library's own Recommended tab
	// has all of them; home is a sampler across libraries, and every row of
	// every library would bury the rows above it.
	homeRecRows = 2
)

// homeShelf is one row of items from one library.
type homeShelf struct {
	LibraryID   string       `json:"libraryId"`
	LibraryName string       `json:"libraryName"`
	Kind        string       `json:"kind"` // "recent", or a recommendation row's kind
	Title       string       `json:"title"`
	Genre       string       `json:"genre,omitempty"`
	Items       []store.Item `json:"items"`
}

type homeResponse struct {
	ContinueWatching []store.ResumeItem `json:"continueWatching"`
	// NextUp is the next unstarted episode of each series the viewer has
	// been watching, once they have finished the one before — an episode
	// they are part-way through is in ContinueWatching instead.
	NextUp  []store.ResumeItem `json:"nextUp"`
	Shelves []homeShelf        `json:"shelves"`
}

// GET /home — everything the home screen shows, in one request, limited to
// the libraries the viewer may see.
func (s *server) home(c echo.Context) error {
	ctx := c.Request().Context()
	me, uid := currentUser(c), userID(c)
	resp := homeResponse{ContinueWatching: []store.ResumeItem{}, NextUp: []store.ResumeItem{}, Shelves: []homeShelf{}}

	resume, err := s.store.ContinueWatching(ctx, uid, visibleLibraryIDs(me), homeRowSize)
	if err != nil {
		return err
	}
	// One card per series: three half-watched episodes of a show are one
	// thing to carry on with, and the latest touched is the one meant.
	inProgress := map[string]bool{} // series with an episode part-watched
	for _, r := range resume {
		if r.SeriesID != "" && inProgress[r.SeriesID] {
			continue
		}
		resp.ContinueWatching = append(resp.ContinueWatching, r)
		if r.SeriesID != "" {
			inProgress[r.SeriesID] = true
		}
	}

	series, err := s.store.RecentSeries(ctx, uid, homeRowSize)
	if err != nil {
		return err
	}
	for _, sr := range series {
		if inProgress[sr.ID] || !canSeeLibrary(me, sr.LibraryID) {
			continue
		}
		up, ok, err := s.store.NextUp(ctx, uid, sr.ID)
		if err != nil {
			return err
		}
		// "restart" means every episode is watched: the series is done,
		// not next up.
		if !ok || up.Reason != "next" {
			continue
		}
		resp.NextUp = append(resp.NextUp, store.ResumeItem{
			Item: up.Item, SeriesID: sr.ID, SeriesTitle: sr.Title,
			SeriesPoster: sr.Poster, SeriesBackdrop: sr.Backdrop,
		})
	}

	libs, err := s.store.Libraries(ctx)
	if err != nil {
		return err
	}
	for _, lib := range libs {
		if !canSeeLibrary(me, lib.ID) {
			continue
		}
		if recent, err := s.store.RecentlyAdded(ctx, lib, homeRowSize); err == nil && len(recent) > 0 {
			resp.Shelves = append(resp.Shelves, homeShelf{
				LibraryID: lib.ID, LibraryName: lib.Name, Kind: "recent",
				Title: "Recently added", Items: s.withPlayState(c, lib.ID, recent),
			})
		}
		if lib.MediaType == store.MediaMusic {
			shelves, err := s.musicShelves(c, lib)
			if err != nil {
				return err
			}
			resp.Shelves = append(resp.Shelves, shelves...)
		}
	}
	// Recommendations after every library's recently added row, so the
	// newest things are near the top whichever library they are in.
	for _, lib := range libs {
		if !canSeeLibrary(me, lib.ID) || lib.MediaType == store.MediaMusic {
			continue
		}
		rows, err := s.store.Recommendations(ctx, uid, lib.ID)
		if err != nil {
			return err
		}
		for _, row := range rows[:min(len(rows), homeRecRows)] {
			if len(row.Items) == 0 {
				continue
			}
			resp.Shelves = append(resp.Shelves, homeShelf{
				LibraryID: lib.ID, LibraryName: lib.Name, Kind: row.Kind,
				Title: row.Title, Genre: row.Genre, Items: s.withPlayState(c, lib.ID, row.Items),
			})
		}
	}
	return c.JSON(http.StatusOK, resp)
}

// musicShelves are a music library's listening rows for the home screen:
// the albums the viewer played last and played most. Albums rather than
// tracks, since a home row is cards to open; the music library's own page
// has the most played tracks.
func (s *server) musicShelves(c echo.Context, lib store.Library) ([]homeShelf, error) {
	ctx, uid := c.Request().Context(), userID(c)
	var out []homeShelf
	recent, err := s.store.RecentlyPlayedAlbums(ctx, uid, lib.ID, homeRowSize)
	if err != nil {
		return nil, err
	}
	if len(recent) > 0 {
		out = append(out, homeShelf{LibraryID: lib.ID, LibraryName: lib.Name, Kind: "played", Title: "Recently played", Items: recent})
	}
	top, err := s.store.MostPlayedAlbums(ctx, uid, lib.ID, homeRowSize)
	if err != nil {
		return nil, err
	}
	// A handful of plays is not yet a "most played" worth a row: it would
	// repeat the recently played one.
	if len(top) >= 4 {
		out = append(out, homeShelf{LibraryID: lib.ID, LibraryName: lib.Name, Kind: "mostPlayed", Title: "Most played", Items: top})
	}
	return out, nil
}
