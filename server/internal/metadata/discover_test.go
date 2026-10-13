package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDiscovery(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/3/search/multi":
			if r.URL.Query().Get("query") != "matrix" {
				_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
				{"media_type": "movie", "id": 603, "title": "The Matrix", "release_date": "1999-03-31", "poster_path": "/m.jpg"},
				{"media_type": "person", "id": 6384, "name": "Keanu Reeves"},
				{"media_type": "tv", "id": 9999, "name": "The Matrix Show", "first_air_date": "2001-01-01"},
			}})
		case "/3/movie/603":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 603, "title": "The Matrix", "release_date": "1999-03-31", "runtime": 136,
				"credits": map[string]any{"cast": []map[string]any{{"id": 6384, "name": "Keanu Reeves", "character": "Neo"}}},
			})
		case "/3/tv/1396":
			if !strings.Contains(r.URL.Query().Get("append_to_response"), "external_ids") {
				t.Errorf("series detail without external ids: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 1396, "name": "Breaking Bad", "first_air_date": "2008-01-20", "number_of_seasons": 2,
				"seasons": []map[string]any{
					{"season_number": 0, "name": "Specials", "episode_count": 3},
					{"season_number": 1, "name": "Season 1", "episode_count": 7, "air_date": "2008-01-20", "poster_path": "/s1.jpg"},
				},
				"external_ids": map[string]any{"tvdb_id": 81189},
			})
		case "/3/tv/1396/season/1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 3572, "episodes": []map[string]any{{"episode_number": 1, "name": "Pilot"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	d := NewDiscovery(&TMDB{APIKey: "k", Client: &http.Client{Transport: redirectTo{srv.URL}}})
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	ctx := context.Background()

	got, err := d.SearchTitles(ctx, "matrix")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].MediaType != "movie" || got[0].Year != 1999 || got[0].PosterURL == "" ||
		got[1].MediaType != "tv" || got[1].Title != "The Matrix Show" {
		t.Fatalf("search = %+v (people should be left out)", got)
	}
	if empty, err := d.SearchTitles(ctx, "  "); err != nil || len(empty) != 0 {
		t.Fatalf("blank search = %v, %v", empty, err)
	}

	film, err := d.MovieDetail(ctx, 603)
	if err != nil || film.MediaType != "movie" || film.RuntimeMins != 136 || len(film.Cast) != 1 {
		t.Fatalf("movie detail = %+v, %v", film, err)
	}
	show, err := d.TVDetail(ctx, 1396)
	if err != nil {
		t.Fatal(err)
	}
	if show.TvdbID != 81189 || len(show.Seasons) != 2 || show.Seasons[1].EpisodeCount != 7 || show.Seasons[1].PosterURL == "" {
		t.Fatalf("tv detail = %+v", show)
	}
	if season, err := d.TVSeason(ctx, 1396, 1); err != nil || len(season.Episodes) != 1 {
		t.Fatalf("season = %+v, %v", season, err)
	}
	if _, err := d.TVDetail(ctx, 42); err == nil {
		t.Fatal("a missing series was found")
	}

	// Asked again: answered from the cache...
	for i := 0; i < 3; i++ {
		if _, err := d.SearchTitles(ctx, "Matrix"); err != nil {
			t.Fatal(err)
		}
		if _, err := d.TVDetail(ctx, 1396); err != nil {
			t.Fatal(err)
		}
	}
	if calls["/3/search/multi"] != 1 || calls["/3/tv/1396"] != 1 {
		t.Fatalf("calls = %v; want one per distinct question (case does not matter)", calls)
	}
	// ...until it goes stale. Failures are never cached.
	now = now.Add(searchTTL + time.Second)
	if _, err := d.SearchTitles(ctx, "matrix"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TVDetail(ctx, 42); err == nil {
		t.Fatal("a missing series was found")
	}
	if calls["/3/search/multi"] != 2 || calls["/3/tv/1396"] != 1 || calls["/3/tv/42"] != 2 {
		t.Fatalf("calls after expiry = %v", calls)
	}
}

func TestDiscoveryCacheIsBounded(t *testing.T) {
	d := NewDiscovery(&TMDB{})
	for i := 0; i < cacheMax+50; i++ {
		key := "k" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)
		if _, err := cached(d, key, time.Hour, func() (int, error) { return i, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(d.cache); n > cacheMax {
		t.Fatalf("cache holds %d entries, want at most %d", n, cacheMax)
	}
}
