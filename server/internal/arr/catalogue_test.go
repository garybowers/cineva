package arr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/garybowers/cineva/server/internal/store"
)

// library is the fake instance's films, series and episodes, and a record
// of what it was asked to do.
type library struct {
	movies   []map[string]any
	series   []map[string]any
	episodes []map[string]any
	queue    []map[string]any
	commands []map[string]any
	added    []map[string]any
	// race answers an add with "already been added", having added it — as
	// when someone adds the title by hand between our look and our add.
	race bool
	// noLanguageProfiles is Sonarr 4, which has none.
	noLanguageProfiles bool
}

func num(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func (l *library) serveLibrary(w http.ResponseWriter, r *http.Request) bool {
	enc := json.NewEncoder(w)
	q := r.URL.Query()
	id := func(prefix string) int {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, prefix))
		return n
	}
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	find := func(list []map[string]any, key string, want int) []map[string]any {
		out := []map[string]any{}
		for _, m := range list {
			if num(m[key]) == want {
				out = append(out, m)
			}
		}
		return out
	}
	add := func(list *[]map[string]any) {
		body["id"] = float64(len(*list) + 1)
		*list = append(*list, body)
		l.added = append(l.added, body)
		if l.race {
			w.WriteHeader(http.StatusBadRequest)
			_ = enc.Encode([]map[string]any{{"propertyName": "Id", "errorMessage": "This title has already been added"}})
			return
		}
		_ = enc.Encode(body)
	}
	replace := func(list []map[string]any, n int) {
		for i, m := range list {
			if num(m["id"]) == n {
				body["id"] = float64(n)
				list[i] = body
			}
		}
		_ = enc.Encode(body)
	}
	switch p := r.URL.Path; {
	case p == "/api/v3/qualityprofile":
		_ = enc.Encode([]map[string]any{{"id": 4, "name": "HD-1080p"}, {"id": 6, "name": "Ultra-HD"}})
	case p == "/api/v3/rootfolder":
		_ = enc.Encode([]map[string]any{{"id": 1, "path": "/media", "freeSpace": 1 << 40, "accessible": true}})
	case p == "/api/v3/health":
		_ = enc.Encode([]map[string]any{{"source": "IndexerStatusCheck", "type": "warning", "message": "Indexers unavailable"}})
	case p == "/api/v3/queue":
		_ = enc.Encode(map[string]any{"records": l.queue})
	case p == "/api/v3/languageprofile":
		if l.noLanguageProfiles {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		_ = enc.Encode([]map[string]any{{"id": 2, "name": "English"}})
	case p == "/api/v3/command":
		l.commands = append(l.commands, body)
		_ = enc.Encode(body)

	case p == "/api/v3/movie" && r.Method == http.MethodGet:
		tmdb, _ := strconv.Atoi(q.Get("tmdbId"))
		_ = enc.Encode(find(l.movies, "tmdbId", tmdb))
	case p == "/api/v3/movie/lookup/tmdb":
		tmdb, _ := strconv.Atoi(q.Get("tmdbId"))
		_ = enc.Encode(map[string]any{"title": "Film " + q.Get("tmdbId"), "tmdbId": tmdb, "year": 2020, "images": []any{}})
	case p == "/api/v3/movie" && r.Method == http.MethodPost:
		add(&l.movies)
	case strings.HasPrefix(p, "/api/v3/movie/") && r.Method == http.MethodPut:
		replace(l.movies, id("/api/v3/movie/"))

	case p == "/api/v3/series" && r.Method == http.MethodGet:
		tvdb, _ := strconv.Atoi(q.Get("tvdbId"))
		_ = enc.Encode(find(l.series, "tvdbId", tvdb))
	case p == "/api/v3/series/lookup":
		tvdb, _ := strconv.Atoi(strings.TrimPrefix(q.Get("term"), "tvdb:"))
		_ = enc.Encode([]map[string]any{{
			"title": "Show", "tvdbId": tvdb,
			"seasons": []any{
				map[string]any{"seasonNumber": 0, "monitored": false},
				map[string]any{"seasonNumber": 1, "monitored": false},
				map[string]any{"seasonNumber": 2, "monitored": false},
			},
		}})
	case p == "/api/v3/series" && r.Method == http.MethodPost:
		add(&l.series)
	case strings.HasPrefix(p, "/api/v3/series/") && r.Method == http.MethodPut:
		replace(l.series, id("/api/v3/series/"))
	case strings.HasPrefix(p, "/api/v3/series/"):
		got := find(l.series, "id", id("/api/v3/series/"))
		if len(got) == 0 {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		_ = enc.Encode(got[0])
	case p == "/api/v3/episode":
		sid, _ := strconv.Atoi(q.Get("seriesId"))
		_ = enc.Encode(find(l.episodes, "seriesId", sid))
	case p == "/api/v3/episode/monitor":
		for _, raw := range body["episodeIds"].([]any) {
			for _, e := range l.episodes {
				if num(e["id"]) == num(raw) {
					e["monitored"] = body["monitored"]
				}
			}
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		return false
	}
	return true
}

func newFake(t *testing.T, app string) (*fakeArr, *Client) {
	t.Helper()
	fake := &fakeArr{app: app, saved: map[int]map[string]any{}}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return fake, New(strings.ToLower(app), srv.URL, "key")
}

func commandNames(cmds []map[string]any) string {
	var names []string
	for _, c := range cmds {
		names = append(names, c["name"].(string))
	}
	return strings.Join(names, ",")
}

func TestInstanceOptions(t *testing.T) {
	ctx := context.Background()
	_, c := newFake(t, "Radarr")
	profiles, err := c.QualityProfiles(ctx)
	if err != nil || len(profiles) != 2 || profiles[0].Name != "HD-1080p" {
		t.Fatalf("profiles = %+v, %v", profiles, err)
	}
	roots, err := c.RootFolders(ctx)
	if err != nil || len(roots) != 1 || roots[0].Path != "/media" {
		t.Fatalf("roots = %+v, %v", roots, err)
	}
	health, err := c.Health(ctx)
	if err != nil || len(health) != 1 || health[0].Type != "warning" {
		t.Fatalf("health = %+v, %v", health, err)
	}
	if q, err := c.Queue(ctx); err != nil || len(q) != 0 {
		t.Fatalf("empty queue = %+v, %v", q, err)
	}
}

func TestRequestMovie(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		have     map[string]any // already in Radarr
		race     bool
		opts     AddOptions
		wantAdd  bool
		wantCmds string
		check    func(t *testing.T, f *fakeArr)
	}{
		{
			name: "new film, instance defaults", wantAdd: true,
			check: func(t *testing.T, f *fakeArr) {
				m := f.added[0]
				if num(m["qualityProfileId"]) != 4 || m["rootFolderPath"] != "/media" || m["monitored"] != true ||
					m["addOptions"].(map[string]any)["searchForMovie"] != true || m["minimumAvailability"] != "released" {
					t.Fatalf("added = %v", m)
				}
			},
		},
		{
			name: "new film, chosen profile and folder", opts: AddOptions{QualityProfileID: 6, RootFolder: "/films-4k"}, wantAdd: true,
			check: func(t *testing.T, f *fakeArr) {
				if m := f.added[0]; num(m["qualityProfileId"]) != 6 || m["rootFolderPath"] != "/films-4k" {
					t.Fatalf("added = %v", m)
				}
			},
		},
		{
			name: "there, unmonitored, no file", have: map[string]any{"id": 9.0, "tmdbId": 603.0, "monitored": false, "hasFile": false, "path": "/media/x"},
			wantCmds: "MoviesSearch",
			check: func(t *testing.T, f *fakeArr) {
				if f.movies[0]["monitored"] != true || f.movies[0]["path"] != "/media/x" {
					t.Fatalf("movie after = %v (fields must survive the update)", f.movies[0])
				}
			},
		},
		{name: "there with a file", have: map[string]any{"id": 9.0, "tmdbId": 603.0, "monitored": true, "hasFile": true}},
		{name: "added meanwhile", race: true, wantAdd: true, wantCmds: "MoviesSearch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newFake(t, "Radarr")
			f.race = tc.race
			if tc.have != nil {
				f.movies = append(f.movies, tc.have)
			}
			id, err := c.RequestMovie(ctx, 603, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if id == 0 {
				t.Fatal("no Radarr id returned")
			}
			if (len(f.added) > 0) != tc.wantAdd {
				t.Fatalf("added = %v, want add %v", f.added, tc.wantAdd)
			}
			if got := commandNames(f.commands); got != tc.wantCmds {
				t.Fatalf("commands = %q, want %q", got, tc.wantCmds)
			}
			if tc.check != nil {
				tc.check(t, f)
			}
		})
	}
}

func TestRequestSeries(t *testing.T) {
	ctx := context.Background()
	monitored := func(series map[string]any) string {
		var on []string
		for _, s := range series["seasons"].([]any) {
			season := s.(map[string]any)
			if season["monitored"] == true {
				on = append(on, strconv.Itoa(num(season["seasonNumber"])))
			}
		}
		return strings.Join(on, ",")
	}
	held := func() map[string]any {
		return map[string]any{
			"id": 5.0, "tvdbId": 81189.0, "monitored": false, "title": "Show", "path": "/tv/Show",
			"seasons": []any{
				map[string]any{"seasonNumber": 0.0, "monitored": false},
				map[string]any{"seasonNumber": 1.0, "monitored": true},
				map[string]any{"seasonNumber": 2.0, "monitored": false},
			},
		}
	}
	for _, tc := range []struct {
		name      string
		have      bool
		v4        bool
		scope     SeriesScope
		wantAdd   bool
		wantSeas  string // seasons monitored afterwards
		wantCmds  string
		wantLang  int
		wantNewIt string
	}{
		{name: "new, whole series", scope: SeriesScope{}, wantAdd: true, wantSeas: "1,2", wantLang: 2, wantNewIt: "all"},
		{name: "new, one season, Sonarr 4", v4: true, scope: SeriesScope{Seasons: []int{2}}, wantAdd: true, wantSeas: "2", wantNewIt: "none"},
		{name: "new, single episodes widen to their season", scope: SeriesScope{Episodes: []store.EpisodeRef{{Season: 1, Episode: 3}}}, wantAdd: true, wantSeas: "1", wantLang: 2, wantNewIt: "none"},
		{name: "held, whole series", have: true, wantSeas: "1,2", wantCmds: "SeriesSearch"},
		{name: "held, a season", have: true, scope: SeriesScope{Seasons: []int{2}}, wantSeas: "1,2", wantCmds: "SeasonSearch"},
		{name: "held, an episode", have: true, scope: SeriesScope{Episodes: []store.EpisodeRef{{Season: 2, Episode: 1}}}, wantSeas: "1", wantCmds: "EpisodeSearch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newFake(t, "Sonarr")
			f.noLanguageProfiles = tc.v4
			if tc.have {
				f.series = append(f.series, held())
				f.episodes = append(f.episodes,
					map[string]any{"id": 51.0, "seriesId": 5.0, "seasonNumber": 2.0, "episodeNumber": 1.0, "monitored": false},
					map[string]any{"id": 52.0, "seriesId": 5.0, "seasonNumber": 2.0, "episodeNumber": 2.0, "monitored": false})
			}
			id, err := c.RequestSeries(ctx, 81189, AddOptions{QualityProfileID: 4, RootFolder: "/tv"}, tc.scope)
			if err != nil {
				t.Fatal(err)
			}
			if id == 0 {
				t.Fatal("no Sonarr id returned")
			}
			if (len(f.added) > 0) != tc.wantAdd {
				t.Fatalf("added = %v, want add %v", f.added, tc.wantAdd)
			}
			series := f.series[0]
			if got := monitored(series); got != tc.wantSeas {
				t.Fatalf("monitored seasons = %s, want %s", got, tc.wantSeas)
			}
			if series["monitored"] != true || (tc.have && series["path"] != "/tv/Show") {
				t.Fatalf("series = %v", series)
			}
			if got := commandNames(f.commands); got != tc.wantCmds {
				t.Fatalf("commands = %q, want %q", got, tc.wantCmds)
			}
			if tc.wantAdd {
				a := f.added[0]
				if num(a["languageProfileId"]) != tc.wantLang || a["monitorNewItems"] != tc.wantNewIt ||
					a["seasonFolder"] != true || num(a["qualityProfileId"]) != 4 {
					t.Fatalf("added = %v", a)
				}
				opts := a["addOptions"].(map[string]any)
				if opts["searchForMissingEpisodes"] != true || opts["monitor"] != nil {
					t.Fatalf("add options = %v", opts)
				}
			}
			if strings.Contains(tc.wantCmds, "EpisodeSearch") {
				cmd := f.commands[0]
				if ids := cmd["episodeIds"].([]any); len(ids) != 1 || num(ids[0]) != 51 || f.episodes[0]["monitored"] != true {
					t.Fatalf("episode search = %v, episodes = %v", cmd, f.episodes)
				}
			}
		})
	}

	t.Run("episodes Sonarr does not list", func(t *testing.T) {
		f, c := newFake(t, "Sonarr")
		f.series = append(f.series, held())
		if err := c.SearchEpisodes(ctx, 5, []store.EpisodeRef{{Season: 9, Episode: 9}}); err == nil {
			t.Fatal("searching for unknown episodes succeeded")
		}
	})
	t.Run("search seasons", func(t *testing.T) {
		f, c := newFake(t, "Sonarr")
		f.series = append(f.series, held())
		if err := c.SearchSeasons(ctx, 5, []int{2}); err != nil {
			t.Fatal(err)
		}
		if got := monitored(f.series[0]); got != "1,2" || commandNames(f.commands) != "SeasonSearch" {
			t.Fatalf("monitored %s, commands %s", got, commandNames(f.commands))
		}
	})
	t.Run("added meanwhile", func(t *testing.T) {
		f, c := newFake(t, "Sonarr")
		f.race = true
		if _, err := c.RequestSeries(ctx, 81189, AddOptions{QualityProfileID: 4, RootFolder: "/tv"}, SeriesScope{}); err != nil {
			t.Fatal(err)
		}
		if commandNames(f.commands) != "SeriesSearch" {
			t.Fatalf("commands = %s", commandNames(f.commands))
		}
	})
}

func TestMapPaths(t *testing.T) {
	maps := []store.PathMap{
		{From: "/tv", To: "/mnt/media/TV"},
		{From: "/tv/kids", To: "/mnt/media/Kids TV"},
		{From: "", To: "/nowhere"},
	}
	for _, tc := range []struct{ in, want string }{
		{"/tv/Show/S01/e1.mkv", "/mnt/media/TV/Show/S01/e1.mkv"},
		{"/tv", "/mnt/media/TV"},
		{"/tv/kids/Bluey", "/mnt/media/Kids TV/Bluey"}, // longest prefix wins
		{"/tvshows/x", "/tvshows/x"},                   // a prefix is not containment
		{"/mnt/media/TV/Show", "/mnt/media/TV/Show"},
	} {
		if got := MapPaths(maps, []string{tc.in}); got[0] != tc.want {
			t.Errorf("MapPaths(%s) = %s, want %s", tc.in, got[0], tc.want)
		}
	}
}

func TestSuggestPathMaps(t *testing.T) {
	libs := []store.Library{
		{ID: "tv", MediaType: store.MediaTV, Source: "local", Paths: []string{"/mnt/media/TV"}},
		{ID: "kids", MediaType: store.MediaTV, Source: "local", Paths: []string{"/mnt/media/Kids"}},
		{ID: "films", MediaType: store.MediaMovies, Source: "local", Paths: []string{"/mnt/media/Movies"}},
	}
	str := func(maps []store.PathMap) string {
		var out []string
		for _, m := range maps {
			out = append(out, m.From+"="+m.To)
		}
		return strings.Join(out, ";")
	}
	for _, tc := range []struct {
		name  string
		kind  string
		roots []string
		want  string
	}{
		{"same name, other case", "sonarr", []string{"/tv", "/kids"}, "/tv=/mnt/media/TV;/kids=/mnt/media/Kids"},
		{"already Cineva's path", "sonarr", []string{"/mnt/media/TV"}, ""},
		{"inside a library folder", "sonarr", []string{"/mnt/media/TV/new"}, ""},
		{"one root, one folder", "radarr", []string{"/films"}, "/films=/mnt/media/Movies"},
		{"no way to tell", "sonarr", []string{"/data/shows"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := str(SuggestPathMaps(tc.kind, tc.roots, libs)); got != tc.want {
				t.Fatalf("suggested %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEventDetails(t *testing.T) {
	for _, tc := range []struct {
		name, body, describe string
		grab                 bool
		arrID, tmdb, tvdb    int
	}{
		{"sonarr grab", `{"eventType":"Grab","series":{"id":5,"title":"Show","tvdbId":81189,"year":2008},"episodes":[{"seasonNumber":2,"episodeNumber":4}]}`,
			"Show S02E04", true, 5, 0, 81189},
		{"season pack", `{"eventType":"Download","series":{"id":5,"title":"Show"},"episodes":[{"seasonNumber":2,"episodeNumber":1},{"seasonNumber":2,"episodeNumber":3}]}`,
			"Show S02E01-E03", false, 5, 0, 0},
		{"radarr grab", `{"eventType":"Grab","movie":{"id":9,"title":"Film","year":2020,"tmdbId":603}}`,
			"Film (2020)", true, 9, 603, 0},
		{"test", `{"eventType":"Test"}`, "", false, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := ParseEvent([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if ev.Describe() != tc.describe || ev.Grab() != tc.grab || ev.ArrID != tc.arrID || ev.TmdbID != tc.tmdb || ev.TvdbID != tc.tvdb {
				t.Fatalf("event = %+v, describe %q", ev, ev.Describe())
			}
		})
	}
}
