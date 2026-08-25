package api

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
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
	// FromStart ignores the stored resume position and plays from 00:00.
	FromStart bool `json:"fromStart"`
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
	Mode         string         `json:"mode"` // "direct" | "hls"
	URL          string         `json:"url"`
	SessionID    string         `json:"sessionId,omitempty"`
	DurationSecs int            `json:"durationSecs"`
	PositionSecs int            `json:"positionSecs"`     // resume point
	Reason       string         `json:"reason,omitempty"` // why transcoding was chosen
	Subtitles    []subtitleInfo `json:"subtitles"`
}

func (s *server) playableItem(c echo.Context) (store.Item, error) {
	item, err := s.store.Item(c.Request().Context(), c.Param("id"))
	if err != nil {
		return store.Item{}, echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	switch item.Kind {
	case store.KindMovie, store.KindEpisode, store.KindTrack:
		return item, nil
	}
	return store.Item{}, echo.NewHTTPError(http.StatusBadRequest, "item is not playable")
}

// POST /items/:id/play — decide direct play vs HLS and start a session if
// needed. The request body optionally carries the device's capabilities.
func (s *server) playItem(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	// Play history for the dashboard; a failure here must not block playback.
	if err := s.store.RecordPlay(c.Request().Context(), store.LocalUser, item.ID, item.Kind); err != nil {
		log.Printf("record play: %v", err)
	} else {
		s.events.Publish(events.Event{Type: "plays"})
	}
	// Music always direct plays: browsers decode flac/mp3/aac/ogg/opus
	// natively, and the files are small. (Transcode fallback: BACKLOG.md.)
	if item.Kind == store.KindTrack {
		return c.JSON(http.StatusOK, playInfo{
			Mode: "direct", URL: "/api/v1/items/" + item.ID + "/stream",
			DurationSecs: item.RuntimeSecs, Subtitles: []subtitleInfo{},
		})
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
	if ps.Watched || body.FromStart {
		resume = 0
	}

	tracks, err := s.store.SubtitlesForItem(c.Request().Context(), item.ID)
	if err != nil {
		return err
	}
	subs := subtitleInfos(tracks)

	direct := playInfo{
		Mode: "direct", URL: "/api/v1/items/" + item.ID + "/stream",
		DurationSecs: item.RuntimeSecs, PositionSecs: resume,
		Subtitles: subs,
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
		Reason: reason, Subtitles: subs,
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

// GET /items/:id/download — a playable item's original file as an
// attachment; container kinds (season, series, album, artist) stream a
// stored ZIP of every descendant file.
func (s *server) downloadItem(c echo.Context) error {
	ctx := c.Request().Context()
	item, err := s.store.Item(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	format := c.QueryParam("format")
	quality, _ := strconv.Atoi(c.QueryParam("quality"))
	switch item.Kind {
	case store.KindTrack:
		if format != "" && format != "original" {
			return s.downloadTranscoded(c, item, format, quality)
		}
		return c.Attachment(item.Path, filepath.Base(item.Path))
	case store.KindMovie, store.KindEpisode:
		return c.Attachment(item.Path, filepath.Base(item.Path))
	case store.KindSeason, store.KindSeries, store.KindAlbum, store.KindArtist:
		return s.downloadArchive(c, item, format, quality)
	}
	return echo.NewHTTPError(http.StatusBadRequest, "item is not downloadable")
}

var allowedBitrates = map[int]bool{128: true, 192: true, 256: true, 320: true}

// transcodedTrack ensures a cached transcode of a track exists and returns
// its path plus the download filename.
func (s *server) transcodedTrack(ctx context.Context, item store.Item, format string, quality int) (string, string, error) {
	f, ok := media.AudioDownloadFormat(format)
	if !ok {
		return "", "", echo.NewHTTPError(http.StatusBadRequest, "unknown format "+format)
	}
	if !allowedBitrates[quality] {
		quality = 192
	}
	dir := filepath.Join(s.dlCacheDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	cached := filepath.Join(dir, fmt.Sprintf("%s-%s-%d.%s", item.ID, format, quality, f.Ext))
	// A crashed transcode can leave an empty/partial file: only trust
	// non-empty caches, and build new ones atomically via rename.
	if st, err := os.Stat(cached); err != nil || st.Size() == 0 {
		part := cached + ".part." + f.Ext // keep the extension so the muxer resolves
		if err := media.TranscodeAudio(ctx, item.Path, part, f.Encoder, quality); err != nil {
			os.Remove(part)
			return "", "", echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		if err := os.Rename(part, cached); err != nil {
			return "", "", err
		}
	}
	nice := item.Title
	if item.Artist != "" {
		nice = item.Artist + " - " + item.Title
	}
	return cached, zipNameSanitiser.Replace(nice) + "." + f.Ext, nil
}

func (s *server) downloadTranscoded(c echo.Context, item store.Item, format string, quality int) error {
	path, name, err := s.transcodedTrack(c.Request().Context(), item, format, quality)
	if err != nil {
		return err
	}
	return c.Attachment(path, name)
}

var zipNameSanitiser = strings.NewReplacer("/", "-", "\\", "-", "\x00", "")

// downloadArchive streams a ZIP (stored, uncompressed — media is already
// compressed) of all files beneath a container item, preserving one level
// of folder structure per container ("Season 02/…", "Album/…").
func (s *server) downloadArchive(c echo.Context, root store.Item, format string, quality int) error {
	ctx := c.Request().Context()
	type entry struct {
		path  string
		name  string
		track *store.Item
	}
	var files []entry
	var walk func(id, prefix string) error
	walk = func(id, prefix string) error {
		children, err := s.store.ItemsByParent(ctx, id)
		if err != nil {
			return err
		}
		for _, ch := range children {
			switch ch.Kind {
			case store.KindTrack:
				t := ch
				files = append(files, entry{path: ch.Path, name: prefix + filepath.Base(ch.Path), track: &t})
			case store.KindMovie, store.KindEpisode:
				files = append(files, entry{path: ch.Path, name: prefix + filepath.Base(ch.Path)})
			default:
				if err := walk(ch.ID, prefix+zipNameSanitiser.Replace(ch.Title)+"/"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root.ID, ""); err != nil {
		return err
	}
	if len(files) == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "nothing to download")
	}

	res := c.Response()
	res.Header().Set(echo.HeaderContentType, "application/zip")
	res.Header().Set(echo.HeaderContentDisposition,
		fmt.Sprintf(`attachment; filename=%q`, zipNameSanitiser.Replace(root.Title)+".zip"))
	res.WriteHeader(http.StatusOK)

	zw := zip.NewWriter(res.Writer)
	defer zw.Close()
	for _, f := range files {
		// Music archives can transcode per entry (format=mp3/ogg).
		if format != "" && format != "original" && f.track != nil {
			if path, name, err := s.transcodedTrack(ctx, *f.track, format, quality); err == nil {
				f.path = path
				f.name = f.name[:len(f.name)-len(filepath.Ext(f.name))] + filepath.Ext(name)
			}
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Store})
		if err != nil {
			return nil // client went away mid-stream
		}
		src, err := os.Open(f.path)
		if err != nil {
			log.Printf("archive %s: %v", f.path, err)
			continue
		}
		_, err = io.Copy(w, src)
		src.Close()
		if err != nil {
			return nil // client went away mid-stream
		}
	}
	return nil
}

// GET /play/:sid/index.m3u8 | /play/:sid/:file — HLS session assets.
func (s *server) hlsPlaylist(c echo.Context) error {
	session, ok := s.sessions.Get(c.Param("sid"))
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.Blob(http.StatusOK, "application/vnd.apple.mpegurl", []byte(session.Playlist()))
}

func (s *server) hlsFile(c echo.Context) error {
	session, ok := s.sessions.Get(c.Param("sid"))
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	}
	// Session files are transient; a cached partial file poisons the player.
	c.Response().Header().Set("Cache-Control", "no-store")
	if c.Param("file") == "init.mp4" {
		data, err := session.Init(c.Request().Context())
		if err != nil {
			return echo.NewHTTPError(http.StatusGatewayTimeout, err.Error())
		}
		return c.Blob(http.StatusOK, "video/mp4", data)
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
