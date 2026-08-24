package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

// subtitleInfo is the client-facing view of a track.
type subtitleInfo struct {
	ID       string `json:"id"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Codec    string `json:"codec"`
	Forced   bool   `json:"forced,omitempty"`
	// URL is set only for text tracks (extractable to WebVTT). Image-based
	// tracks (PGS/VobSub) are listed without a URL until burn-in lands.
	URL string `json:"url,omitempty"`
}

func subtitleInfos(tracks []store.SubtitleTrack) []subtitleInfo {
	out := make([]subtitleInfo, 0, len(tracks))
	for _, t := range tracks {
		info := subtitleInfo{
			ID: t.ID, Language: t.Language, Title: t.Title,
			Codec: t.Codec, Forced: t.Forced,
		}
		if t.TextCodec() {
			info.URL = "/api/v1/subtitles/" + t.ID + ".vtt"
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
			"image-based subtitles need burn-in, which is not available yet")
	}

	cached := filepath.Join(s.subtitleDir, track.ID+".vtt")
	if data, err := os.ReadFile(cached); err == nil {
		return c.Blob(http.StatusOK, "text/vtt; charset=utf-8", data)
	}

	var vtt string
	if track.Source == "sidecar" {
		vtt, err = media.ConvertSidecarVTT(track.Path)
	} else {
		item, itemErr := s.store.Item(c.Request().Context(), track.ItemID)
		if itemErr != nil {
			return itemErr
		}
		vtt, err = media.ExtractVTT(item.Path, track.StreamIndex, track.Codec)
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	if err := os.MkdirAll(s.subtitleDir, 0o755); err == nil {
		_ = os.WriteFile(cached, []byte(vtt), 0o644)
	}
	return c.Blob(http.StatusOK, "text/vtt; charset=utf-8", []byte(vtt))
}
