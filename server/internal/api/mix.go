package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/mix"
	"github.com/garybowers/cineva/server/internal/store"
)

// GET /items/:id/mix — one batch of a mix grown from a track, an album or
// an artist (server/docs/mix.md). The engine decides; this scopes it to
// what the viewer may see and reads the request.
func (s *server) itemMix(c echo.Context) error {
	ctx := c.Request().Context()
	me := currentUser(c)
	item, err := s.store.Item(ctx, c.Param("id"))
	if err != nil || !canSeeLibrary(me, item.LibraryID) {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	if item.Kind != store.KindTrack && item.Kind != store.KindAlbum && item.Kind != store.KindArtist {
		return echo.NewHTTPError(http.StatusBadRequest, "a mix grows from a track, an album or an artist")
	}
	if s.mixes == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "mixes are not available")
	}
	o := mix.Options{LibraryIDs: visibleLibraryIDs(me)}
	o.Size, _ = strconv.Atoi(c.QueryParam("size"))
	o.Start, _ = strconv.ParseBool(c.QueryParam("start"))
	if ex := c.QueryParam("exclude"); ex != "" {
		for _, id := range strings.Split(ex, ",") {
			if id = strings.TrimSpace(id); id != "" {
				o.Exclude = append(o.Exclude, id)
			}
			if len(o.Exclude) >= mix.MaxExclude {
				break
			}
		}
	}
	if after := c.QueryParam("after"); after != "" {
		a, err := s.store.Item(ctx, after)
		if err == nil && canSeeLibrary(me, a.LibraryID) && a.Kind == store.KindTrack {
			o.After = &a
		}
	}
	res, err := s.mixes.Mix(ctx, me.ID, item, o)
	if errors.Is(err, mix.ErrSeedKind) {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, res)
}
