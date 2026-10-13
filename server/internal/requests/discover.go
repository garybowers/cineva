package requests

import (
	"context"
	"slices"
	"strconv"

	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// Found is a search result outside the library, with the status of any
// request for it.
type Found struct {
	metadata.DiscoverResult
	// Request is the status of the newest open request for the title, ""
	// when there is none.
	Request string `json:"request,omitempty"`
}

// maxFound caps the row: it sits under the library's own results.
const maxFound = 20

// Search finds films and series on TMDB that are not in any library the
// viewer can see (visible says which they can), of the kinds that can be
// requested.
func (s *Service) Search(ctx context.Context, st Status, q string, visible func(libraryID string) bool) ([]Found, error) {
	out := []Found{}
	if s.Discover == nil || !st.Enabled {
		return out, nil
	}
	results, err := s.Discover.SearchTitles(ctx, q)
	if err != nil {
		return nil, err
	}
	// TMDB's own order puts a posterless short with the exact title above
	// the series everyone means. Titles with artwork first, then the more
	// popular; a copy, since the slice is the cache's.
	results = slices.Clone(results)
	slices.SortStableFunc(results, func(a, b metadata.DiscoverResult) int {
		if (a.PosterURL == "") != (b.PosterURL == "") {
			if a.PosterURL == "" {
				return 1
			}
			return -1
		}
		switch {
		case a.Popularity > b.Popularity:
			return -1
		case a.Popularity < b.Popularity:
			return 1
		}
		return 0
	})
	var films, shows []int
	for _, r := range results {
		if r.MediaType == "movie" {
			films = append(films, r.TmdbID)
		} else {
			shows = append(shows, r.TmdbID)
		}
	}
	held := map[string]bool{}
	for kind, ids := range map[store.ItemKind][]int{store.KindMovie: films, store.KindSeries: shows} {
		items, err := s.Store.ItemsByTmdbIDs(ctx, kind, ids)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if visible(it.LibraryID) {
				held[key(mediaTypeOf(kind), it.TmdbID)] = true
			}
		}
	}
	statuses, err := s.requestStatuses(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range results {
		if held[key(r.MediaType, r.TmdbID)] || (r.MediaType == "movie" && !st.Movies) || (r.MediaType == "tv" && !st.TV) {
			continue
		}
		out = append(out, Found{DiscoverResult: r, Request: statuses[key(r.MediaType, r.TmdbID)]})
		if len(out) == maxFound {
			break
		}
	}
	return out, nil
}

func key(mediaType string, tmdbID int) string { return mediaType + ":" + strconv.Itoa(tmdbID) }

func mediaTypeOf(kind store.ItemKind) string {
	if kind == store.KindMovie {
		return store.RequestMovie
	}
	return store.RequestTV
}

// requestStatuses maps each title with an open request to the newest one's
// status.
func (s *Service) requestStatuses(ctx context.Context) (map[string]string, error) {
	open, err := s.Store.Requests(ctx, store.RequestFilter{Statuses: pending, Limit: 1000})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range open { // newest first, so the first seen wins
		k := key(r.MediaType, r.TmdbID)
		if _, ok := out[k]; !ok {
			out[k] = r.Status
		}
	}
	return out, nil
}

// Credit is one person on a title page.
type Credit struct {
	TmdbID  int    `json:"tmdbId"`
	Name    string `json:"name"`
	Role    string `json:"role,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// SeasonView is one season on a series page, with whether it is held.
type SeasonView struct {
	metadata.SeasonSummary
	// Held counts the season's episodes on disk.
	Held int `json:"held"`
	// Request is the status of an open request naming this season (or the
	// whole series), "" when there is none.
	Request string `json:"request,omitempty"`
}

// Title is a film or series page driven by TMDB.
type Title struct {
	MediaType     string       `json:"mediaType"`
	TmdbID        int          `json:"tmdbId"`
	Title         string       `json:"title"`
	OriginalTitle string       `json:"originalTitle,omitempty"`
	Year          int          `json:"year,omitempty"`
	Overview      string       `json:"overview,omitempty"`
	Tagline       string       `json:"tagline,omitempty"`
	Poster        string       `json:"poster,omitempty"`
	Backdrop      string       `json:"backdrop,omitempty"`
	Genres        []string     `json:"genres"`
	Rating        float64      `json:"rating,omitempty"`
	VoteCount     int          `json:"voteCount,omitempty"`
	RuntimeMins   int          `json:"runtimeMins,omitempty"`
	Certification string       `json:"certification,omitempty"`
	Status        string       `json:"status,omitempty"`
	Network       string       `json:"network,omitempty"`
	Cast          []Credit     `json:"cast"`
	Crew          []Credit     `json:"crew"`
	Seasons       []SeasonView `json:"seasons,omitempty"`
	// ItemID is the library item for the title, when the viewer can see
	// one: the client goes there instead.
	ItemID string `json:"itemId,omitempty"`
	// Requests are the open requests for the title, newest first, and the
	// newest closed one (declined or available) if there are no open ones.
	Requests []store.MediaRequest `json:"requests"`
	// CanRequest says whether this viewer may request it.
	CanRequest bool `json:"canRequest"`
}

// TitleFor builds a title page. region picks the content rating.
func (s *Service) TitleFor(ctx context.Context, st Status, mediaType string, tmdbID int, region string, visible func(string) bool) (Title, error) {
	if s.Discover == nil {
		return Title{}, ErrNoTMDB
	}
	var d metadata.TitleDetail
	var err error
	kind := store.KindMovie
	if mediaType == store.RequestMovie {
		d, err = s.Discover.MovieDetail(ctx, tmdbID)
	} else {
		kind = store.KindSeries
		d, err = s.Discover.TVDetail(ctx, tmdbID)
	}
	if err != nil {
		return Title{}, err
	}
	t := Title{
		MediaType: mediaType, TmdbID: tmdbID, Title: d.Title, OriginalTitle: d.OriginalTitle, Year: d.Year,
		Overview: d.Overview, Tagline: d.Tagline, Poster: d.PosterURL, Backdrop: d.BackdropURL,
		Genres: d.Genres, Rating: d.Rating, VoteCount: d.VoteCount, RuntimeMins: d.RuntimeMins,
		Certification: d.CertificationFor(region), Status: d.Status, Network: d.Network,
		Cast: []Credit{}, Crew: []Credit{}, Requests: []store.MediaRequest{},
	}
	if t.Genres == nil {
		t.Genres = []string{}
	}
	for _, p := range d.Cast {
		t.Cast = append(t.Cast, Credit{TmdbID: p.TmdbID, Name: p.Name, Role: p.Character, Profile: p.ProfileURL})
	}
	for _, p := range d.Crew {
		t.Crew = append(t.Crew, Credit{TmdbID: p.TmdbID, Name: p.Name, Role: p.Job, Profile: p.ProfileURL})
	}
	items, err := s.Store.ItemsByTmdbIDs(ctx, kind, []int{tmdbID})
	if err != nil {
		return Title{}, err
	}
	for _, it := range items {
		if visible(it.LibraryID) {
			t.ItemID = it.ID
			break
		}
	}
	all, err := s.Store.Requests(ctx, store.RequestFilter{MediaType: mediaType, TmdbID: tmdbID, Limit: 50})
	if err != nil {
		return Title{}, err
	}
	for _, r := range all {
		if r.Open() {
			t.Requests = append(t.Requests, r)
		}
	}
	if len(t.Requests) == 0 && len(all) > 0 {
		t.Requests = append(t.Requests, all[0])
	}
	if mediaType == store.RequestTV {
		disk, _, err := s.Store.SeriesOnDisk(ctx, tmdbID)
		if err != nil {
			return Title{}, err
		}
		for _, ss := range d.Seasons {
			v := SeasonView{SeasonSummary: ss, Held: len(disk[ss.Number])}
			for _, r := range t.Requests {
				if r.Open() && (r.WholeSeries() && ss.Number > 0 || slices.Contains(r.Seasons, ss.Number)) {
					v.Request = r.Status
					break
				}
			}
			t.Seasons = append(t.Seasons, v)
		}
	}
	t.CanRequest = st.Enabled && ((mediaType == store.RequestMovie && st.Movies && t.ItemID == "") ||
		(mediaType == store.RequestTV && st.TV))
	return t, nil
}
