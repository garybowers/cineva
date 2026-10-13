package api

import (
	"net/http"
	"os"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

// Seek-preview thumbnails are built in the background by s.previews
// (internal/trickplay), under the same rules as intro detection: never while
// anything plays or scans, only when the admin's schedule allows, and no
// faster than the read-speed cap. Every film and episode is queued after
// each scan, so previews are usually there before the first play.

// GET /items/:id/trickplay.vtt — the cue index for the sprite sheet.
//
// 202 while the previews do not exist yet (or are older than the file),
// with the title moved to the front of the queue. 202 rather than 404
// because the request was understood and the work is queued — nothing is
// missing or wrong, and a 404 here put a failed request in every viewer's
// console for ordinary behaviour. The previews are never built while the
// title is playing, so the player shows none this time and picks them up on
// a later play.
func (s *server) trickplayVTT(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	if item.Kind == store.KindTrack {
		return echo.NewHTTPError(http.StatusNotFound, "no previews for audio")
	}
	p, err := s.store.PreviewItem(c.Request().Context(), item.ID)
	if err != nil {
		return err
	}
	if !s.previews.Has(item.ID, p.FileMtime) {
		s.previews.Request(p)
		return c.NoContent(http.StatusAccepted)
	}
	_, vtt := media.TrickplayPaths(s.trickplayDir, item.ID)
	return c.File(vtt)
}

// GET /items/:id/trickplay.jpg — the sprite sheet the cues address.
func (s *server) trickplaySheet(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	sheet, _ := media.TrickplayPaths(s.trickplayDir, item.ID)
	if _, err := os.Stat(sheet); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "previews not ready")
	}
	return c.File(sheet)
}

// GET /previews/status — what seek-bar preview building is doing, for the
// admin's activity page. Also pushed over /events as "previews".
func (s *server) previewStatus(c echo.Context) error {
	return c.JSON(http.StatusOK, s.previews.Status())
}

// enqueueBackground queues a film or TV library for the background work that
// follows a scan: seek-bar previews for both, intro and credits detection
// for TV.
func (s *server) enqueueBackground(lib store.Library) {
	switch lib.MediaType {
	case store.MediaTV:
		if s.segments != nil {
			s.segments.Enqueue(lib.ID)
		}
		s.previews.EnqueueLibrary(lib.ID)
	case store.MediaMovies:
		s.previews.EnqueueLibrary(lib.ID)
	}
}
