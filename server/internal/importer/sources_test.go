package importer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveJSON(t *testing.T, check func(r *http.Request) bool, routes map[string]func(r *http.Request) any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !check(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		for prefix, fn := range routes {
			if r.URL.Path == prefix || (prefix != "/" && strings.HasSuffix(prefix, "/") && strings.HasPrefix(r.URL.Path, prefix)) {
				_ = json.NewEncoder(w).Encode(fn(r))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestJellyfin(t *testing.T) {
	srv := serveJSON(t, func(r *http.Request) bool {
		return strings.Contains(r.Header.Get("Authorization"), `Token="key"`)
	}, map[string]func(r *http.Request) any{
		"/System/Info": func(*http.Request) any { return map[string]any{"ServerName": "Old box", "Version": "10.10.3"} },
		"/Users": func(*http.Request) any {
			return []any{map[string]any{"Id": "u1", "Name": "Gary", "Policy": map[string]any{"IsAdministrator": true}}}
		},
		"/Items": func(r *http.Request) any {
			q := r.URL.Query()
			switch {
			case q.Get("Ids") != "":
				return map[string]any{"Items": []any{map[string]any{"Id": "s1", "Type": "Series", "ProviderIds": map[string]any{"Tmdb": "200"}}}, "TotalRecordCount": 1}
			case q.Get("Filters") == "IsPlayed":
				return map[string]any{"TotalRecordCount": 1, "Items": []any{map[string]any{
					"Id": "m1", "Name": "Paddington", "Type": "Movie", "ProductionYear": 2014, "ProviderIds": map[string]any{"Tmdb": "100"},
					"Path":     "/media/Films/Paddington (2014)/Paddington.mkv",
					"UserData": map[string]any{"Played": true, "LastPlayedDate": "2025-12-01T20:00:00.0000000Z"}}}}
			case q.Get("Filters") == "IsResumable":
				return map[string]any{"TotalRecordCount": 1, "Items": []any{map[string]any{
					"Id": "e1", "Name": "Episode 2", "Type": "Episode", "SeriesId": "s1", "SeriesName": "Slow Horses",
					"ParentIndexNumber": 1, "IndexNumber": 2,
					"UserData": map[string]any{"PlaybackPositionTicks": 6_000_000_000, "LastPlayedDate": "2026-01-02T10:00:00Z"}}}}
			case q.Get("IncludeItemTypes") == "Playlist":
				return map[string]any{"TotalRecordCount": 1, "Items": []any{map[string]any{"Id": "p1", "Name": "Road trip", "Type": "Playlist"}}}
			case q.Get("Filters") == "IsFavorite":
				return map[string]any{"TotalRecordCount": 1, "Items": []any{map[string]any{"Id": "t2", "Name": "Grounds for Divorce", "Type": "Audio", "Album": "The Seldom Seen Kid", "AlbumArtist": "Elbow"}}}
			}
			return map[string]any{"Items": []any{}}
		},
		"/Playlists/": func(*http.Request) any {
			return map[string]any{"Items": []any{
				map[string]any{"Id": "t1", "Name": "One Day Like This", "Type": "Audio", "Album": "The Seldom Seen Kid", "AlbumArtist": "Elbow", "Path": "/music/Elbow/The Seldom Seen Kid/11.flac"},
				map[string]any{"Id": "v1", "Name": "A video", "Type": "Video"},
			}}
		},
	})
	ctx := context.Background()
	src, _ := New("jellyfin", srv.URL+"/", "key")
	info, err := src.Check(ctx)
	if err != nil || info.Name != "Old box" || len(info.Users) != 1 || !info.Users[0].Admin {
		t.Fatalf("check = %+v, %v", info, err)
	}
	ws, err := src.Watches(ctx, info.Users[0])
	if err != nil || len(ws) != 2 {
		t.Fatalf("watches = %+v, %v", ws, err)
	}
	film, ep := ws[0], ws[1]
	if !film.Movie || film.TmdbID != 100 || !film.Watched || film.At.Year() != 2025 {
		t.Errorf("film = %+v", film)
	}
	if ep.Movie || ep.SeriesTmdbID != 200 || ep.Season != 1 || ep.Episode != 2 || ep.PositionSecs != 600 || ep.Watched {
		t.Errorf("episode = %+v", ep)
	}
	lists, err := src.Playlists(ctx, info.Users[0])
	if err != nil || len(lists) != 2 || lists[0].Name != "Road trip" || len(lists[0].Tracks) != 1 || lists[1].Name != "Favourites" {
		t.Fatalf("playlists = %+v, %v", lists, err)
	}
	bad, _ := New("jellyfin", srv.URL, "wrong")
	if _, err := bad.Check(ctx); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestPlex(t *testing.T) {
	meta := func(items ...map[string]any) any {
		return map[string]any{"MediaContainer": map[string]any{"Metadata": items}}
	}
	srv := serveJSON(t, func(r *http.Request) bool { return r.Header.Get("X-Plex-Token") == "tok" }, map[string]func(r *http.Request) any{
		"/": func(*http.Request) any {
			return map[string]any{"MediaContainer": map[string]any{"friendlyName": "Plex box", "version": "1.41"}}
		},
		"/myplex/account": func(*http.Request) any { return map[string]any{"MyPlex": map[string]any{"username": "gary"}} },
		"/accounts": func(*http.Request) any {
			return map[string]any{"MediaContainer": map[string]any{"Account": []any{
				map[string]any{"id": 0, "name": ""}, map[string]any{"id": 1, "name": "gary"}, map[string]any{"id": 7, "name": "Kid"}}}}
		},
		"/library/sections": func(*http.Request) any {
			return map[string]any{"MediaContainer": map[string]any{"Directory": []any{
				map[string]any{"key": "1", "type": "movie"}, map[string]any{"key": "2", "type": "show"}, map[string]any{"key": "3", "type": "artist"}}}}
		},
		"/library/sections/1/all": func(*http.Request) any {
			return meta(map[string]any{"ratingKey": "10", "type": "movie", "title": "Paddington", "year": 2014, "viewCount": 0, "viewOffset": 1_200_000,
				"lastViewedAt": 1767225600, "Guid": []any{map[string]any{"id": "tmdb://100"}}},
				map[string]any{"ratingKey": "11", "type": "movie", "title": "Unwatched"})
		},
		"/library/sections/2/all": func(*http.Request) any {
			return meta(map[string]any{"ratingKey": "20", "type": "episode", "title": "Ep", "grandparentRatingKey": "30", "parentIndex": 1, "index": 3, "viewCount": 2})
		},
		"/library/metadata/": func(r *http.Request) any {
			var out []map[string]any
			for _, k := range strings.Split(strings.TrimPrefix(r.URL.Path, "/library/metadata/"), ",") {
				switch k {
				case "30":
					out = append(out, map[string]any{"ratingKey": "30", "type": "show", "title": "Slow Horses", "Guid": []any{map[string]any{"id": "tmdb://200"}}})
				case "10":
					out = append(out, map[string]any{"ratingKey": "10", "type": "movie", "title": "Paddington", "Guid": []any{map[string]any{"id": "tmdb://100"}},
						"Media": []any{map[string]any{"Part": []any{map[string]any{"file": "/data/movies/Paddington/Paddington.mkv"}}}}})
				}
			}
			return map[string]any{"MediaContainer": map[string]any{"Metadata": out}}
		},
		"/status/sessions/history/all": func(r *http.Request) any {
			if r.URL.Query().Get("accountID") != "7" {
				return meta()
			}
			return meta(map[string]any{"ratingKey": "10", "type": "movie", "viewedAt": 1767225600, "accountID": 7},
				map[string]any{"ratingKey": "10", "type": "movie", "viewedAt": 1767000000, "accountID": 7})
		},
		"/playlists": func(*http.Request) any { return meta(map[string]any{"ratingKey": "9", "title": "Running"}) },
		"/playlists/9/items": func(*http.Request) any {
			return meta(map[string]any{"type": "track", "title": "One Day Like This", "grandparentTitle": "Elbow", "parentTitle": "The Seldom Seen Kid"})
		},
	})
	ctx := context.Background()
	src, _ := New("plex", srv.URL, "tok")
	info, err := src.Check(ctx)
	if err != nil || info.Name != "Plex box" || len(info.Users) != 2 || !info.Users[0].Admin || len(info.Caveats) != 1 {
		t.Fatalf("check = %+v, %v", info, err)
	}
	// The owner: positions from the library.
	ws, err := src.Watches(ctx, info.Users[0])
	if err != nil || len(ws) != 2 {
		t.Fatalf("owner watches = %+v, %v", ws, err)
	}
	if ws[0].TmdbID != 100 || ws[0].PositionSecs != 1200 || ws[0].Watched {
		t.Errorf("owner's film = %+v", ws[0])
	}
	if ws[1].SeriesTmdbID != 200 || ws[1].SeriesTitle != "Slow Horses" || ws[1].Episode != 3 || !ws[1].Watched {
		t.Errorf("owner's episode = %+v", ws[1])
	}
	// Another account: watched, from history, once, at the latest view.
	ws, err = src.Watches(ctx, info.Users[1])
	if err != nil || len(ws) != 1 || !ws[0].Watched || ws[0].At.Unix() != 1767225600 || ws[0].Path == "" {
		t.Fatalf("kid's watches = %+v, %v", ws, err)
	}
	lists, err := src.Playlists(ctx, info.Users[0])
	if err != nil || len(lists) != 1 || lists[0].Tracks[0].Artist != "Elbow" {
		t.Fatalf("playlists = %+v, %v", lists, err)
	}
	if lists, _ := src.Playlists(ctx, info.Users[1]); len(lists) != 0 {
		t.Fatalf("another account's playlists = %+v, want none", lists)
	}
}
