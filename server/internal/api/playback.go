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
	"github.com/garybowers/cineva/server/internal/playback"
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
	// AudioTrack picks an audio stream by index; nil uses the file's
	// default. Changing it restarts the session, like a quality change.
	AudioTrack *int `json:"audioTrack"`
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

// videoCodecSupported reports whether the device can decode the item's video
// stream. It is asked separately from directPlayReason because the two lead
// to different transcodes: a container or audio mismatch only needs a remux,
// but a video codec the device cannot decode has to be re-encoded.
func videoCodecSupported(item store.Item, caps capabilities) bool {
	return item.VideoCodec == "" || contains(caps.VideoCodecs, item.VideoCodec)
}

// remuxSafe reports whether a video stream can be copied into fMP4 and still
// play on a device that says it can decode it.
//
// Copying is always the better answer when it works: the video reaches the
// client bit-for-bit, at full resolution, for almost no CPU. Re-encoding
// costs quality and a great deal of CPU, so it is the fallback, not the
// default.
//
// HEVC needs the hvc1 sample-entry tag to be copied into fMP4 (the muxer
// otherwise warns "Stream HEVC is not hvc1" and browsers reject it); RunHLS
// sets it. Dolby Vision is the remaining question, and it depends on the
// profile rather than on Dolby Vision being present at all: profile 8 with
// base-layer compatibility 1 is plain HDR10 underneath and decodes correctly
// anywhere, whereas profile 5 carries an IPT base layer that renders as
// false colour without a Dolby Vision display pipeline. Only the latter has
// to be re-encoded.
func remuxSafe(item store.Item, info media.Info) bool {
	switch item.VideoCodec {
	case "", "h264":
		return true
	case "hevc":
		// Profile 5 signals its base layer is not independently viewable
		// (compatibility id 0): the pixels are IPT, which renders as false
		// colour anywhere without a Dolby Vision pipeline. Profile 8 with
		// compatibility 1 is HDR10 underneath and copies fine.
		return info.DVProfile == 0 || info.DVBLCompat != 0
	}
	return false
}

// defaultAudioTrack is the stream the file marks default, else the first.
// -1 when there is no audio at all.
func defaultAudioTrack(tracks []store.AudioTrack) int {
	if len(tracks) == 0 {
		return -1
	}
	for _, t := range tracks {
		if t.Default {
			return t.StreamIndex
		}
	}
	return tracks[0].StreamIndex
}

// autoVideoQuality picks an encode rung for a source whose video codec the
// device cannot decode. Capped at 1080p: software-encoding 2160p cannot keep
// ahead of a playhead, and the ladder tops out there anyway.
func autoVideoQuality(item store.Item) media.Quality {
	switch {
	case item.Height == 0 || item.Height >= 1080:
		return qualityLevels["1080p"]
	case item.Height >= 720:
		return qualityLevels["720p"]
	default:
		return qualityLevels["480p"]
	}
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
	PositionSecs int    `json:"positionSecs"` // resume point
	Reason       string `json:"reason,omitempty"`
	// Chapters are the file's own marks, for the player's chapter list and
	// skip. Read from the probe already done here, so they cost nothing.
	Chapters  []media.Chapter `json:"chapters,omitempty"` // why transcoding was chosen
	Subtitles []subtitleInfo  `json:"subtitles"`
	// Audio lists the file's audio streams so the player can offer a
	// picker; AudioTrack is the stream index actually being played.
	Audio      []store.AudioTrack `json:"audio"`
	AudioTrack int                `json:"audioTrack"`
	// What the viewer is actually being sent, for the player's info panel.
	Source Source `json:"source"`
}

// Source describes the stream behind a playback, so the player can show what
// is really happening rather than leaving the viewer to guess why a 4K title
// looks soft.
type Source struct {
	Container  string `json:"container,omitempty"`
	VideoCodec string `json:"videoCodec,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	HDRFormat  string `json:"hdrFormat,omitempty"`
	DVProfile  int    `json:"dvProfile,omitempty"`
	// Video is "direct", "remux" (copied untouched) or "transcode".
	Video string `json:"video"`
	// OutHeight is the height being sent; 0 when the video is untouched.
	OutHeight int `json:"outHeight,omitempty"`
	// Audio is "direct" or "transcode", with the codec being sent.
	Audio      string `json:"audio"`
	AudioCodec string `json:"audioCodec,omitempty"`
	// Accel names the transcode path actually built — the device, or "none"
	// for software. The session reports it after the pipeline is up (a
	// device that opens but cannot allocate frames falls back), so it is
	// empty at the moment play is answered and the client reads the live
	// value from the session.
	Accel string `json:"accel,omitempty"`
	// HDRToneMapped is true when an HDR source is being mapped to SDR for
	// this stream — worth saying, since it is why the picture differs from
	// the file.
	HDRToneMapped bool `json:"hdrToneMapped,omitempty"`
}

func (s *server) playableItem(c echo.Context) (store.Item, error) {
	item, err := s.store.Item(c.Request().Context(), c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), item.LibraryID) {
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
	if err := s.store.RecordPlay(c.Request().Context(), userID(c), item.ID, item.Kind); err != nil {
		log.Printf("record play: %v", err)
	} else {
		s.events.Publish(events.Event{Type: "plays"})
	}
	s.audit(c, "play.start", string(item.Kind), item.ID, item.Title, "")
	// Music always direct plays: browsers decode flac/mp3/aac/ogg/opus
	// natively, and the files are small. (Transcode fallback: BACKLOG.md.)
	if item.Kind == store.KindTrack {
		s.registerPlayback(c, item, "direct", "original", "")
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
	ps, err := s.store.PlayStateFor(c.Request().Context(), userID(c), item.ID)
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

	// The remux decision needs facts the items table does not carry (Dolby
	// Vision profile, HDR transfer), so the file is probed once here. It is
	// a header read, and only on starting playback. A failure is not fatal:
	// the zero Info reads as "no Dolby Vision", which is the common case.
	vinfo, err := media.Probe(item.Path)
	if err != nil {
		log.Printf("play %s: probe: %v", item.Title, err)
	}

	audio, err := s.store.AudioTracksForItem(c.Request().Context(), item.ID)
	if err != nil {
		return err
	}
	// The requested track, defaulting to the file's own default.
	chosen := defaultAudioTrack(audio)
	if body.AudioTrack != nil {
		for _, a := range audio {
			if a.StreamIndex == *body.AudioTrack {
				chosen = a.StreamIndex
			}
		}
	}
	src := Source{
		Container: item.Container, VideoCodec: item.VideoCodec,
		Width: item.Width, Height: item.Height,
		HDRFormat: vinfo.HDRFormat, DVProfile: vinfo.DVProfile,
	}

	direct := playInfo{
		Mode: "direct", URL: "/api/v1/items/" + item.ID + "/stream",
		DurationSecs: item.RuntimeSecs, PositionSecs: resume,
		Subtitles: subs, Audio: audio, AudioTrack: chosen,
		Chapters: vinfo.Chapters,
		Source: func() Source {
			d := src
			d.Video, d.Audio, d.AudioCodec = "direct", "direct", item.AudioCodec
			return d
		}(),
	}

	var sessionQuality media.Quality // zero value = remux (video copy)
	var reason string
	switch {
	case quality == "original":
		// "Original" means keep the video exactly as it is — not "hand the
		// browser a file it cannot open". Where only the container or the
		// audio codec is the obstacle, a remux still delivers the original
		// video stream bit-for-bit, so fall through to one instead of
		// returning a direct-play URL that cannot play (mkv, most often).
		if directPlayReason(item, caps) == "" {
			return c.JSON(http.StatusOK, direct)
		}
		if remuxSafe(item, vinfo) {
			sessionQuality = media.Quality{Label: "original"} // zero height = copy the video
			reason = "original video, remuxed for the browser"
			break
		}
		// The video cannot survive a copy into fMP4, so bit-for-bit is not
		// on offer. Re-encode rather than fail: the viewer asked for the
		// best available, and the reason says why it is not a copy. Note
		// autoVideoQuality caps at 1080p, so a 4K source is downscaled.
		sessionQuality = autoVideoQuality(item)
		reason = item.VideoCodec + " cannot be remuxed to fMP4 — re-encoding"
	case quality == "" || quality == "auto":
		if reason = directPlayReason(item, caps); reason == "" {
			return c.JSON(http.StatusOK, direct)
		}
		// A container or audio-only mismatch is fixed by a remux, which
		// copies the video stream untouched and costs almost nothing.
		sessionQuality = media.Quality{Label: "auto"}
		switch {
		case !videoCodecSupported(item, caps):
			// Copying a codec the device already said it cannot decode just
			// hands it the same undecodable stream — the player loads
			// segments and then sits there. Re-encode instead.
			sessionQuality = autoVideoQuality(item)
			reason = "video codec " + item.VideoCodec + " not supported by device"
		case !remuxSafe(item, vinfo):
			// The device claims it can decode this, but the stream cannot
			// survive the copy into fMP4. See remuxSafe.
			sessionQuality = autoVideoQuality(item)
			reason = item.VideoCodec + " cannot be remuxed to fMP4 — re-encoding"
		}
	default:
		level, ok := qualityLevels[quality]
		if !ok {
			return echo.NewHTTPError(http.StatusBadRequest, "unknown quality "+quality)
		}
		sessionQuality = level
		reason = "quality " + quality + " requested"
	}

	session, err := s.sessions.Start(media.StartOpts{
		ItemID: item.ID, Title: item.Title, InputPath: item.Path,
		RuntimeSecs: item.RuntimeSecs, StartSecs: resume,
		Quality: sessionQuality, AudioTrack: chosen,
		// Only consulted when the video is re-encoded; a remux keeps the
		// HDR stream untouched.
		HDR: vinfo.HDRFormat,
	})
	if err != nil {
		return err
	}
	s.registerPlayback(c, item, "hls", session.Quality.Label, session.ID)
	src.Video, src.OutHeight = "transcode", sessionQuality.Height
	if sessionQuality.Height == 0 {
		src.Video, src.OutHeight = "remux", 0 // video copied bit-for-bit
	}
	src.Audio, src.AudioCodec = "transcode", "aac"
	src.Accel = session.Accel
	// Only the re-encode path tone maps; a copied stream keeps its HDR.
	src.HDRToneMapped = sessionQuality.Height > 0 && vinfo.HDRFormat != ""
	return c.JSON(http.StatusOK, playInfo{
		Mode: "hls", URL: "/api/v1/play/" + session.ID + "/index.m3u8",
		SessionID: session.ID, DurationSecs: item.RuntimeSecs, PositionSecs: resume,
		Reason: reason, Subtitles: subs, Audio: audio, AudioTrack: chosen,
		Chapters: vinfo.Chapters,
		Source:   src,
	})
}

// GET /items/:id/stream — direct play with HTTP range support.
// GET /items/:id/stream[?format=opus|aac|mp3|ogg&quality=kbps] — the file
// itself with range support, or (music only) a cached transcode for
// bandwidth saving and for codecs the browser cannot decode.
func (s *server) streamItem(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	if format := c.QueryParam("format"); format != "" && format != "original" && item.Kind == store.KindTrack {
		quality, _ := strconv.Atoi(c.QueryParam("quality"))
		path, _, err := s.transcodedTrack(c.Request().Context(), item, format, quality)
		if err != nil {
			return err
		}
		return c.File(path)
	}
	return c.File(item.Path)
}

// GET /items/:id/download — a playable item's original file as an
// attachment; container kinds (season, series, album, artist) stream a
// stored ZIP of every descendant file.
func (s *server) downloadItem(c echo.Context) error {
	ctx := c.Request().Context()
	item, err := s.store.Item(ctx, c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), item.LibraryID) {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	format := c.QueryParam("format")
	quality, _ := strconv.Atoi(c.QueryParam("quality"))
	detail := format
	if quality > 0 {
		detail += " " + strconv.Itoa(quality) + " kbps"
	}
	s.audit(c, "download", string(item.Kind), item.ID, item.Title, strings.TrimSpace(detail))
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

var allowedBitrates = map[int]bool{64: true, 96: true, 128: true, 192: true, 256: true, 320: true}

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
	if sess, ok := s.sessions.Get(c.Param("sid")); ok {
		s.audit(c, "transcode.stop", "session", sess.ID, sess.Title, "stopped from admin")
	}
	s.sessions.Stop(c.Param("sid"))
	s.playbacks.StopSession(c.Param("sid"))
	return c.NoContent(http.StatusNoContent)
}

// registerPlayback records a play on the activity registry with what the
// request reveals about the client.
func (s *server) registerPlayback(c echo.Context, item store.Item, mode, quality, sessionID string) {
	ua := c.Request().UserAgent()
	browser, osName := playback.ParseUserAgent(ua)
	p := playback.Playback{
		UserID: userID(c), ItemID: item.ID, Title: item.Title, Kind: string(item.Kind),
		Mode: mode, Quality: quality, SessionID: sessionID, DurationSecs: item.RuntimeSecs,
		Client: playback.Client{IP: c.RealIP(), Browser: browser, OS: osName, UserAgent: ua},
	}
	// Context line: "Series · S01E02" for episodes, "Artist · Album" for tracks.
	switch item.Kind {
	case store.KindEpisode:
		if season, err := s.store.Item(c.Request().Context(), item.ParentID); err == nil {
			if series, err := s.store.Item(c.Request().Context(), season.ParentID); err == nil {
				p.Subtitle = fmt.Sprintf("%s · S%02dE%02d", series.Title, item.SeasonNum, item.EpisodeNum)
			}
		}
	case store.KindTrack:
		if album, err := s.store.Item(c.Request().Context(), item.ParentID); err == nil {
			p.Subtitle = item.Artist + " · " + album.Title
		}
	}
	s.playbacks.Start(p)
}

// activeSession is an active playback plus its transcode's live numbers.
type activeSession struct {
	playback.Playback
	Transcode *media.Session `json:"transcode,omitempty"`
}

// GET /play/sessions — everything playing right now, direct or transcoded.
func (s *server) listSessions(c echo.Context) error {
	out := []activeSession{}
	for _, p := range s.playbacks.List() {
		a := activeSession{Playback: p}
		if p.SessionID != "" {
			if sess, ok := s.sessions.Get(p.SessionID); ok {
				a.Transcode = sess
			}
		}
		out = append(out, a)
	}
	return c.JSON(http.StatusOK, out)
}

// DELETE /items/:id/playback — the client has stopped; drop the activity
// entry straight away (and any transcode behind it).
func (s *server) stopPlayback(c echo.Context) error {
	if sid, ok := s.playbacks.Stop(userID(c), c.Param("id")); ok && sid != "" {
		s.sessions.Stop(sid)
	}
	return c.NoContent(http.StatusNoContent)
}

// POST /items/:id/progress — update watch state.
// PUT /items/:id/watched — mark an item watched or unwatched by hand.
//
// Separate from /progress because the two mean different things: progress
// reports where the playhead is and lets the threshold decide, while this
// states the outcome directly. Without it nothing can ever lower the flag,
// since SetProgress only raises it.
func (s *server) putWatched(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	var req struct {
		Watched *bool `json:"watched"`
	}
	if err := c.Bind(&req); err != nil || req.Watched == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "watched must be true or false")
	}
	ps, err := s.store.SetWatched(c.Request().Context(), userID(c), item.ID, *req.Watched)
	if err != nil {
		return err
	}
	// Continue-watching rows and any open grid change as a result.
	s.events.Publish(events.Event{Type: "items", Data: map[string]string{"libraryId": item.LibraryID}})
	return c.JSON(http.StatusOK, ps)
}

func (s *server) postProgress(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	var req struct {
		PositionSecs int  `json:"positionSecs"`
		Paused       bool `json:"paused"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	// Heartbeat for the activity registry. A client whose entry was reaped
	// (or that predates a restart) is re-registered so it shows up again.
	if !s.playbacks.Touch(userID(c), item.ID, req.PositionSecs, req.Paused) {
		s.registerPlayback(c, item, "direct", "", "")
		s.playbacks.Touch(userID(c), item.ID, req.PositionSecs, req.Paused)
	}
	// Music positions are not resume state: tracks are short and would
	// otherwise litter Continue Watching.
	if item.Kind == store.KindTrack {
		return c.JSON(http.StatusOK, store.PlayState{ItemID: item.ID, PositionSecs: req.PositionSecs})
	}
	ps, err := s.store.SetProgress(c.Request().Context(), userID(c), item.ID, req.PositionSecs, item.RuntimeSecs)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, ps)
}

// GET /items/:id/next — the server-side next-up decision (see CLAUDE.md).
func (s *server) nextUp(c echo.Context) error {
	if _, err := s.playableItem(c); err != nil {
		return err
	}
	next, ok, err := s.store.NextEpisode(c.Request().Context(), c.Param("id"))
	if err != nil {
		return err
	}
	if !ok {
		return c.NoContent(http.StatusNoContent)
	}
	return c.JSON(http.StatusOK, next)
}

// GET /items/:id/prev — the episode before this one (204 at the start).
func (s *server) prevEpisode(c echo.Context) error {
	if _, err := s.playableItem(c); err != nil {
		return err
	}
	prev, ok, err := s.store.PrevEpisode(c.Request().Context(), c.Param("id"))
	if err != nil {
		return err
	}
	if !ok {
		return c.NoContent(http.StatusNoContent)
	}
	return c.JSON(http.StatusOK, prev)
}

// GET /continue-watching — resume row for the home screen.
func (s *server) continueWatching(c echo.Context) error {
	items, err := s.store.ContinueWatching(c.Request().Context(), userID(c), 20)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, items)
}
