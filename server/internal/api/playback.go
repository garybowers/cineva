package api

import (
	"net/http"
	"path/filepath"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

// capabilities are what the requesting device claims it can decode/demux.
// The client detects them via canPlayType/MediaSource.isTypeSupported and
// sends them with the play request; the server only transcodes what the
// device actually lacks.
type capabilities struct {
	Containers  []string `json:"containers"`
	VideoCodecs []string `json:"videoCodecs"`
	AudioCodecs []string `json:"audioCodecs"`
	// Quality is the user's Plex-style playback choice:
	// "" / "auto"  — negotiate: direct play if capable, else remux
	// "original"   — force direct play of the untouched file
	// "1080p", "720p", "480p" — force a video transcode at that level
	Quality string `json:"quality"`
}

// qualityLevels maps the selectable transcode levels to encode parameters.
var qualityLevels = map[string]media.Quality{
	"1080p": {Label: "1080p", Height: 1080, BitrateKbps: 8000},
	"720p":  {Label: "720p", Height: 720, BitrateKbps: 4000},
	"480p":  {Label: "480p", Height: 480, BitrateKbps: 2500},
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// defaultCaps: the conservative baseline used when a client sends no
// capabilities (curl, old clients): plain MP4 with universal codecs.
var defaultCaps = capabilities{
	Containers:  []string{"mp4", "m4v"},
	VideoCodecs: []string{"h264"},
	AudioCodecs: []string{"aac", "mp3"},
}

// directPlayReason returns "" when the device can direct play the item, else
// a human-readable reason for transcoding.
func directPlayReason(item store.Item, caps capabilities) string {
	if !contains(caps.Containers, item.Container) {
		return "container " + item.Container + " not supported by device"
	}
	if item.VideoCodec != "" && !contains(caps.VideoCodecs, item.VideoCodec) {
		return "video codec " + item.VideoCodec + " not supported by device"
	}
	if item.AudioCodec != "" && !contains(caps.AudioCodecs, item.AudioCodec) {
		return "audio codec " + item.AudioCodec + " not supported by device"
	}
	return ""
}

type playInfo struct {
	Mode         string `json:"mode"` // "direct" | "hls"
	URL          string `json:"url"`
	SessionID    string `json:"sessionId,omitempty"`
	DurationSecs int    `json:"durationSecs"`
	PositionSecs int    `json:"positionSecs"`     // resume point
	Reason       string `json:"reason,omitempty"` // why transcoding was chosen
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

// POST /items/:id/play — decide direct play vs HLS and start a session if
// needed. The request body optionally carries the device's capabilities.
func (s *server) playItem(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	caps := defaultCaps
	var body capabilities
	if err := c.Bind(&body); err == nil && len(body.Containers) > 0 {
		caps = capabilities{Containers: body.Containers, VideoCodecs: body.VideoCodecs, AudioCodecs: body.AudioCodecs}
	}
	quality := body.Quality
	ps, err := s.store.PlayStateFor(c.Request().Context(), store.LocalUser, item.ID)
	if err != nil {
		return err
	}
	resume := ps.PositionSecs
	if ps.Watched {
		resume = 0
	}

	direct := playInfo{
		Mode: "direct", URL: "/api/v1/items/" + item.ID + "/stream",
		DurationSecs: item.RuntimeSecs, PositionSecs: resume,
	}

	var sessionQuality media.Quality // zero value = remux (video copy)
	var reason string
	switch {
	case quality == "original":
		return c.JSON(http.StatusOK, direct)
	case quality == "" || quality == "auto":
		if reason = directPlayReason(item, caps); reason == "" {
			return c.JSON(http.StatusOK, direct)
		}
		sessionQuality = media.Quality{Label: "auto"}
	default:
		level, ok := qualityLevels[quality]
		if !ok {
			return echo.NewHTTPError(http.StatusBadRequest, "unknown quality "+quality)
		}
		sessionQuality = level
		reason = "quality " + quality + " requested"
	}

	session, err := s.sessions.Start(item.ID, item.Title, item.Path, item.RuntimeSecs, resume, sessionQuality)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, playInfo{
		Mode: "hls", URL: "/api/v1/play/" + session.ID + "/index.m3u8",
		SessionID: session.ID, DurationSecs: item.RuntimeSecs, PositionSecs: resume,
		Reason: reason,
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
