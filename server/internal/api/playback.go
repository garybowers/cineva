package api

import (
	"net/http"
	"path/filepath"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/store"
)

// directPlayable: containers/codecs browsers handle natively. Anything else
// goes through an HLS session (video copied, audio transcoded to AAC).
func directPlayable(item store.Item) bool {
	okContainer := item.Container == "mp4" || item.Container == "m4v" || item.Container == "webm"
	okVideo := item.VideoCodec == "h264" || item.VideoCodec == "vp9" || item.VideoCodec == "av1"
	okAudio := item.AudioCodec == "aac" || item.AudioCodec == "mp3" || item.AudioCodec == "opus" || item.AudioCodec == "vorbis" || item.AudioCodec == ""
	return okContainer && okVideo && okAudio
}

type playInfo struct {
	Mode         string `json:"mode"` // "direct" | "hls"
	URL          string `json:"url"`
	SessionID    string `json:"sessionId,omitempty"`
	DurationSecs int    `json:"durationSecs"`
	PositionSecs int    `json:"positionSecs"` // resume point
}

func (s *server) playableItem(c echo.Context) (store.Item, error) {
	item, err := s.store.Item(c.Request().Context(), c.Param("id"))
	if err != nil {
		return store.Item{}, echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	if item.Kind != store.KindMovie && item.Kind != store.KindEpisode {
		return store.Item{}, echo.NewHTTPError(http.StatusBadRequest, "item is not playable")
	}
	return item, nil
}

// POST /items/:id/play — decide direct play vs HLS and start a session if needed.
func (s *server) playItem(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	ps, err := s.store.PlayStateFor(c.Request().Context(), store.LocalUser, item.ID)
	if err != nil {
		return err
	}
	resume := ps.PositionSecs
	if ps.Watched {
		resume = 0
	}

	if directPlayable(item) {
		return c.JSON(http.StatusOK, playInfo{
			Mode: "direct", URL: "/api/v1/items/" + item.ID + "/stream",
			DurationSecs: item.RuntimeSecs, PositionSecs: resume,
		})
	}
	session, err := s.sessions.Start(item.ID, item.Title, item.Path, item.RuntimeSecs, resume)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, playInfo{
		Mode: "hls", URL: "/api/v1/play/" + session.ID + "/index.m3u8",
		SessionID: session.ID, DurationSecs: item.RuntimeSecs, PositionSecs: resume,
	})
}

// GET /items/:id/stream — direct play with HTTP range support.
func (s *server) streamItem(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	return c.File(item.Path)
}

// GET /items/:id/download — original file as attachment.
func (s *server) downloadItem(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	return c.Attachment(item.Path, filepath.Base(item.Path))
}

// GET /play/:sid/index.m3u8 | /play/:sid/:file — HLS session assets.
func (s *server) hlsPlaylist(c echo.Context) error {
	session, ok := s.sessions.Get(c.Param("sid"))
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	}
	return c.Blob(http.StatusOK, "application/vnd.apple.mpegurl", []byte(session.Playlist()))
}

func (s *server) hlsFile(c echo.Context) error {
	session, ok := s.sessions.Get(c.Param("sid"))
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	}
	path, err := session.EnsureFile(c.Request().Context(), c.Param("file"))
	if err != nil {
		return echo.NewHTTPError(http.StatusGatewayTimeout, err.Error())
	}
	return c.File(path)
}

func (s *server) stopSession(c echo.Context) error {
	s.sessions.Stop(c.Param("sid"))
	return c.NoContent(http.StatusNoContent)
}

func (s *server) listSessions(c echo.Context) error {
	return c.JSON(http.StatusOK, s.sessions.Sessions())
}

// POST /items/:id/progress — update watch state.
func (s *server) postProgress(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	var req struct {
		PositionSecs int `json:"positionSecs"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ps, err := s.store.SetProgress(c.Request().Context(), store.LocalUser, item.ID, req.PositionSecs, item.RuntimeSecs)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, ps)
}

// GET /items/:id/next — the server-side next-up decision (see CLAUDE.md).
func (s *server) nextUp(c echo.Context) error {
	next, ok, err := s.store.NextEpisode(c.Request().Context(), c.Param("id"))
	if err != nil {
		return err
	}
	if !ok {
		return c.NoContent(http.StatusNoContent)
	}
	return c.JSON(http.StatusOK, next)
}

// GET /continue-watching — resume row for the home screen.
func (s *server) continueWatching(c echo.Context) error {
	items, err := s.store.ContinueWatching(c.Request().Context(), store.LocalUser, 20)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, items)
}
