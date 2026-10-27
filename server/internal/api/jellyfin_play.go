package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/jellyfin"
	"github.com/garybowers/cineva/server/internal/playback"
	"github.com/garybowers/cineva/server/internal/store"
)

// Playback through the Jellyfin-compatible API (docs/jellyfin.md, "The
// playback decision"): the client's device profile becomes Cineva's
// capabilities, decidePlay makes the same call it makes for the web
// client, and the answer is a MediaSource that direct plays or follows
// TranscodingUrl to the same HLS session.

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// jfAPIKey is the token to put on the URLs a client fetches without
// headers (segments, subtitles): the one it sent.
func jfAPIKey(c echo.Context) string {
	_, token := jfToken(c)
	return token
}

// jfSubtitleURL is where a text subtitle is fetched as WebVTT.
func (s *server) jfSubtitleURL(c echo.Context, it store.Item) func(store.SubtitleTrack) string {
	key := jfAPIKey(c)
	return func(st store.SubtitleTrack) string {
		u := "/Videos/" + jellyfin.ID(it.ID) + "/" + jellyfin.ID(it.ID) + "/Subtitles/" + itoa(st.StreamIndex) + "/Stream.vtt"
		if key != "" {
			u += "?api_key=" + url.QueryEscape(key)
		}
		return u
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// jfPlayable is a film or episode the caller may play (music playback is a
// later stage and answers 404).
func (s *server) jfPlayable(c echo.Context, raw string) (store.Item, bool) {
	it := s.jfLoad(c, raw)
	if it == nil || (it.Kind != store.KindMovie && it.Kind != store.KindEpisode) {
		return store.Item{}, false
	}
	return *it, true
}

// POST /Items/{id}/PlaybackInfo — the decision.
func (s *server) jfPlaybackInfo(c echo.Context) error {
	ctx := c.Request().Context()
	it, ok := s.jfPlayable(c, c.Param("id"))
	if !ok {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	if err := s.storageReady(ctx, it); err != nil {
		return err
	}
	var req jellyfin.PlaybackInfoRequest
	if c.Request().Method == http.MethodPost {
		if err := c.Bind(&req); err != nil {
			return jfProblem(c, http.StatusBadRequest, "Bad Request")
		}
	}
	if v := c.QueryParam("MaxStreamingBitrate"); v != "" && req.MaxStreamingBitrate == 0 {
		req.MaxStreamingBitrate = jellyfin.Atoi(v, 0)
	}
	if v := c.QueryParam("AudioStreamIndex"); v != "" && req.AudioStreamIndex == nil {
		req.AudioStreamIndex = jellyfin.IntPtr(jellyfin.Atoi(v, 0))
	}
	if v := c.QueryParam("SubtitleStreamIndex"); v != "" && req.SubtitleStreamIndex == nil {
		req.SubtitleStreamIndex = jellyfin.IntPtr(jellyfin.Atoi(v, -1))
	}
	playable := jellyfin.Read(req.DeviceProfile)
	if req.MaxStreamingBitrate > 0 {
		playable.MaxBitrate = req.MaxStreamingBitrate
	}
	caps := capabilities{Containers: playable.Containers, VideoCodecs: playable.VideoCodecs, AudioCodecs: playable.AudioCodecs}
	fileBitrate := 0
	if it.RuntimeSecs > 0 {
		fileBitrate = int(it.SizeBytes * 8 / int64(it.RuntimeSecs))
	}
	caps.Quality = jellyfin.QualityFor(playable.MaxBitrate, fileBitrate)
	if req.EnableDirectPlay != nil && !*req.EnableDirectPlay && req.EnableDirectStream != nil && !*req.EnableDirectStream && caps.Quality == "" {
		caps.Quality = autoVideoQuality(it).Label
	}
	caps.AudioTrack = req.AudioStreamIndex
	// A subtitle the client chose: a text one it overlays itself, an image
	// one Cineva burns in; -1 is "none", which stops the preference too.
	audio, _ := s.store.AudioTracksForItem(ctx, it.ID)
	subs, _ := s.store.SubtitlesForItem(ctx, it.ID)
	if req.SubtitleStreamIndex != nil {
		caps.BurnSubtitle = -1
		for _, st := range subs {
			if st.StreamIndex == *req.SubtitleStreamIndex && !st.TextCodec() {
				caps.BurnSubtitle = st.StreamIndex
			}
		}
	}
	info, err := s.decidePlay(c, it, caps)
	if err != nil {
		return err
	}
	streams := jellyfin.Streams(it, audio, subs, s.jfSubtitleURL(c, it))
	src := jellyfin.Source(it, streams, "")
	src.DefaultAudioStreamIndex = jellyfin.IntPtr(info.AudioTrack)
	for _, st := range subs {
		if st.ID == info.Subtitle {
			src.DefaultSubtitleStreamIndex = jellyfin.IntPtr(st.StreamIndex)
		}
	}
	key := jfAPIKey(c)
	switch info.Mode {
	case "direct":
		src.SupportsDirectPlay, src.SupportsDirectStream = true, true
	default:
		src.SupportsDirectPlay, src.SupportsDirectStream = false, false
		q := url.Values{}
		q.Set("PlaySessionId", info.SessionID)
		q.Set("MediaSourceId", jellyfin.ID(it.ID))
		if key != "" {
			q.Set("api_key", key)
		}
		src.TranscodingURL = "/Videos/" + jellyfin.ID(it.ID) + "/master.m3u8?" + q.Encode()
		src.TranscodingSubProtocol = "hls"
		src.TranscodingContainer = "mp4"
	}
	return c.JSON(http.StatusOK, jellyfin.PlaybackInfoResponse{
		MediaSources: []jellyfin.MediaSource{src}, PlaySessionID: info.SessionID,
	})
}

// GET /Videos/{id}/stream — direct play with ranges, through the storage
// deadline as the web client's is.
func (s *server) jfVideoStream(c echo.Context) error {
	it, ok := s.jfPlayable(c, c.Param("id"))
	if !ok {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	if _, has := s.playbacks.Get(userID(c), it.ID); !has {
		s.registerPlayback(c, it, "direct", "original", "")
	}
	return s.serveMedia(c, it, it.Path, "")
}

// GET /Items/{id}/Download — the file as an attachment.
func (s *server) jfDownload(c echo.Context) error {
	it := s.jfLoad(c, c.Param("id"))
	if it == nil || it.Kind == store.KindArtist || it.Kind == store.KindSeries || it.Kind == store.KindSeason {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	return s.serveMedia(c, *it, it.Path, filepath.Base(it.Path))
}

// GET /Videos/{id}/master.m3u8?PlaySessionId= — the HLS session's
// playlist. Cineva's playlist names its segments relatively, which would
// drop the token from the URL, so each is written out in full with it.
func (s *server) jfMaster(c echo.Context) error {
	sid := c.QueryParam("PlaySessionId")
	if sid == "" {
		sid = c.QueryParam("playSessionId")
	}
	if s.sessions == nil {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	session, ok := s.sessions.Get(sid)
	if !ok {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	if err := session.Err(); err != nil {
		return s.sessionFailed(c.Request().Context(), session, err)
	}
	base := "/Videos/" + c.Param("id") + "/hls/" + sid + "/"
	suffix := ""
	if key := jfAPIKey(c); key != "" {
		suffix = "?api_key=" + url.QueryEscape(key)
	}
	var b strings.Builder
	for _, line := range strings.Split(session.Playlist(), "\n") {
		switch {
		case strings.HasPrefix(line, "#EXT-X-MAP:URI=\""):
			b.WriteString("#EXT-X-MAP:URI=\"" + base + "init.mp4" + suffix + "\"\n")
		case line != "" && !strings.HasPrefix(line, "#"):
			b.WriteString(base + line + suffix + "\n")
		default:
			b.WriteString(line + "\n")
		}
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.Blob(http.StatusOK, "application/vnd.apple.mpegurl", []byte(strings.TrimRight(b.String(), "\n")+"\n"))
}

// GET /Videos/{id}/hls/{sid}/{file} — a segment or the init, as the web
// client fetches them. The session id is the credential: unguessable and
// short-lived.
func (s *server) jfHLSFile(c echo.Context) error {
	if s.sessions == nil {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	return s.hlsFile(c)
}

// GET /Videos/{id}/{source}/Subtitles/{index}/Stream.vtt — a text
// subtitle as WebVTT, by stream index.
func (s *server) jfSubtitle(c echo.Context) error {
	ctx := c.Request().Context()
	it, err := s.store.Item(ctx, jellyfin.ParseID(c.Param("id")))
	if err != nil {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	if u := currentUser(c); u.ID == "" || !canSeeLibrary(u, it.LibraryID) {
		// Subtitles are fetched by players without headers; the token
		// rides on the URL and was resolved by the middleware when sent.
		if u.ID == "" {
			return c.NoContent(http.StatusUnauthorized)
		}
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	subs, err := s.store.SubtitlesForItem(ctx, it.ID)
	if err != nil {
		return err
	}
	want := jellyfin.Atoi(c.Param("index"), -1)
	for _, st := range subs {
		if st.StreamIndex == want && st.TextCodec() {
			c.SetParamNames("file")
			c.SetParamValues(st.ID + ".vtt")
			return s.subtitleVTT(c)
		}
	}
	return jfProblem(c, http.StatusNotFound, "Not Found")
}

// ---- Reporting ----

// jfReport reads what a client says about a playback, from the body or
// (older clients) the query.
func jfReport(c echo.Context) (jellyfin.PlaybackProgress, bool) {
	var r jellyfin.PlaybackProgress
	if c.Request().ContentLength != 0 {
		if err := c.Bind(&r); err != nil {
			return r, false
		}
	}
	if r.ItemID == "" {
		r.ItemID = c.QueryParam("itemId")
	}
	if r.ItemID == "" {
		r.ItemID = c.QueryParam("ItemId")
	}
	if r.PositionTicks == nil {
		if v := c.QueryParam("positionTicks"); v != "" {
			t := int64(jellyfin.Atoi(v, 0))
			r.PositionTicks = &t
		}
	}
	if v := c.QueryParam("isPaused"); v != "" {
		r.IsPaused = strings.EqualFold(v, "true")
	}
	return r, r.ItemID != ""
}

// POST /Sessions/Playing — playback started. PlaybackInfo already opened
// the history and registered it; a client that skipped PlaybackInfo (direct
// play of a known file) is registered here.
func (s *server) jfPlaying(c echo.Context) error {
	r, ok := jfReport(c)
	if !ok {
		return c.NoContent(http.StatusNoContent)
	}
	it := s.jfLoad(c, r.ItemID)
	if it == nil || it.Kind == store.KindTrack {
		return c.NoContent(http.StatusNoContent)
	}
	if _, has := s.playbacks.Get(userID(c), it.ID); !has {
		s.openPlayHistory(c, *it)
		mode := "direct"
		if strings.EqualFold(r.PlayMethod, "Transcode") {
			mode = "hls"
		}
		s.registerPlayback(c, *it, mode, "", r.PlaySessionID)
	}
	return s.jfRecordProgress(c, *it, r)
}

// POST /Sessions/Playing/Progress and /Ping — the heartbeat.
func (s *server) jfProgress(c echo.Context) error {
	r, ok := jfReport(c)
	if !ok {
		return c.NoContent(http.StatusNoContent)
	}
	it := s.jfLoad(c, r.ItemID)
	if it == nil || it.Kind == store.KindTrack {
		return c.NoContent(http.StatusNoContent)
	}
	return s.jfRecordProgress(c, *it, r)
}

func (s *server) jfPlayingPing(c echo.Context) error {
	return c.NoContent(http.StatusNoContent)
}

// jfRecordProgress is the heartbeat as postProgress keeps it: the registry
// touched (or the playback re-registered), the position saved; a playback
// an administrator stopped answers 409 so the client stops.
func (s *server) jfRecordProgress(c echo.Context, it store.Item, r jellyfin.PlaybackProgress) error {
	ctx := c.Request().Context()
	pos := 0
	if r.PositionTicks != nil {
		pos = jellyfin.Secs(*r.PositionTicks)
	}
	if s.playbacks.Stopped(userID(c), it.ID) {
		if _, err := s.store.SetProgress(ctx, userID(c), it.ID, pos, it.RuntimeSecs); err != nil {
			playbackLog.Error("saving progress on admin stop failed", "item", it.ID, "err", err)
		}
		return jfProblem(c, http.StatusConflict, "playback was stopped by an administrator")
	}
	device, _ := c.Get(ctxSessionID).(string)
	if r.IsPaused && s.playbacks.HeldElsewhere(userID(c), it.ID, device) {
		return c.NoContent(http.StatusNoContent)
	}
	if !s.playbacks.Touch(userID(c), it.ID, pos, r.IsPaused) {
		s.registerPlayback(c, it, "direct", "", "")
		s.playbacks.Touch(userID(c), it.ID, pos, r.IsPaused)
	}
	if !r.IsPaused && s.playbacks.Claim(userID(c), it.ID, device) {
		s.events.Publish(events.Event{Type: "devices", User: userID(c)})
	}
	if r.PositionTicks != nil {
		if _, err := s.store.SetProgress(ctx, userID(c), it.ID, pos, it.RuntimeSecs); err != nil {
			return err
		}
	}
	return c.NoContent(http.StatusNoContent)
}

// POST /Sessions/Playing/Stopped — playback ended: the position kept, the
// registry entry and any transcode dropped (which closes the history row
// and counts the play, as the web client's stop does).
func (s *server) jfStopped(c echo.Context) error {
	r, ok := jfReport(c)
	if !ok {
		return c.NoContent(http.StatusNoContent)
	}
	it := s.jfLoad(c, r.ItemID)
	if it == nil || it.Kind == store.KindTrack {
		return c.NoContent(http.StatusNoContent)
	}
	ctx := c.Request().Context()
	if r.PositionTicks != nil {
		if _, err := s.store.SetProgress(ctx, userID(c), it.ID, jellyfin.Secs(*r.PositionTicks), it.RuntimeSecs); err != nil {
			return err
		}
	}
	device, _ := c.Get(ctxSessionID).(string)
	if s.playbacks.HeldElsewhere(userID(c), it.ID, device) {
		return c.NoContent(http.StatusNoContent)
	}
	if sid, ok := s.playbacks.Stop(userID(c), it.ID); ok && sid != "" {
		s.sessions.Stop(sid)
	}
	s.events.Publish(events.Event{Type: "items", Data: map[string]string{"libraryId": it.LibraryID}})
	return c.NoContent(http.StatusNoContent)
}

// ---- User data ----

func (s *server) jfUserDataResponse(c echo.Context, it store.Item) error {
	items := s.jfDecorate(c, []store.Item{it})
	return c.JSON(http.StatusOK, jellyfin.UserDataFor(items[0]))
}

// POST /Users/{id}/PlayedItems/{id}
func (s *server) jfMarkPlayed(c echo.Context) error {
	return s.jfSetWatched(c, jfItemParam(c), true)
}

// DELETE /Users/{id}/PlayedItems/{id}
func (s *server) jfMarkUnplayed(c echo.Context) error {
	return s.jfSetWatched(c, jfItemParam(c), false)
}

func (s *server) jfSetWatched(c echo.Context, raw string, watched bool) error {
	ctx := c.Request().Context()
	it := s.jfLoad(c, raw)
	if it == nil {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	// A series or season marks every episode under it.
	var targets []store.Item
	switch it.Kind {
	case store.KindMovie, store.KindEpisode, store.KindTrack:
		targets = []store.Item{*it}
	case store.KindSeries:
		seasons, _ := s.store.ItemsByParent(ctx, it.ID)
		for _, se := range seasons {
			eps, _ := s.store.ItemsByParent(ctx, se.ID)
			targets = append(targets, eps...)
		}
	case store.KindSeason:
		targets, _ = s.store.ItemsByParent(ctx, it.ID)
	}
	for _, t := range targets {
		if _, err := s.store.SetWatched(ctx, userID(c), t.ID, watched); err != nil {
			return err
		}
	}
	s.events.Publish(events.Event{Type: "items", Data: map[string]string{"libraryId": it.LibraryID}})
	return s.jfUserDataResponse(c, *it)
}

// POST /Users/{id}/FavoriteItems/{id} — a favourite is a five-star rating
// until Cineva has favourites of its own.
func (s *server) jfFavourite(c echo.Context) error {
	return s.jfSetFavourite(c, jfItemParam(c), true)
}

func (s *server) jfUnfavourite(c echo.Context) error {
	return s.jfSetFavourite(c, jfItemParam(c), false)
}

func (s *server) jfSetFavourite(c echo.Context, raw string, on bool) error {
	it := s.jfLoad(c, raw)
	if it == nil {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	stars := 0
	if on {
		stars = 5
	}
	if err := s.store.SetRating(c.Request().Context(), userID(c), it.ID, stars); err != nil {
		return err
	}
	s.events.Publish(events.Event{Type: "rating", User: userID(c), Data: map[string]any{"itemId": it.ID, "stars": stars}})
	return s.jfUserDataResponse(c, *it)
}

// GET /UserItems/{id}/UserData
func (s *server) jfGetUserData(c echo.Context) error {
	it := s.jfLoad(c, c.Param("id"))
	if it == nil {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	return s.jfUserDataResponse(c, *it)
}

// POST /UserItems/{id}/UserData — played, position and favourite in one.
func (s *server) jfSetUserData(c echo.Context) error {
	ctx := c.Request().Context()
	it := s.jfLoad(c, c.Param("id"))
	if it == nil {
		return jfProblem(c, http.StatusNotFound, "Not Found")
	}
	var req struct {
		Played                *bool  `json:"Played"`
		PlaybackPositionTicks *int64 `json:"PlaybackPositionTicks"`
		IsFavorite            *bool  `json:"IsFavorite"`
	}
	if err := c.Bind(&req); err != nil {
		return jfProblem(c, http.StatusBadRequest, "Bad Request")
	}
	if req.PlaybackPositionTicks != nil && (it.Kind == store.KindMovie || it.Kind == store.KindEpisode) {
		if _, err := s.store.SetProgress(ctx, userID(c), it.ID, jellyfin.Secs(*req.PlaybackPositionTicks), it.RuntimeSecs); err != nil {
			return err
		}
	}
	if req.Played != nil {
		if err := s.jfSetWatched(c, c.Param("id"), *req.Played); err != nil {
			return err
		}
		if req.IsFavorite == nil {
			return nil
		}
	}
	if req.IsFavorite != nil {
		return s.jfSetFavourite(c, c.Param("id"), *req.IsFavorite)
	}
	return s.jfUserDataResponse(c, *it)
}

// ---- Sessions ----

// jfSessionInfo describes one of the user's devices, with what it plays.
func (s *server) jfSessionInfo(c echo.Context, u store.User, sess store.Session, a jellyfin.Auth) jellyfin.SessionInfo {
	info := jellyfin.SessionInfo{
		PlayState:       jellyfin.PlayState{RepeatMode: "RepeatNone", PlaybackOrder: "Default"},
		AdditionalUsers: []any{}, Capabilities: jellyfin.Capabilities{PlayableMediaTypes: []string{}, SupportedCommands: []string{}},
		RemoteEndPoint: sess.ClientIP, PlayableMediaTypes: []string{"Video", "Audio"},
		ID: jellyfin.ID(sess.ID), UserID: jellyfin.ID(u.ID), UserName: u.DisplayName, Client: a.Client,
		LastActivityDate: jfISO(sess.LastSeenAt), LastPlaybackCheckIn: jfISO(sess.LastSeenAt),
		DeviceName: sess.Name, DeviceID: a.DeviceID, ApplicationVersion: a.Version, IsActive: true,
		NowPlayingQueue: []any{}, NowPlayingQueueFullItems: []any{}, HasCustomDeviceName: sess.Name != "",
		ServerID: s.jfServerID(c.Request().Context()), SupportedCommands: []string{},
	}
	if info.Client == "" {
		info.Client = sess.Client
	}
	if info.DeviceName == "" {
		info.DeviceName = a.Device
	}
	if p, ok := s.playbacks.OnDevice(u.ID, sess.ID); ok {
		if it, err := s.store.Item(c.Request().Context(), p.ItemID); err == nil {
			k := s.jfCache(c)
			b := s.jfMapper(c).Item(it, k.context(it))
			info.NowPlayingItem = &b
		}
		info.PlayState.PositionTicks = jellyfin.Ticks(p.PositionSecs)
		info.PlayState.IsPaused = p.Paused
		info.PlayState.CanSeek = true
		info.PlayState.PlayMethod = jfPlayMethod(p)
	}
	return info
}

func jfPlayMethod(p playback.Playback) string {
	if p.Mode == "hls" || p.Mode == "transcode" {
		return "Transcode"
	}
	return "DirectPlay"
}

func jfISO(t string) string {
	if len(t) == 19 && t[10] == ' ' {
		return t[:10] + "T" + t[11:] + ".0000000Z"
	}
	return t
}

// GET /Sessions — the user's own devices and what each plays.
func (s *server) jfSessions(c echo.Context) error {
	me := currentUser(c)
	sessions, err := s.store.UserSessions(c.Request().Context(), me.ID)
	if err != nil {
		return err
	}
	a, _ := c.Get(ctxJFAuth).(jellyfin.Auth)
	out := []jellyfin.SessionInfo{}
	for _, sess := range sessions {
		info := s.jfSessionInfo(c, me, sess, jellyfin.Auth{})
		if sid, _ := c.Get(ctxSessionID).(string); sid == sess.ID {
			info.Client, info.DeviceID, info.ApplicationVersion = a.Client, a.DeviceID, a.Version
			if info.DeviceName == "" {
				info.DeviceName = a.Device
			}
		}
		out = append(out, info)
	}
	return c.JSON(http.StatusOK, out)
}

// ---- The socket ----

// jfKeepAlive is how often a client must say it is still there, in
// seconds; Jellyfin's clients send a KeepAlive at half the interval.
const jfKeepAlive = 60

// GET /socket — the websocket the apps open on sign-in. It answers
// KeepAlive and asks for them; remote control messages come in a later
// stage.
func (s *server) jfSocket(c echo.Context) error {
	if currentUser(c).ID == "" {
		return c.NoContent(http.StatusUnauthorized)
	}
	conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return nil
	}
	ctx := c.Request().Context()
	defer conn.Close(websocket.StatusNormalClosure, "")
	write := func(m jellyfin.Message) error {
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, data)
	}
	if err := write(jellyfin.Message{MessageType: "ForceKeepAlive", Data: jfKeepAlive}); err != nil {
		return nil
	}
	for {
		rctx, cancel := context.WithTimeout(ctx, 2*jfKeepAlive*time.Second)
		_, data, err := conn.Read(rctx)
		cancel()
		if err != nil {
			return nil
		}
		var m jellyfin.Message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if strings.EqualFold(m.MessageType, "KeepAlive") {
			if err := write(jellyfin.Message{MessageType: "KeepAlive"}); err != nil {
				return nil
			}
		}
	}
}
