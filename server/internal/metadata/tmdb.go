// Package metadata defines per-domain metadata provider interfaces and their
// implementations. TMDB covers the video domain.
package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// VideoResult is what a video metadata lookup returns.
type VideoResult struct {
	TmdbID    int
	Title     string
	Year      int
	Overview  string
	PosterURL string
}

// EpisodeResult is one episode's metadata within a season.
type EpisodeResult struct {
	Num      int
	Title    string
	Overview string
	StillURL string
}

// SeasonResult is a TV season's metadata including its episode list.
type SeasonResult struct {
	Overview  string
	PosterURL string
	Episodes  []EpisodeResult
}

// VideoProvider is the video-domain metadata interface (see CLAUDE.md).
type VideoProvider interface {
	SearchMovie(ctx context.Context, title string, year int) (VideoResult, error)
	SearchTV(ctx context.Context, name string) (VideoResult, error)
	TVSeason(ctx context.Context, tvID, season int) (SeasonResult, error)
}

var ErrNotFound = fmt.Errorf("no metadata match")

type TMDB struct {
	APIKey string
	Client *http.Client
}

func NewTMDB(apiKey string) *TMDB {
	return &TMDB{APIKey: apiKey, Client: &http.Client{Timeout: 15 * time.Second}}
}

const tmdbImageBase = "https://image.tmdb.org/t/p/w500"

type tmdbSearchResponse struct {
	Results []struct {
		ID           int    `json:"id"`
		Title        string `json:"title"`
		Name         string `json:"name"`
		Overview     string `json:"overview"`
		PosterPath   string `json:"poster_path"`
		ReleaseDate  string `json:"release_date"`
		FirstAirDate string `json:"first_air_date"`
	} `json:"results"`
}

func (t *TMDB) SearchMovie(ctx context.Context, title string, year int) (VideoResult, error) {
	params := url.Values{"query": {title}}
	if year > 0 {
		params.Set("year", strconv.Itoa(year))
	}
	return t.search(ctx, "movie", params)
}

func (t *TMDB) SearchTV(ctx context.Context, name string) (VideoResult, error) {
	return t.search(ctx, "tv", url.Values{"query": {name}})
}

func (t *TMDB) search(ctx context.Context, kind string, params url.Values) (VideoResult, error) {
	params.Set("api_key", t.APIKey)
	u := fmt.Sprintf("https://api.themoviedb.org/3/search/%s?%s", kind, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return VideoResult{}, err
	}
	res, err := t.Client.Do(req)
	if err != nil {
		return VideoResult{}, fmt.Errorf("tmdb search: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return VideoResult{}, fmt.Errorf("tmdb search: status %d", res.StatusCode)
	}
	var body tmdbSearchResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return VideoResult{}, fmt.Errorf("tmdb search: %w", err)
	}
	if len(body.Results) == 0 {
		return VideoResult{}, ErrNotFound
	}
	r := body.Results[0]
	out := VideoResult{TmdbID: r.ID, Title: r.Title, Overview: r.Overview}
	if out.Title == "" {
		out.Title = r.Name
	}
	if r.PosterPath != "" {
		out.PosterURL = tmdbImageBase + r.PosterPath
	}
	for _, d := range []string{r.ReleaseDate, r.FirstAirDate} {
		if len(d) >= 4 {
			out.Year, _ = strconv.Atoi(d[:4])
			break
		}
	}
	return out, nil
}

type tmdbSeasonResponse struct {
	Overview   string `json:"overview"`
	PosterPath string `json:"poster_path"`
	Episodes   []struct {
		EpisodeNumber int    `json:"episode_number"`
		Name          string `json:"name"`
		Overview      string `json:"overview"`
		StillPath     string `json:"still_path"`
	} `json:"episodes"`
}

// TVSeason fetches a season's metadata: overview, poster, and the episode
// list with titles, overviews, and still frames.
func (t *TMDB) TVSeason(ctx context.Context, tvID, season int) (SeasonResult, error) {
	u := fmt.Sprintf("https://api.themoviedb.org/3/tv/%d/season/%d?api_key=%s", tvID, season, url.QueryEscape(t.APIKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return SeasonResult{}, err
	}
	res, err := t.Client.Do(req)
	if err != nil {
		return SeasonResult{}, fmt.Errorf("tmdb season: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return SeasonResult{}, ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return SeasonResult{}, fmt.Errorf("tmdb season: status %d", res.StatusCode)
	}
	var body tmdbSeasonResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return SeasonResult{}, fmt.Errorf("tmdb season: %w", err)
	}
	out := SeasonResult{Overview: body.Overview}
	if body.PosterPath != "" {
		out.PosterURL = tmdbImageBase + body.PosterPath
	}
	for _, e := range body.Episodes {
		ep := EpisodeResult{Num: e.EpisodeNumber, Title: e.Name, Overview: e.Overview}
		if e.StillPath != "" {
			ep.StillURL = tmdbImageBase + e.StillPath
		}
		out.Episodes = append(out.Episodes, ep)
	}
	return out, nil
}

// DownloadArtwork fetches url into dir under a stable name and returns the
// filename (relative to dir). Existing files are reused.
func DownloadArtwork(ctx context.Context, client *http.Client, rawURL, dir, name string) (string, error) {
	filename := name + filepath.Ext(rawURL)
	dest := filepath.Join(dir, filename)
	if _, err := os.Stat(dest); err == nil {
		return filename, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("artwork dir: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download artwork: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download artwork: status %d", res.StatusCode)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("write artwork: %w", err)
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("write artwork: %w", err)
	}
	f.Close()
	if err := os.Rename(tmp, dest); err != nil {
		return "", fmt.Errorf("write artwork: %w", err)
	}
	return filename, nil
}
