package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/lang"
	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

// subtitleInfo is the client-facing view of a track.
type subtitleInfo struct {
	ID       string `json:"id"`
	Language string `json:"language,omitempty"`
	// LanguageName is Language in English, for a client to show.
	LanguageName string `json:"languageName,omitempty"`
	Title        string `json:"title,omitempty"`
	Codec        string `json:"codec"`
	Forced       bool   `json:"forced,omitempty"`
	// URL is set only for text tracks (extractable to WebVTT).
	URL string `json:"url,omitempty"`
	// BurnIn marks a track that can only be shown by painting it into the
	// picture: PGS and VobSub are bitmaps with no text to hand a player.
	// Selecting one restarts playback as a re-encode, since nothing can be
	// drawn on a copied stream — so the client should say so rather than
	// have the picture quietly change.
	BurnIn bool `json:"burnIn,omitempty"`
	// StreamIndex identifies the track to the transcoder when burning in.
	StreamIndex int `json:"streamIndex,omitempty"`
}

func subtitleInfos(tracks []store.SubtitleTrack) []subtitleInfo {
	out := make([]subtitleInfo, 0, len(tracks))
	for _, t := range tracks {
		info := subtitleInfo{
			ID: t.ID, Language: t.Language, LanguageName: lang.NameOf(t.Language), Title: t.Title,
			Codec: t.Codec, Forced: t.Forced,
		}
		if t.TextCodec() {
			info.URL = "/api/v1/subtitles/" + t.ID + ".vtt"
		} else {
			info.BurnIn = true
			info.StreamIndex = t.StreamIndex
		}
		out = append(out, info)
	}
	return out
}

// GET /items/:id/subtitles
func (s *server) itemSubtitles(c echo.Context) error {
	tracks, err := s.store.SubtitlesForItem(c.Request().Context(), c.Param("id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, subtitleInfos(tracks))
}

// GET /subtitles/:file ("<id>.vtt") — extract (and cache) one text track as WebVTT.
func (s *server) subtitleVTT(c echo.Context) error {
	id := strings.TrimSuffix(c.Param("file"), ".vtt")
	track, err := s.store.Subtitle(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "subtitle not found")
	}
	if !track.TextCodec() {
		return echo.NewHTTPError(http.StatusUnprocessableEntity,
			"image-based subtitles are shown by burning them in: request playback with burnSubtitle set to this track's streamIndex")
	}

	cached := filepath.Join(s.subtitleDir, track.ID+".vtt")
	if data, err := os.ReadFile(cached); err == nil {
		return c.Blob(http.StatusOK, "text/vtt; charset=utf-8", data)
	}

	// Both kinds are read from media storage: refused at once when it is
	// known to be down, and given up on when it stops answering.
	ctx := c.Request().Context()
	item, err := s.store.Item(ctx, track.ItemID)
	if err != nil || !canSeeLibrary(currentUser(c), item.LibraryID) {
		return echo.NewHTTPError(http.StatusNotFound, "subtitle not found")
	}
	if err := s.storageReady(ctx, item); err != nil {
		return err
	}
	var vtt string
	if track.Source == "sidecar" {
		vtt, err = library.WithinFile(track.Path, mediaTimeout, func() (string, error) {
			return media.ConvertSidecarVTT(track.Path)
		})
	} else {
		// The whole film is read to find the track, which can take a while
		// over a share; bounded by progress, not by total time.
		vtt, err = library.WithinIdleFile(item.Path, mediaTimeout, func(progress func()) (string, error) {
			return media.ExtractVTT(item.Path, track.StreamIndex, track.Codec, progress)
		})
	}
	if errors.Is(err, library.ErrNotResponding) {
		return s.storageFailed(ctx, item, err)
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	if err := os.MkdirAll(s.subtitleDir, 0o755); err == nil {
		_ = os.WriteFile(cached, []byte(vtt), 0o644)
	}
	return c.Blob(http.StatusOK, "text/vtt; charset=utf-8", []byte(vtt))
}
