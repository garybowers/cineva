package metadata

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Discovery is TMDB as a catalogue of everything, rather than a matcher for
// files already held: what search shows under "Not in your library", and
// the detail page of a title nobody has yet. Results are cached, since the
// same title is looked at by several people and a search box asks again on
// every keystroke that settles.

// DiscoverResult is one title found by a search.
type DiscoverResult struct {
	MediaType   string  `json:"mediaType"` // "movie" | "tv"
	TmdbID      int     `json:"tmdbId"`
	Title       string  `json:"title"`
	Year        int     `json:"year,omitempty"`
	Overview    string  `json:"overview,omitempty"`
	PosterURL   string  `json:"poster,omitempty"`
	BackdropURL string  `json:"backdrop,omitempty"`
	Popularity  float64 `json:"-"`
}

// SeasonSummary is one season as a series' detail lists it.
type SeasonSummary struct {
	Number       int    `json:"seasonNumber"`
	Name         string `json:"name"`
	EpisodeCount int    `json:"episodeCount"`
	AirDate      string `json:"airDate,omitempty"`
	PosterURL    string `json:"poster,omitempty"`
}

// TitleDetail is a film or series in full, for a detail page.
type TitleDetail struct {
	VideoResult
	MediaType string
	// Seasons and TvdbID are television only. TvdbID is what Sonarr knows
	// a series by.
	Seasons []SeasonSummary
	TvdbID  int
}

// Discoverer is the discovery interface; *Discovery implements it over
// TMDB, and tests substitute their own.
type Discoverer interface {
	SearchTitles(ctx context.Context, query string) ([]DiscoverResult, error)
	MovieDetail(ctx context.Context, tmdbID int) (TitleDetail, error)
	TVDetail(ctx context.Context, tmdbID int) (TitleDetail, error)
	TVSeason(ctx context.Context, tvID, season int) (SeasonResult, error)
}

// tmdbThumbBase is the poster size for search rows.
const tmdbThumbBase = "https://image.tmdb.org/t/p/w185"

const (
	searchTTL = 15 * time.Minute
	detailTTL = 6 * time.Hour
	// cacheMax bounds the cache: a few hundred titles is a busy evening.
	cacheMax = 500
)

type cacheEntry struct {
	value   any
	expires time.Time
}

// Discovery wraps a TMDB client with a small time-limited cache.
type Discovery struct {
	tmdb *TMDB
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

var _ Discoverer = (*Discovery)(nil)

func NewDiscovery(t *TMDB) *Discovery {
	return &Discovery{tmdb: t, now: time.Now, cache: map[string]cacheEntry{}}
}

// cached returns the value under key, fetching and keeping it for ttl when
// it is missing or stale. Failures are not cached: TMDB being briefly
// unreachable should not hide a title for hours.
func cached[T any](d *Discovery, key string, ttl time.Duration, fetch func() (T, error)) (T, error) {
	d.mu.Lock()
	if e, ok := d.cache[key]; ok && d.now().Before(e.expires) {
		d.mu.Unlock()
		return e.value.(T), nil
	}
	d.mu.Unlock()
	v, err := fetch()
	if err != nil {
		return v, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.cache) >= cacheMax {
		now := d.now()
		for k, e := range d.cache {
			if !now.Before(e.expires) {
				delete(d.cache, k)
			}
		}
		// Still full of live entries: drop any one. Map order is as good
		// as any for a cache this size.
		for k := range d.cache {
			if len(d.cache) < cacheMax {
				break
			}
			delete(d.cache, k)
		}
	}
	d.cache[key] = cacheEntry{value: v, expires: d.now().Add(ttl)}
	return v, nil
}

type tmdbMultiResult struct {
	tmdbResult
	MediaType  string  `json:"media_type"`
	Popularity float64 `json:"popularity"`
}

// SearchTitles finds films and series by name, in TMDB's order. People and
// anything else a multi-search returns are left out.
func (d *Discovery) SearchTitles(ctx context.Context, query string) ([]DiscoverResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []DiscoverResult{}, nil
	}
	return cached(d, "search:"+strings.ToLower(query), searchTTL, func() ([]DiscoverResult, error) {
		params := url.Values{"query": {query}, "include_adult": {"false"}, "api_key": {d.tmdb.APIKey}}
		var body struct {
			Results []tmdbMultiResult `json:"results"`
		}
		if err := d.tmdb.getJSON(ctx, "https://api.themoviedb.org/3/search/multi?"+params.Encode(), &body); err != nil {
			return nil, fmt.Errorf("tmdb search: %w", err)
		}
		out := []DiscoverResult{}
		for _, r := range body.Results {
			if r.MediaType != "movie" && r.MediaType != "tv" {
				continue
			}
			v := r.toVideoResult()
			found := DiscoverResult{
				MediaType: r.MediaType, TmdbID: v.TmdbID, Title: v.Title, Year: v.Year,
				Overview: v.Overview, BackdropURL: v.BackdropURL, Popularity: r.Popularity,
			}
			// A search row shows a thumbnail, so the small size will do.
			if r.PosterPath != "" {
				found.PosterURL = tmdbThumbBase + r.PosterPath
			}
			out = append(out, found)
		}
		return out, nil
	})
}

// MovieDetail fetches a film in full, credits included.
func (d *Discovery) MovieDetail(ctx context.Context, tmdbID int) (TitleDetail, error) {
	return cached(d, "movie:"+strconv.Itoa(tmdbID), detailTTL, func() (TitleDetail, error) {
		v, err := d.tmdb.MovieByID(ctx, tmdbID)
		if err != nil {
			return TitleDetail{}, err
		}
		return TitleDetail{VideoResult: v, MediaType: "movie"}, nil
	})
}

type tmdbTVDetail struct {
	tmdbResult
	Seasons []struct {
		SeasonNumber int    `json:"season_number"`
		Name         string `json:"name"`
		EpisodeCount int    `json:"episode_count"`
		AirDate      string `json:"air_date"`
		PosterPath   string `json:"poster_path"`
	} `json:"seasons"`
	ExternalIDs *struct {
		TvdbID int `json:"tvdb_id"`
	} `json:"external_ids"`
}

// TVDetail fetches a series in full: credits, ratings, its seasons, and
// its TVDB id, which is how Sonarr is asked for it.
func (d *Discovery) TVDetail(ctx context.Context, tmdbID int) (TitleDetail, error) {
	return cached(d, "tv:"+strconv.Itoa(tmdbID), detailTTL, func() (TitleDetail, error) {
		u := fmt.Sprintf("https://api.themoviedb.org/3/tv/%d?api_key=%s&append_to_response=credits,content_ratings,external_ids",
			tmdbID, url.QueryEscape(d.tmdb.APIKey))
		var r tmdbTVDetail
		if err := d.tmdb.getJSON(ctx, u, &r); err != nil {
			return TitleDetail{}, fmt.Errorf("tmdb tv %d: %w", tmdbID, err)
		}
		if r.ID == 0 {
			return TitleDetail{}, ErrNotFound
		}
		out := TitleDetail{VideoResult: r.toVideoResult(), MediaType: "tv", Seasons: []SeasonSummary{}}
		if r.ExternalIDs != nil {
			out.TvdbID = r.ExternalIDs.TvdbID
		}
		for _, s := range r.Seasons {
			ss := SeasonSummary{Number: s.SeasonNumber, Name: s.Name, EpisodeCount: s.EpisodeCount, AirDate: s.AirDate}
			if s.PosterPath != "" {
				ss.PosterURL = tmdbImageBase + s.PosterPath
			}
			out.Seasons = append(out.Seasons, ss)
		}
		return out, nil
	})
}

// TVSeason is the season's episode list, cached like the rest.
func (d *Discovery) TVSeason(ctx context.Context, tvID, season int) (SeasonResult, error) {
	key := "season:" + strconv.Itoa(tvID) + ":" + strconv.Itoa(season)
	return cached(d, key, detailTTL, func() (SeasonResult, error) {
		return d.tmdb.TVSeason(ctx, tvID, season)
	})
}
