// Package metadata defines per-domain metadata provider interfaces and their
// implementations. TMDB covers the video domain.
package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// VideoResult is what a video metadata lookup returns.
type VideoResult struct {
	TmdbID    int
	Title     string
	Year      int
	Overview  string
	PosterURL string

	// Everything below comes from the detail endpoint rather than a search
	// hit, which carries only genre ids and no runtime, tagline or
	// collection at all.
	Genres      []string
	Rating      float64 // TMDB vote_average, 0-10
	VoteCount   int
	RuntimeMins int
	Tagline     string
	BackdropURL string
	// Certifications is the content rating per ISO 3166-1 country code.
	// The provider does not choose: certification is regional, so the
	// caller picks with CertificationFor.
	Certifications map[string]string
	// Collection groups sequels — "The Matrix Collection". Zero id when the
	// film belongs to none, which is most of them.
	CollectionID   int
	CollectionName string

	Cast []Person // billed order, trimmed to maxCast
	Crew []Person // key jobs only, see keyCrewJobs

	// Television only. A film has none of these: it does not run for
	// seasons, is not carried by a broadcaster, and is never "returning".
	//
	// Status is TMDB's own wording — "Ended", "Returning Series",
	// "Canceled" — kept verbatim rather than mapped to a boolean, because
	// "Canceled" and "Ended" mean different things to someone deciding
	// whether to start a series.
	Status string
	// Network is the original broadcaster. TMDB lists several when a series
	// changed hands; the first is the one it began on.
	Network string
	// SeasonCount and EpisodeCount are what TMDB says exists, which is not
	// what is on disk — the difference is the point, so a series page can
	// say "3 of 5 seasons".
	SeasonCount  int
	EpisodeCount int
	// FirstAired and LastAired are full dates where Year is only the year.
	FirstAired string
	LastAired  string
	// NextAired is when the next episode is due, for a series still
	// running. Empty for anything finished, which is most of them.
	NextAired string
	// OriginalTitle is the name in the series' own language — "Dark" is
	// German, "Money Heist" is "La Casa de Papel" — worth keeping because
	// it is often what the files on disk are named after.
	OriginalTitle string
	// Country is the ISO 3166-1 code the series originates from.
	Country string
	// SeasonPremieres lists a series' seasons with the date each began,
	// which is how an episode known only by its broadcast date is placed:
	// in the season that began most recently before it.
	SeasonPremieres []SeasonPremiere
}

// SeasonPremiere is one season of a series and the date it began.
type SeasonPremiere struct {
	Num     int
	AirDate string // "2023-09-05"; empty when not announced
}

// Person is one credit. Character is set for cast, Job for crew.
type Person struct {
	TmdbID     int
	Name       string
	Character  string
	Job        string
	Order      int
	ProfileURL string
}

// EpisodeResult is one episode's metadata within a season.
type EpisodeResult struct {
	TmdbID   int
	Num      int
	Title    string
	Overview string
	StillURL string
	// AirDate is the broadcast date, "2008-01-20". The only date an episode
	// has — without it two thirds of this library's episodes had no date at
	// all, and no correct way to sort by one.
	AirDate   string
	Rating    float64
	VoteCount int
	// GuestStars are the people in this episode and no other — the whole
	// point of an episode-level cast, since the regulars are already on the
	// series.
	GuestStars []Person
}

// SeasonResult is a TV season's metadata including its episode list.
type SeasonResult struct {
	TmdbID    int
	Overview  string
	PosterURL string
	AirDate   string
	Episodes  []EpisodeResult
}

// VideoProvider is the video-domain metadata interface (see CLAUDE.md).
type VideoProvider interface {
	SearchMovie(ctx context.Context, title string, year int) (VideoResult, error)
	// SearchTV takes a year because series reuse titles far more than films
	// do — "Alone" is a 2013 US series and a 2023 UK one — and the folder
	// name is usually the only thing that distinguishes them. Pass 0 when
	// the library gives no year.
	SearchTV(ctx context.Context, name string, year int) (VideoResult, error)
	TVSeason(ctx context.Context, tvID, season int) (SeasonResult, error)
	// SearchMovies returns the candidates rather than just the best one, so
	// an operator correcting a wrong match can choose between them.
	SearchMovies(ctx context.Context, title string, year int) ([]VideoResult, error)
	// MovieByID fetches one title directly, for applying a chosen match.
	MovieByID(ctx context.Context, tmdbID int) (VideoResult, error)
}

var ErrNotFound = fmt.Errorf("no metadata match")

type TMDB struct {
	APIKey string
	Client *http.Client
}

// defaultAPIKey is the project's own TMDB key, injected at release build
// time rather than committed:
//
//	go build -ldflags "-X github.com/garybowers/cineva/server/internal/metadata.defaultAPIKey=..."
//
// Shipping a key is the normal arrangement for a self-hosted media server —
// Jellyfin and Kodi both do it — because asking every user to register one
// before their library shows a single poster is a poor first run. TMDB
// permits it for applications that carry the attribution line, which the
// client's Settings page does.
//
// It is the lowest tier of the usual precedence: a key set by flag, env or
// config file is a deliberate override and always wins. Left empty in a
// source build, in which case the server behaves as it does today and says
// so at startup.
var defaultAPIKey string

// APIKey resolves the key to use: the explicit override when one is given,
// otherwise the built-in project key. The second return reports whether any
// key is available at all.
func APIKey(override string) (string, bool) {
	if override = strings.TrimSpace(override); override != "" {
		return override, true
	}
	return defaultAPIKey, defaultAPIKey != ""
}

func NewTMDB(apiKey string) *TMDB {
	return &TMDB{APIKey: apiKey, Client: &http.Client{Timeout: 15 * time.Second}}
}

// A film carries a few dozen cast and, for a large production, several
// hundred crew — 538 on one title here. Storing all of it would bloat the
// database and the detail page for no benefit, so cast is capped at the
// billed order the page can show and crew is limited to the jobs a viewer
// actually looks for.
const maxCast = 20

// Ordered by what a viewer looks for first, not the order TMDB happens to
// return — which is department order, so a film's executive producers
// arrive long before its director.
var crewJobRank = map[string]int{
	"Director": 0, "Screenplay": 1, "Writer": 1, "Story": 2, "Novel": 2,
	"Director of Photography": 3, "Original Music Composer": 4,
	"Producer": 5, "Executive Producer": 6,
}

const (
	tmdbImageBase = "https://image.tmdb.org/t/p/w500"
	// Headshots are shown small, so the smallest useful size keeps the
	// artwork directory from ballooning across thousands of people.
	tmdbProfileBase = "https://image.tmdb.org/t/p/w185"
	// Backdrops are shown full-bleed behind a detail page, so they want
	// more width than a poster.
	tmdbBackdropBase = "https://image.tmdb.org/t/p/w1280"
)

// tmdbResult is one title as TMDB returns it, from either a search hit or a
// direct fetch — the shapes agree on the fields we use.
type tmdbResult struct {
	ID           int    `json:"id"`
	Title        string `json:"title"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
	ReleaseDate  string `json:"release_date"`
	FirstAirDate string `json:"first_air_date"`
	// Detail-only fields; absent and harmless on a search hit.
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres"`
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
	Runtime     int     `json:"runtime"`
	Tagline     string  `json:"tagline"`
	Collection  *struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"belongs_to_collection"`
	Credits *tmdbCredits `json:"credits"`
	// release_dates carries the per-country certification. TMDB has no
	// single "the" certification: a film is classified separately by each
	// board, so the response is a list per country and we pick a region.
	ReleaseDates *struct {
		Results []struct {
			Country      string `json:"iso_3166_1"`
			ReleaseDates []struct {
				Certification string `json:"certification"`
				Type          int    `json:"type"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
	// content_ratings is the television equivalent of release_dates: one
	// rating per country, with no release-type breakdown because a series
	// is not released in cinemas and then on disc.
	ContentRatings *struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
	// A series has a runtime per episode rather than one for the whole
	// thing, and TMDB gives a list because it can vary.
	EpisodeRunTime   []int  `json:"episode_run_time"`
	Status           string `json:"status"`
	LastAirDate      string `json:"last_air_date"`
	NumberOfSeasons  int    `json:"number_of_seasons"`
	NumberOfEpisodes int    `json:"number_of_episodes"`
	Networks         []struct {
		Name string `json:"name"`
	} `json:"networks"`
	OriginalName     string   `json:"original_name"`
	OriginCountry    []string `json:"origin_country"`
	NextEpisodeToAir *struct {
		AirDate string `json:"air_date"`
	} `json:"next_episode_to_air"`
	SeasonList []struct {
		SeasonNumber int    `json:"season_number"`
		AirDate      string `json:"air_date"`
	} `json:"seasons"`
	// created_by is television's answer to a director credit, and sits
	// outside `credits` in TMDB's response.
	CreatedBy []struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		ProfilePath string `json:"profile_path"`
	} `json:"created_by"`
}

// tmdbCredits is the `credits` sub-object folded in by append_to_response.
type tmdbCredits struct {
	Cast []struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Character   string `json:"character"`
		Order       int    `json:"order"`
		ProfilePath string `json:"profile_path"`
	} `json:"cast"`
	Crew []struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Job         string `json:"job"`
		ProfilePath string `json:"profile_path"`
	} `json:"crew"`
}

// certifications collects the content rating for every country TMDB has
// one for, keyed by ISO 3166-1 code. The caller picks the region, because
// certification is regional by nature: a BBFC "15" and an FSK "16" are
// different boards' judgements, not two spellings of one value, and which
// one a viewer wants depends on where they are.
func (r *tmdbResult) certifications() map[string]string {
	out := map[string]string{}
	// Television first: a result carries one or the other, never both.
	if r.ContentRatings != nil {
		for _, res := range r.ContentRatings.Results {
			if c := strings.TrimSpace(res.Rating); c != "" {
				out[res.Country] = c
			}
		}
	}
	if r.ReleaseDates == nil {
		if len(out) == 0 {
			return nil
		}
		return out
	}
	for _, res := range r.ReleaseDates.Results {
		for _, rd := range res.ReleaseDates {
			if c := strings.TrimSpace(rd.Certification); c != "" {
				// First non-empty wins: a country lists several release
				// types (cinema, digital, physical) that usually agree.
				if _, seen := out[res.Country]; !seen {
					out[res.Country] = c
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (r tmdbResult) toVideoResult() VideoResult {
	out := VideoResult{TmdbID: r.ID, Title: r.Title, Overview: r.Overview}
	if out.Title == "" {
		out.Title = r.Name // TV uses "name" where film uses "title"
	}
	if r.PosterPath != "" {
		out.PosterURL = tmdbImageBase + r.PosterPath
	}
	if r.BackdropPath != "" {
		out.BackdropURL = tmdbBackdropBase + r.BackdropPath
	}
	for _, g := range r.Genres {
		out.Genres = append(out.Genres, g.Name)
	}
	out.Rating, out.VoteCount = r.VoteAverage, r.VoteCount
	out.Status, out.LastAired = r.Status, r.LastAirDate
	out.SeasonCount, out.EpisodeCount = r.NumberOfSeasons, r.NumberOfEpisodes
	if r.FirstAirDate != "" {
		out.FirstAired = r.FirstAirDate
	} else {
		out.FirstAired = r.ReleaseDate
	}
	if len(r.Networks) > 0 {
		out.Network = r.Networks[0].Name
	}
	if r.NextEpisodeToAir != nil {
		out.NextAired = r.NextEpisodeToAir.AirDate
	}
	if len(r.OriginCountry) > 0 {
		out.Country = r.OriginCountry[0]
	}
	// Only worth keeping when it differs; storing "The IT Crowd" twice
	// helps nobody and puts it on screen for every English-language show.
	if r.OriginalName != "" && r.OriginalName != out.Title {
		out.OriginalTitle = r.OriginalName
	}
	for _, sn := range r.SeasonList {
		out.SeasonPremieres = append(out.SeasonPremieres, SeasonPremiere{Num: sn.SeasonNumber, AirDate: sn.AirDate})
	}
	// Creators are crew, and join the crew list so one display path covers
	// "Director" for a film and "Creator" for a series.
	for _, c := range r.CreatedBy {
		p := Person{TmdbID: c.ID, Name: c.Name, Job: "Creator"}
		if c.ProfilePath != "" {
			p.ProfileURL = tmdbProfileBase + c.ProfilePath
		}
		out.Crew = append(out.Crew, p)
	}
	out.RuntimeMins, out.Tagline = r.Runtime, r.Tagline
	// A series reports its runtime per episode instead.
	if out.RuntimeMins == 0 && len(r.EpisodeRunTime) > 0 {
		out.RuntimeMins = r.EpisodeRunTime[0]
	}
	out.Certifications = r.certifications()
	if r.Collection != nil {
		out.CollectionID, out.CollectionName = r.Collection.ID, r.Collection.Name
	}
	if r.Credits != nil {
		profile := func(p string) string {
			if p == "" {
				return ""
			}
			return tmdbProfileBase + p
		}
		for i, c := range r.Credits.Cast {
			if i >= maxCast {
				break
			}
			out.Cast = append(out.Cast, Person{
				TmdbID: c.ID, Name: c.Name, Character: c.Character,
				Order: c.Order, ProfileURL: profile(c.ProfilePath),
			})
		}
		// One person can hold several key jobs on a film (writer and
		// director); each is its own credit, but keep them in job order
		// rather than letting duplicates pile up.
		for _, c := range r.Credits.Crew {
			if _, keep := crewJobRank[c.Job]; !keep {
				continue
			}
			out.Crew = append(out.Crew, Person{
				TmdbID: c.ID, Name: c.Name, Job: c.Job,
				Order: crewJobRank[c.Job], ProfileURL: profile(c.ProfilePath),
			})
		}
		sort.SliceStable(out.Crew, func(i, j int) bool { return out.Crew[i].Order < out.Crew[j].Order })
	}
	for _, d := range []string{r.ReleaseDate, r.FirstAirDate} {
		if len(d) >= 4 {
			out.Year, _ = strconv.Atoi(d[:4])
			break
		}
	}
	return out
}

type tmdbSearchResponse struct {
	Results []tmdbResult `json:"results"`
}

// SearchMovie finds the best match and returns it in full.
//
// A search hit carries only genre *ids*, no runtime, tagline or collection,
// so the best candidate is fetched from the detail endpoint. That is a
// second request per title, which is the price of storing anything beyond a
// poster and a synopsis.
func (t *TMDB) SearchMovie(ctx context.Context, title string, year int) (VideoResult, error) {
	results, err := t.SearchMovies(ctx, title, year)
	if err != nil {
		return VideoResult{}, err
	}
	// Not results[0]: TMDB orders by its own relevance, which for a common
	// title is the most popular film of that name rather than the one the
	// filename describes. See bestMatch.
	pick := bestMatch(results, title, year)
	full, err := t.MovieByID(ctx, pick.TmdbID)
	if err != nil {
		return pick, nil // the search hit is still better than nothing
	}
	return full, nil
}

// SearchMovies returns every candidate TMDB offers, best first.
//
// Note that TMDB's year parameter only nudges relevance, it does not filter,
// so the first result is regularly wrong for a common title — which is
// exactly why the operator needs to see the rest.
func (t *TMDB) SearchMovies(ctx context.Context, title string, year int) ([]VideoResult, error) {
	params := url.Values{"query": {title}}
	if year > 0 {
		params.Set("year", strconv.Itoa(year))
	}
	return t.searchAll(ctx, "movie", params)
}

// MovieByID fetches one film by its TMDB id, for applying a chosen match or
// an id hint taken from a filename.
func (t *TMDB) MovieByID(ctx context.Context, tmdbID int) (VideoResult, error) {
	// append_to_response folds the credits into the same request, so cast
	// and crew cost nothing beyond the detail call already being made.
	u := fmt.Sprintf("https://api.themoviedb.org/3/movie/%d?api_key=%s&append_to_response=credits,release_dates",
		tmdbID, url.QueryEscape(t.APIKey))
	var r tmdbResult
	if err := t.getJSON(ctx, u, &r); err != nil {
		return VideoResult{}, fmt.Errorf("tmdb movie %d: %w", tmdbID, err)
	}
	if r.ID == 0 {
		return VideoResult{}, ErrNotFound
	}
	return r.toVideoResult(), nil
}

func (t *TMDB) SearchTV(ctx context.Context, name string, year int) (VideoResult, error) {
	params := url.Values{"query": {name}}
	if year > 0 {
		params.Set("first_air_date_year", strconv.Itoa(year))
	}
	results, err := t.searchAll(ctx, "tv", params)
	// A year narrows the field, but a wrong one — a folder named for the
	// year a box set was released rather than the year the series began —
	// would otherwise turn a good match into no match at all.
	if err != nil && year > 0 && errors.Is(err, ErrNotFound) {
		results, err = t.searchAll(ctx, "tv", url.Values{"query": {name}})
	}
	if err != nil {
		return VideoResult{}, err
	}
	// Not results[0], for the same reason films are not: TMDB orders by its
	// own relevance, which for a common title is the most popular series of
	// that name rather than the one on disk.
	pick := bestMatch(results, name, year)
	full, err := t.TVByID(ctx, pick.TmdbID)
	if err != nil {
		return pick, nil // the search hit is still better than nothing
	}
	return full, nil
}

// TVByID fetches one series in full.
//
// Needed because a search hit carries genre *ids* and nothing else of
// substance: without this, series had no genres, no cast, no tagline and no
// age rating, while films had all four — the difference being that
// SearchMovie has always followed its search with a detail call and
// SearchTV never did.
func (t *TMDB) TVByID(ctx context.Context, tmdbID int) (VideoResult, error) {
	u := fmt.Sprintf("https://api.themoviedb.org/3/tv/%d?api_key=%s&append_to_response=credits,content_ratings",
		tmdbID, url.QueryEscape(t.APIKey))
	var r tmdbResult
	if err := t.getJSON(ctx, u, &r); err != nil {
		return VideoResult{}, fmt.Errorf("tmdb tv %d: %w", tmdbID, err)
	}
	if r.ID == 0 {
		return VideoResult{}, ErrNotFound
	}
	return r.toVideoResult(), nil
}

func (t *TMDB) search(ctx context.Context, kind string, params url.Values) (VideoResult, error) {
	results, err := t.searchAll(ctx, kind, params)
	if err != nil {
		return VideoResult{}, err
	}
	return results[0], nil
}

// searchAll returns every result, best first, or ErrNotFound when there are
// none — so callers wanting just the best can take the first safely.
func (t *TMDB) searchAll(ctx context.Context, kind string, params url.Values) ([]VideoResult, error) {
	params.Set("api_key", t.APIKey)
	u := fmt.Sprintf("https://api.themoviedb.org/3/search/%s?%s", kind, params.Encode())
	var body tmdbSearchResponse
	if err := t.getJSON(ctx, u, &body); err != nil {
		return nil, fmt.Errorf("tmdb search: %w", err)
	}
	if len(body.Results) == 0 {
		return nil, ErrNotFound
	}
	out := make([]VideoResult, 0, len(body.Results))
	for _, r := range body.Results {
		out = append(out, r.toVideoResult())
	}
	return out, nil
}

// isReadToken reports whether the credential is a v4 "API Read Access
// Token" rather than a v3 "API Key". TMDB's settings page issues both and
// does not make the difference obvious: the key is a 32-character hex
// string passed as ?api_key=, while the token is a JWT passed as a Bearer
// header. Sending one where the other is expected fails with a bare 401, so
// the shape is detected and the right scheme used either way.
func isReadToken(cred string) bool { return strings.HasPrefix(cred, "eyJ") }

func (t *TMDB) getJSON(ctx context.Context, u string, into any) error {
	// A read token must not travel in the query string, so strip the
	// api_key the callers added and send the header instead.
	if isReadToken(t.APIKey) {
		if parsed, err := url.Parse(u); err == nil {
			q := parsed.Query()
			q.Del("api_key")
			parsed.RawQuery = q.Encode()
			u = parsed.String()
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if isReadToken(t.APIKey) {
		req.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	res, err := t.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(into)
}

type tmdbSeasonResponse struct {
	ID         int    `json:"id"`
	Overview   string `json:"overview"`
	PosterPath string `json:"poster_path"`
	AirDate    string `json:"air_date"`
	Episodes   []struct {
		ID            int     `json:"id"`
		EpisodeNumber int     `json:"episode_number"`
		Name          string  `json:"name"`
		Overview      string  `json:"overview"`
		StillPath     string  `json:"still_path"`
		AirDate       string  `json:"air_date"`
		VoteAverage   float64 `json:"vote_average"`
		VoteCount     int     `json:"vote_count"`
		GuestStars    []struct {
			ID          int    `json:"id"`
			Name        string `json:"name"`
			Character   string `json:"character"`
			Order       int    `json:"order"`
			ProfilePath string `json:"profile_path"`
		} `json:"guest_stars"`
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
	out := SeasonResult{TmdbID: body.ID, Overview: body.Overview, AirDate: body.AirDate}
	if body.PosterPath != "" {
		out.PosterURL = tmdbImageBase + body.PosterPath
	}
	for _, e := range body.Episodes {
		ep := EpisodeResult{
			TmdbID: e.ID, Num: e.EpisodeNumber, Title: e.Name, Overview: e.Overview,
			AirDate: e.AirDate, Rating: e.VoteAverage, VoteCount: e.VoteCount,
		}
		for i, g := range e.GuestStars {
			if i >= maxCast {
				break
			}
			p := Person{TmdbID: g.ID, Name: g.Name, Character: g.Character, Order: g.Order}
			if g.ProfilePath != "" {
				p.ProfileURL = tmdbProfileBase + g.ProfilePath
			}
			ep.GuestStars = append(ep.GuestStars, p)
		}
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
