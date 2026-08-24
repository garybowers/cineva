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

// VideoProvider is the video-domain metadata interface (see CLAUDE.md).
type VideoProvider interface {
	SearchMovie(ctx context.Context, title string, year int) (VideoResult, error)
	SearchTV(ctx context.Context, name string) (VideoResult, error)
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
