package arr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/garybowers/cineva/server/internal/store"
)

// The calls requests need: what an instance offers (quality profiles, root
// folders, health), and adding or monitoring a film or series and searching
// for it. Films and series are kept as the instance returns them, whole, so
// that updating one sends back every field — including those this client
// has never heard of — rather than blanking them.

// QualityProfile is one of an instance's quality profiles.
type QualityProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// RootFolder is a folder the instance puts new titles under.
type RootFolder struct {
	ID         int    `json:"id"`
	Path       string `json:"path"`
	FreeSpace  int64  `json:"freeSpace"`
	Accessible bool   `json:"accessible"`
}

// HealthCheck is one of the warnings an instance shows on its System page.
type HealthCheck struct {
	Source  string `json:"source"`
	Type    string `json:"type"` // "ok" | "notice" | "warning" | "error"
	Message string `json:"message"`
}

// Resource is a film or series as the instance returns it.
type Resource map[string]any

func (r Resource) int(key string) int {
	if f, ok := r[key].(float64); ok {
		return int(f)
	}
	return 0
}

func (r Resource) bool(key string) bool {
	b, _ := r[key].(bool)
	return b
}

// ID is the title's id inside the instance.
func (r Resource) ID() int { return r.int("id") }

// HasFile reports whether a film is already downloaded.
func (r Resource) HasFile() bool { return r.bool("hasFile") }

// Episode is one episode as Sonarr lists it.
type Episode struct {
	ID            int    `json:"id"`
	SeriesID      int    `json:"seriesId"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"episodeNumber"`
	Title         string `json:"title"`
	AirDate       string `json:"airDate"`    // local broadcast date, "2008-01-20"
	AirDateUTC    string `json:"airDateUtc"` // RFC 3339
	HasFile       bool   `json:"hasFile"`
	Monitored     bool   `json:"monitored"`
}

// QueueItem is a download the instance is tracking.
type QueueItem struct {
	MovieID   int    `json:"movieId"`
	SeriesID  int    `json:"seriesId"`
	EpisodeID int    `json:"episodeId"`
	Title     string `json:"title"`
	Status    string `json:"status"`
}

// AddOptions are what a new title is added with.
type AddOptions struct {
	QualityProfileID int
	RootFolder       string
}

func (c *Client) QualityProfiles(ctx context.Context) ([]QualityProfile, error) {
	out := []QualityProfile{}
	err := c.do(ctx, http.MethodGet, "/api/v3/qualityprofile", nil, &out)
	return out, err
}

func (c *Client) RootFolders(ctx context.Context) ([]RootFolder, error) {
	out := []RootFolder{}
	err := c.do(ctx, http.MethodGet, "/api/v3/rootfolder", nil, &out)
	return out, err
}

func (c *Client) Health(ctx context.Context) ([]HealthCheck, error) {
	out := []HealthCheck{}
	err := c.do(ctx, http.MethodGet, "/api/v3/health", nil, &out)
	return out, err
}

// Queue lists what the instance is downloading. One page is plenty: the
// question is only whether a requested title is among them.
func (c *Client) Queue(ctx context.Context) ([]QueueItem, error) {
	var page struct {
		Records []QueueItem `json:"records"`
	}
	err := c.do(ctx, http.MethodGet, "/api/v3/queue?page=1&pageSize=250", nil, &page)
	if page.Records == nil {
		page.Records = []QueueItem{}
	}
	return page.Records, err
}

func (c *Client) command(ctx context.Context, body map[string]any) error {
	return c.do(ctx, http.MethodPost, "/api/v3/command", body, nil)
}

// Defaults fills in a profile and root folder an admin has not chosen with
// the instance's first, which is what its own Add dialogue offers.
func (c *Client) Defaults(ctx context.Context, opts AddOptions) (AddOptions, error) {
	if opts.QualityProfileID == 0 {
		profiles, err := c.QualityProfiles(ctx)
		if err != nil {
			return opts, err
		}
		if len(profiles) == 0 {
			return opts, fmt.Errorf("%s has no quality profiles", c.Label())
		}
		opts.QualityProfileID = profiles[0].ID
	}
	if opts.RootFolder == "" {
		roots, err := c.RootFolders(ctx)
		if err != nil {
			return opts, err
		}
		if len(roots) == 0 {
			return opts, fmt.Errorf("%s has no root folders: add one under Settings, Media Management", c.Label())
		}
		opts.RootFolder = roots[0].Path
	}
	return opts, nil
}

// alreadyAdded reports whether an add failed because the title is there
// already — someone added it by hand between our look and our add.
func alreadyAdded(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(se.Message), "already")
}

// ---- Radarr ----

// MovieByTMDB returns the film if the instance has it, nil if not.
func (c *Client) MovieByTMDB(ctx context.Context, tmdbID int) (Resource, error) {
	var list []Resource
	if err := c.do(ctx, http.MethodGet, "/api/v3/movie?tmdbId="+strconv.Itoa(tmdbID), nil, &list); err != nil {
		return nil, err
	}
	for _, m := range list {
		if m.int("tmdbId") == tmdbID {
			return m, nil
		}
	}
	return nil, nil
}

// RequestMovie makes sure Radarr has the film, monitored, and is looking
// for it: added and searched for when it is new; monitored and searched
// for when it was there unmonitored or without a file. It returns the
// film's id in Radarr.
func (c *Client) RequestMovie(ctx context.Context, tmdbID int, opts AddOptions) (int, error) {
	movie, err := c.MovieByTMDB(ctx, tmdbID)
	if err != nil {
		return 0, err
	}
	if movie == nil {
		if movie, err = c.addMovie(ctx, tmdbID, opts); err != nil {
			return 0, err
		}
		if movie != nil {
			return movie.ID(), nil // added with searchForMovie
		}
		// Added meanwhile by someone else: carry on as for one we had.
		if movie, err = c.MovieByTMDB(ctx, tmdbID); err != nil {
			return 0, err
		}
		if movie == nil {
			return 0, fmt.Errorf("%s says the film was already added but does not list it", c.Label())
		}
	}
	id := movie.ID()
	if !movie.bool("monitored") {
		movie["monitored"] = true
		if err := c.do(ctx, http.MethodPut, "/api/v3/movie/"+strconv.Itoa(id), movie, nil); err != nil {
			return 0, err
		}
	}
	if !movie.HasFile() {
		if err := c.command(ctx, map[string]any{"name": "MoviesSearch", "movieIds": []int{id}}); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// addMovie adds a film Radarr does not have. A nil result with no error
// means it turned out to be there already.
func (c *Client) addMovie(ctx context.Context, tmdbID int, opts AddOptions) (Resource, error) {
	opts, err := c.Defaults(ctx, opts)
	if err != nil {
		return nil, err
	}
	var movie Resource
	if err := c.do(ctx, http.MethodGet, "/api/v3/movie/lookup/tmdb?tmdbId="+strconv.Itoa(tmdbID), nil, &movie); err != nil {
		return nil, err
	}
	if movie == nil || movie.int("tmdbId") == 0 {
		return nil, fmt.Errorf("%s cannot find TMDB film %d", c.Label(), tmdbID)
	}
	movie["qualityProfileId"] = opts.QualityProfileID
	movie["rootFolderPath"] = opts.RootFolder
	movie["monitored"] = true
	if s, _ := movie["minimumAvailability"].(string); s == "" {
		movie["minimumAvailability"] = "released"
	}
	movie["addOptions"] = map[string]any{"searchForMovie": true}
	var saved Resource
	err = c.do(ctx, http.MethodPost, "/api/v3/movie", movie, &saved)
	if alreadyAdded(err) {
		return nil, nil
	}
	return saved, err
}

// ---- Sonarr ----

// SeriesByTVDB returns the series if the instance has it, nil if not.
func (c *Client) SeriesByTVDB(ctx context.Context, tvdbID int) (Resource, error) {
	var list []Resource
	if err := c.do(ctx, http.MethodGet, "/api/v3/series?tvdbId="+strconv.Itoa(tvdbID), nil, &list); err != nil {
		return nil, err
	}
	for _, s := range list {
		if s.int("tvdbId") == tvdbID {
			return s, nil
		}
	}
	return nil, nil
}

// Episodes lists every episode Sonarr knows of a series, held or not.
func (c *Client) Episodes(ctx context.Context, seriesID int) ([]Episode, error) {
	out := []Episode{}
	err := c.do(ctx, http.MethodGet, "/api/v3/episode?seriesId="+strconv.Itoa(seriesID), nil, &out)
	return out, err
}

// SeriesScope is what of a series is wanted: everything, some seasons, or
// single episodes.
type SeriesScope struct {
	Seasons  []int
	Episodes []store.EpisodeRef
}

func (s SeriesScope) whole() bool { return len(s.Seasons) == 0 && len(s.Episodes) == 0 }

// wantsSeason reports whether a season is to be monitored as a whole.
// Specials (season 0) are only wanted when asked for by number.
func (s SeriesScope) wantsSeason(n int) bool {
	if s.whole() {
		return n > 0
	}
	return slices.Contains(s.Seasons, n)
}

// RequestSeries makes sure Sonarr has the series with the wanted seasons
// or episodes monitored, and is searching for them. It returns the
// series' id in Sonarr.
//
// A new series is added with only the wanted seasons monitored. Single
// episodes of a series Sonarr does not yet have are widened to their
// seasons: Sonarr fills a new series' episode list in the background, so
// there are no episodes to monitor at the moment it is added.
func (c *Client) RequestSeries(ctx context.Context, tvdbID int, opts AddOptions, scope SeriesScope) (int, error) {
	series, err := c.SeriesByTVDB(ctx, tvdbID)
	if err != nil {
		return 0, err
	}
	if series == nil {
		added := scope
		for _, e := range scope.Episodes {
			added.Seasons = append(added.Seasons, e.Season)
		}
		added.Episodes = nil
		if series, err = c.addSeries(ctx, tvdbID, opts, added); err != nil {
			return 0, err
		}
		if series != nil {
			return series.ID(), nil // added with searchForMissingEpisodes
		}
		if series, err = c.SeriesByTVDB(ctx, tvdbID); err != nil {
			return 0, err
		}
		if series == nil {
			return 0, fmt.Errorf("%s says the series was already added but does not list it", c.Label())
		}
	}
	id := series.ID()
	if err := c.monitorSeries(ctx, series, scope); err != nil {
		return 0, err
	}
	switch {
	case scope.whole():
		return id, c.command(ctx, map[string]any{"name": "SeriesSearch", "seriesId": id})
	case len(scope.Episodes) > 0:
		if err := c.SearchEpisodes(ctx, id, scope.Episodes); err != nil {
			return 0, err
		}
	}
	for _, n := range scope.Seasons {
		if err := c.command(ctx, map[string]any{"name": "SeasonSearch", "seriesId": id, "seasonNumber": n}); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// monitorSeries turns monitoring on for the series and the wanted seasons,
// leaving seasons already monitored as they are.
func (c *Client) monitorSeries(ctx context.Context, series Resource, scope SeriesScope) error {
	changed := !series.bool("monitored")
	series["monitored"] = true
	seasons, _ := series["seasons"].([]any)
	for _, s := range seasons {
		season, ok := s.(map[string]any)
		if !ok {
			continue
		}
		n := Resource(season).int("seasonNumber")
		if scope.wantsSeason(n) && !Resource(season).bool("monitored") {
			season["monitored"] = true
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return c.do(ctx, http.MethodPut, "/api/v3/series/"+strconv.Itoa(series.ID()), series, nil)
}

// addSeries adds a series Sonarr does not have. A nil result with no error
// means it turned out to be there already.
func (c *Client) addSeries(ctx context.Context, tvdbID int, opts AddOptions, scope SeriesScope) (Resource, error) {
	opts, err := c.Defaults(ctx, opts)
	if err != nil {
		return nil, err
	}
	var found []Resource
	if err := c.do(ctx, http.MethodGet, "/api/v3/series/lookup?term="+url.QueryEscape("tvdb:"+strconv.Itoa(tvdbID)), nil, &found); err != nil {
		return nil, err
	}
	var series Resource
	for _, s := range found {
		if s.int("tvdbId") == tvdbID {
			series = s
			break
		}
	}
	if series == nil {
		return nil, fmt.Errorf("%s cannot find TVDB series %d", c.Label(), tvdbID)
	}
	series["qualityProfileId"] = opts.QualityProfileID
	series["rootFolderPath"] = opts.RootFolder
	series["seasonFolder"] = true
	series["monitored"] = true
	// New seasons follow a whole-series request; a request for particular
	// seasons is just those.
	series["monitorNewItems"] = "none"
	if scope.whole() {
		series["monitorNewItems"] = "all"
	}
	// Sonarr 3 refuses a series without a language profile; Sonarr 4 has
	// none and ignores the field. Use the first one where there are any.
	if series.int("languageProfileId") == 0 {
		var langs []QualityProfile
		if err := c.do(ctx, http.MethodGet, "/api/v3/languageprofile", nil, &langs); err == nil && len(langs) > 0 {
			series["languageProfileId"] = langs[0].ID
		}
	}
	seasons, _ := series["seasons"].([]any)
	for _, s := range seasons {
		if season, ok := s.(map[string]any); ok {
			season["monitored"] = scope.wantsSeason(Resource(season).int("seasonNumber"))
		}
	}
	// No "monitor" strategy here: anything but the default would override
	// the season flags just set.
	series["addOptions"] = map[string]any{"searchForMissingEpisodes": true, "ignoreEpisodesWithFiles": true}
	var saved Resource
	err = c.do(ctx, http.MethodPost, "/api/v3/series", series, &saved)
	if alreadyAdded(err) {
		return nil, nil
	}
	return saved, err
}

// SearchEpisodes monitors the given episodes of a series and searches for
// them. Episodes Sonarr does not list are skipped; none found is an error.
func (c *Client) SearchEpisodes(ctx context.Context, seriesID int, want []store.EpisodeRef) error {
	eps, err := c.Episodes(ctx, seriesID)
	if err != nil {
		return err
	}
	ids := []int{}
	for _, e := range eps {
		if slices.Contains(want, store.EpisodeRef{Season: e.SeasonNumber, Episode: e.EpisodeNumber}) {
			ids = append(ids, e.ID)
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("%s does not list those episodes", c.Label())
	}
	if err := c.do(ctx, http.MethodPut, "/api/v3/episode/monitor", map[string]any{"episodeIds": ids, "monitored": true}, nil); err != nil {
		return err
	}
	return c.command(ctx, map[string]any{"name": "EpisodeSearch", "episodeIds": ids})
}

// SearchSeasons monitors whole seasons of a series Sonarr has and searches
// for them.
func (c *Client) SearchSeasons(ctx context.Context, seriesID int, seasons []int) error {
	var series Resource
	if err := c.do(ctx, http.MethodGet, "/api/v3/series/"+strconv.Itoa(seriesID), nil, &series); err != nil {
		return err
	}
	if err := c.monitorSeries(ctx, series, SeriesScope{Seasons: seasons}); err != nil {
		return err
	}
	for _, n := range seasons {
		if err := c.command(ctx, map[string]any{"name": "SeasonSearch", "seriesId": seriesID, "seasonNumber": n}); err != nil {
			return err
		}
	}
	return nil
}
