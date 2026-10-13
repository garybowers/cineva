package api

import (
	"net/http"
	"os"
	"sync"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/media"
)

var trickplayLog = logging.For("trickplay")

// Seek-preview thumbnails are generated on demand, once per item, and cached
// on disk beside the transcode scratch.
//
// Lazily rather than during the scan: generating them for a whole library up
// front is hours of decoding for films nobody may watch, and the first
// viewer only loses the preview for the half-minute it takes.
type trickplayJobs struct {
	mu      sync.Mutex
	running map[string]bool
}

func newTrickplayJobs() *trickplayJobs {
	return &trickplayJobs{running: map[string]bool{}}
}

// claim reports whether the caller should generate this item, marking it in
// flight. Several players asking at once must not start several jobs on the
// same file.
func (t *trickplayJobs) claim(itemID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running[itemID] {
		return false
	}
	t.running[itemID] = true
	return true
}

func (t *trickplayJobs) release(itemID string) {
	t.mu.Lock()
	delete(t.running, itemID)
	t.mu.Unlock()
}

// GET /items/:id/trickplay.vtt — the cue index for the sprite sheet.
//
// 202 while nothing has been generated yet, with generation kicked off in
// the background on the first miss. 202 rather than 404 because the request
// was understood and the work is now under way — nothing is missing or
// wrong, and a 404 here put a failed request in every viewer's console and
// in the server log for what is ordinary first-play behaviour. The player
// shows no previews either way and picks them up on the next load.
func (s *server) trickplayVTT(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	_, vtt := media.TrickplayPaths(s.trickplayDir, item.ID)
	if _, err := os.Stat(vtt); err != nil {
		s.startTrickplay(item.ID, item.Path, item.RuntimeSecs)
		return c.NoContent(http.StatusAccepted)
	}
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

// startTrickplay generates previews in the background, at most one job per
// item. Failures are logged and not retried in a loop: the next request
// starts a fresh attempt, which is enough for a transient problem and does
// not hammer a file that cannot be decoded.
func (s *server) startTrickplay(itemID, path string, runtimeSecs int) {
	if runtimeSecs <= 0 || !s.trickplay.claim(itemID) {
		return
	}
	go func() {
		defer s.trickplay.release(itemID)
		if err := media.GenerateTrickplay(path, s.trickplayDir, itemID, runtimeSecs); err != nil {
			trickplayLog.Warn("preview generation failed", "item", itemID, "err", err)
		}
	}()
}
