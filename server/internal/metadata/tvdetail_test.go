package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// redirectTo sends every request to a test server instead of TMDB, without
// the production code needing a seam it would otherwise have no use for.
type redirectTo struct{ host string }

func (r redirectTo) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(r.host)
	if err != nil {
		return nil, err
	}
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(req)
}

// A TMDB search hit carries genre *ids* and nothing else of substance, so
// SearchTV has to follow it with a detail call the way SearchMovie does.
// Without that, series had no genres, no cast, no tagline and no age rating
// while films had all four.
func TestSearchTVFetchesFullDetail(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/3/search/tv"):
			// What a search really returns: no genres, no cast, no tagline.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": 1396, "name": "Breaking Bad", "first_air_date": "2008-01-20", "overview": "x"},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/3/tv/1396"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 1396, "name": "Breaking Bad", "first_air_date": "2008-01-20",
				"tagline":          "All Hail the King",
				"episode_run_time": []int{45},
				"genres":           []map[string]any{{"name": "Drama"}, {"name": "Crime"}},
				"credits": map[string]any{
					"cast": []map[string]any{{"id": 17419, "name": "Bryan Cranston", "character": "Walter White", "order": 0}},
				},
				"content_ratings": map[string]any{
					"results": []map[string]any{{"iso_3166_1": "GB", "rating": "18"}, {"iso_3166_1": "US", "rating": "TV-MA"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tmdb := &TMDB{APIKey: "k", Client: &http.Client{Transport: redirectTo{srv.URL}}}
	got, err := tmdb.SearchTV(context.Background(), "Breaking Bad", 2008)
	if err != nil {
		t.Fatalf("SearchTV: %v", err)
	}

	if len(paths) < 2 || !strings.HasPrefix(paths[1], "/3/tv/1396") {
		t.Fatalf("expected a detail call after the search, got %v", paths)
	}
	if len(got.Genres) != 2 || got.Genres[0] != "Drama" {
		t.Errorf("genres = %v, want [Drama Crime]", got.Genres)
	}
	if len(got.Cast) != 1 || got.Cast[0].Name != "Bryan Cranston" {
		t.Errorf("cast = %+v, want Bryan Cranston", got.Cast)
	}
	if got.Tagline != "All Hail the King" {
		t.Errorf("tagline = %q", got.Tagline)
	}
	// A series reports runtime per episode, not for the whole run.
	if got.RuntimeMins != 45 {
		t.Errorf("runtime = %d, want 45 from episode_run_time", got.RuntimeMins)
	}
	// Television ratings arrive under content_ratings, not release_dates.
	if got.CertificationFor("GB") != "18" {
		t.Errorf("GB certification = %q, want 18", got.CertificationFor("GB"))
	}
	if got.CertificationFor("US") != "TV-MA" {
		t.Errorf("US certification = %q, want TV-MA", got.CertificationFor("US"))
	}
}
