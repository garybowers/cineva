package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/homeassistant"
	"github.com/garybowers/cineva/server/internal/jellyfin"
	"github.com/garybowers/cineva/server/internal/playback"
	"github.com/garybowers/cineva/server/internal/store"
)

// jfFixture is a server with a film, a series and an album, two users
// (one who cannot see the hidden library), mounted on the Jellyfin routes.
type jfFixture struct {
	srv      *server
	e        *echo.Echo
	st       *store.Store
	films    store.Library
	tv       store.Library
	music    store.Library
	hidden   store.Library
	film     store.Item
	series   store.Item
	season   store.Item
	episodes []store.Item
	album    store.Item
	tracks   []store.Item
	secret   store.Item
	admin    store.User
	kid      store.User
}

func newJFFixture(t *testing.T) *jfFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f := &jfFixture{st: st}
	mk := func(name string, typ store.MediaType, path string) store.Library {
		l, err := st.CreateLibrary(ctx, name, typ, []string{path})
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	f.films, f.tv, f.music = mk("Films", store.MediaMovies, "/films"), mk("TV", store.MediaTV, "/tv"), mk("Music", store.MediaMusic, "/music")
	f.hidden = mk("Grown-up films", store.MediaMovies, "/grown")
	save := func(it store.Item) store.Item {
		t.Helper()
		saved, err := st.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	f.film = save(store.Item{LibraryID: f.films.ID, Kind: store.KindMovie, Title: "Heat", Year: 1995, Path: "/films/heat.mkv",
		Container: "mkv", VideoCodec: "h264", AudioCodec: "ac3", RuntimeSecs: 10200, SizeBytes: 10_000_000, Poster: "/artwork/heat.jpg", TmdbID: 949})
	save(store.Item{LibraryID: f.films.ID, Kind: store.KindMovie, Title: "The Avengers", Year: 2012, Path: "/films/avengers.mp4", Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", RuntimeSecs: 8000})
	f.secret = save(store.Item{LibraryID: f.hidden.ID, Kind: store.KindMovie, Title: "Secret", Path: "/grown/secret.mkv"})
	f.series = save(store.Item{LibraryID: f.tv.ID, Kind: store.KindSeries, Title: "Succession", Year: 2018, Path: "/tv/Succession", SeasonCount: 1, EpisodeCount: 2})
	f.season = save(store.Item{LibraryID: f.tv.ID, Kind: store.KindSeason, ParentID: f.series.ID, Title: "Season 1", SeasonNum: 1, Path: "/tv/Succession/S01"})
	for i, title := range []string{"Celebration", "Lifeboats"} {
		f.episodes = append(f.episodes, save(store.Item{LibraryID: f.tv.ID, Kind: store.KindEpisode, ParentID: f.season.ID, Title: title,
			SeasonNum: 1, EpisodeNum: i + 1, Path: "/tv/Succession/S01/e" + title + ".mkv", RuntimeSecs: 3600, AirDate: "2018-06-0" + string(rune('3'+i))}))
	}
	artist := save(store.Item{LibraryID: f.music.ID, Kind: store.KindArtist, Title: "ABBA", Artist: "ABBA", Path: "artist://abba"})
	var saves []store.TrackSave
	for i, title := range []string{"Dancing Queen", "Fernando"} {
		saves = append(saves, store.TrackSave{Item: store.Item{LibraryID: f.music.ID, Kind: store.KindTrack, Title: title, Artist: "ABBA", TrackNum: i + 1,
			Path: "/music/ABBA/Arrival/" + title + ".flac", RuntimeSecs: 230}})
	}
	f.album, f.tracks, err = st.SaveAlbum(ctx, store.Item{LibraryID: f.music.ID, Kind: store.KindAlbum, ParentID: artist.ID, Title: "Arrival", Artist: "ABBA", Year: 1976, Path: "/music/ABBA/Arrival"}, saves)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("Correct-horse-1")
	if err != nil {
		t.Fatal(err)
	}
	f.admin, err = st.CreateUser(ctx, "gary", "Gary", hash, store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	f.kid, err = st.CreateUser(ctx, "kid", "Kid", hash, store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryAccess(ctx, f.hidden.ID, store.LibraryAccess{Restricted: true, UserIDs: []string{f.admin.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetWatched(ctx, f.kid.ID, f.episodes[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetProgress(ctx, f.kid.ID, f.film.ID, 600, f.film.RuntimeSecs); err != nil {
		t.Fatal(err)
	}
	s := &server{store: st, events: events.NewBus(), presence: newPresence(), ha: homeassistant.New(st),
		playbacks: playback.NewRegistry(time.Minute), throttle: auth.NewThrottle(), sessionTTL: 30 * 24 * time.Hour}
	f.e = echo.New()
	s.jellyfinRoutes(f.e)
	f.srv = s
	return f
}

// call sends a request through the routes, with a token when given.
func (f *jfFixture) call(t *testing.T, method, path, token, body string) (int, string) {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	h := `MediaBrowser Client="Test", Device="Bench", DeviceId="dev-1", Version="1"`
	if token != "" {
		h += `, Token="` + token + `"`
	}
	req.Header.Set("Authorization", h)
	rec := httptest.NewRecorder()
	f.e.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func (f *jfFixture) signIn(t *testing.T, username, password string) (int, jellyfin.AuthenticationResult) {
	t.Helper()
	code, body := f.call(t, http.MethodPost, "/Users/AuthenticateByName", "", `{"Username":"`+username+`","Pw":"`+password+`"}`)
	var res jellyfin.AuthenticationResult
	if code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &res); err != nil {
			t.Fatalf("auth body: %v %s", err, body)
		}
	}
	return code, res
}

func decode[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	return v
}

func TestJellyfinSignIn(t *testing.T) {
	f := newJFFixture(t)
	ctx := context.Background()

	if code, body := f.call(t, http.MethodGet, "/System/Info/Public", "", ""); code != 200 || !strings.Contains(body, `"ProductName":"Cineva"`) {
		t.Errorf("public info: %d %s", code, body)
	}
	if code, _ := f.call(t, http.MethodGet, "/Users/Me", "", ""); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
	if code, _ := f.signIn(t, "gary", "wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", code)
	}
	code, res := f.signIn(t, "gary", "Correct-horse-1")
	if code != 200 || res.AccessToken == "" || res.User.Name != "Gary" || !res.User.Policy.IsAdministrator || res.ServerID == "" {
		t.Fatalf("sign in: %d %+v", code, res)
	}
	// The token is a Cineva session on a device named by the client, and
	// the same client signs in on the same device again.
	sessions, err := f.st.UserSessions(ctx, f.admin.ID)
	if err != nil || len(sessions) != 1 || sessions[0].Name != "Bench" || !strings.HasPrefix(sessions[0].Client, "Test 1") {
		t.Fatalf("sessions: %v %+v", err, sessions)
	}
	if _, again := f.signIn(t, "gary", "Correct-horse-1"); again.SessionInfo.ID != jellyfin.ID(sessions[0].ID) {
		t.Errorf("second sign-in made another device: %s vs %s", again.SessionInfo.ID, sessions[0].ID)
	}
	_, res = f.signIn(t, "gary", "Correct-horse-1")
	if code, body := f.call(t, http.MethodGet, "/Users/Me", res.AccessToken, ""); code != 200 || !strings.Contains(body, `"Name":"Gary"`) {
		t.Errorf("me: %d %s", code, body)
	}
	// An API key made for apps works as the password; a plain key does
	// not.
	appKey := auth.APIKeyPrefix + "appkeytoken"
	sk, err := f.st.SecretsKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := auth.Seal(sk, appKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateAPIKey(ctx, f.admin.ID, "tv app", auth.HashToken(appKey), sealed); err != nil {
		t.Fatal(err)
	}
	// Signing in with the key takes the device's session, as any sign-in
	// on the device does, so the token from before is renewed afterwards.
	if code, keyed := f.signIn(t, "gary", appKey); code != 200 || keyed.User.Name != "Gary" {
		t.Errorf("key as password: %d %+v", code, keyed)
	}
	if code, _ := f.signIn(t, "kid", appKey); code != http.StatusUnauthorized {
		t.Errorf("another user's key: %d", code)
	}
	_, res = f.signIn(t, "gary", "Correct-horse-1")
	// A disabled user is refused; so is a token after logout.
	if err := f.st.UpdateUser(ctx, f.kid.ID, "Kid", store.RoleUser, true); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.signIn(t, "kid", "Correct-horse-1"); code != http.StatusUnauthorized {
		t.Errorf("disabled: %d", code)
	}
	if err := f.st.UpdateUser(ctx, f.kid.ID, "Kid", store.RoleUser, false); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.call(t, http.MethodPost, "/Sessions/Logout", res.AccessToken, ""); code != http.StatusNoContent {
		t.Errorf("logout: %d", code)
	}
	if code, _ := f.call(t, http.MethodGet, "/Users/Me", res.AccessToken, ""); code != http.StatusUnauthorized {
		t.Errorf("after logout: %d", code)
	}
	// A Cineva API key works as api_key.
	_, token, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	key := auth.APIKeyPrefix + token
	if _, err := f.st.CreateAPIKey(ctx, f.admin.ID, "script", auth.HashToken(key), nil); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.call(t, http.MethodGet, "/Users/Me?api_key="+key, "", ""); code != 200 {
		t.Errorf("api key: %d", code)
	}
	// Unknown calls under the shim's prefixes answer Jellyfin's 404, in
	// any case and with the emby prefix.
	if code, body := f.call(t, http.MethodGet, "/emby/LiveTv/Programs/Recommended", res.AccessToken, ""); code != 404 || !strings.Contains(body, `"title":"Not Found"`) {
		t.Errorf("unknown: %d %s", code, body)
	}
}

func TestJellyfinBrowsing(t *testing.T) {
	f := newJFFixture(t)
	_, kid := f.signIn(t, "kid", "Correct-horse-1")
	tok := kid.AccessToken
	uid := kid.User.ID

	// Views: the hidden library is absent for the kid.
	_, body := f.call(t, http.MethodGet, "/Users/"+uid+"/Views", tok, "")
	views := decode[jellyfin.QueryResult](t, body)
	names := []string{}
	for _, v := range views.Items {
		names = append(names, v.Name+":"+v.CollectionType)
	}
	if strings.Join(names, ",") != "Films:movies,Music:music,TV:tvshows" {
		t.Errorf("views: %v", names)
	}
	if code, _ := f.call(t, http.MethodGet, "/Users/"+uid+"/Items/"+jellyfin.ID(f.secret.ID), tok, ""); code != 404 {
		t.Errorf("hidden item: %d", code)
	}

	// Films by name, with the viewer's resume position; filters and paging.
	_, body = f.call(t, http.MethodGet, "/Users/"+uid+"/Items?ParentId="+jellyfin.ID(f.films.ID)+"&SortBy=SortName&Recursive=true&IncludeItemTypes=Movie", tok, "")
	films := decode[jellyfin.QueryResult](t, body)
	if films.TotalRecordCount != 2 || films.Items[0].Name != "The Avengers" || films.Items[1].Name != "Heat" {
		t.Errorf("films by sort name (article dropped): %+v", films)
	}
	if films.Items[1].UserData.PlaybackPositionTicks != jellyfin.Ticks(600) || films.Items[1].ProviderIds["Tmdb"] != "949" {
		t.Errorf("heat user data: %+v", films.Items[1].UserData)
	}
	_, body = f.call(t, http.MethodGet, "/Users/"+uid+"/Items?ParentId="+jellyfin.ID(f.films.ID)+"&Filters=IsResumable", tok, "")
	if r := decode[jellyfin.QueryResult](t, body); r.TotalRecordCount != 1 || r.Items[0].Name != "Heat" {
		t.Errorf("resumable: %+v", r)
	}
	_, body = f.call(t, http.MethodGet, "/Users/"+uid+"/Items?ParentId="+jellyfin.ID(f.films.ID)+"&SortBy=ProductionYear&SortOrder=Descending&StartIndex=1&Limit=1", tok, "")
	if r := decode[jellyfin.QueryResult](t, body); r.TotalRecordCount != 2 || len(r.Items) != 1 || r.Items[0].Name != "Heat" || r.StartIndex != 1 {
		t.Errorf("paged: %+v", r)
	}
	_, body = f.call(t, http.MethodGet, "/Items?IncludeItemTypes=Movie&Recursive=true&SearchTerm=heat", tok, "")
	if r := decode[jellyfin.QueryResult](t, body); r.TotalRecordCount != 1 || r.Items[0].Name != "Heat" {
		t.Errorf("search: %+v", r)
	}

	// The series tree, with context on each level and the kid's watched tick.
	_, body = f.call(t, http.MethodGet, "/Shows/"+jellyfin.ID(f.series.ID)+"/Seasons?UserId="+uid, tok, "")
	seasons := decode[jellyfin.QueryResult](t, body)
	if len(seasons.Items) != 1 || seasons.Items[0].SeriesName != "Succession" || *seasons.Items[0].UserData.UnplayedItemCount != 1 {
		t.Errorf("seasons: %+v", seasons)
	}
	_, body = f.call(t, http.MethodGet, "/Shows/"+jellyfin.ID(f.series.ID)+"/Episodes?SeasonId="+jellyfin.ID(f.season.ID)+"&UserId="+uid, tok, "")
	eps := decode[jellyfin.QueryResult](t, body)
	if len(eps.Items) != 2 || eps.Items[0].Name != "Celebration" || !eps.Items[0].UserData.Played || eps.Items[1].UserData.Played ||
		eps.Items[0].SeasonID != jellyfin.ID(f.season.ID) || eps.Items[0].SeriesID != jellyfin.ID(f.series.ID) || *eps.Items[1].IndexNumber != 2 {
		t.Errorf("episodes: %+v", eps)
	}
	_, body = f.call(t, http.MethodGet, "/Shows/NextUp?UserId="+uid, tok, "")
	if r := decode[jellyfin.QueryResult](t, body); r.TotalRecordCount != 1 || r.Items[0].Name != "Lifeboats" {
		t.Errorf("next up: %+v", r)
	}
	_, body = f.call(t, http.MethodGet, "/Users/"+uid+"/Items/Resume", tok, "")
	if r := decode[jellyfin.QueryResult](t, body); r.TotalRecordCount != 1 || r.Items[0].Name != "Heat" {
		t.Errorf("resume: %+v", r)
	}
	_, body = f.call(t, http.MethodGet, "/Items/"+jellyfin.ID(f.episodes[1].ID)+"/Ancestors", tok, "")
	var anc []jellyfin.BaseItem
	if err := json.Unmarshal([]byte(body), &anc); err != nil || len(anc) != 3 || anc[0].Type != "Season" || anc[1].Type != "Series" || anc[2].Type != "CollectionFolder" {
		t.Errorf("ancestors: %v %s", err, body)
	}

	// Music through the paged listings: albums under the library, tracks
	// under the album with their album and artist.
	_, body = f.call(t, http.MethodGet, "/Users/"+uid+"/Items?ParentId="+jellyfin.ID(f.music.ID)+"&IncludeItemTypes=MusicAlbum&Recursive=true", tok, "")
	if r := decode[jellyfin.QueryResult](t, body); r.TotalRecordCount != 1 || r.Items[0].Name != "Arrival" || r.Items[0].AlbumArtist != "ABBA" || r.Items[0].ChildCount != 2 {
		t.Errorf("albums: %+v", r)
	}
	_, body = f.call(t, http.MethodGet, "/Users/"+uid+"/Items?ParentId="+jellyfin.ID(f.album.ID), tok, "")
	if r := decode[jellyfin.QueryResult](t, body); r.TotalRecordCount != 2 || r.Items[1].Name != "Fernando" || r.Items[1].Album != "Arrival" || r.Items[1].AlbumID != jellyfin.ID(f.album.ID) {
		t.Errorf("tracks: %+v", r)
	}
	_, body = f.call(t, http.MethodGet, "/Artists?ParentId="+jellyfin.ID(f.music.ID), tok, "")
	if r := decode[jellyfin.QueryResult](t, body); r.TotalRecordCount != 1 || r.Items[0].Name != "ABBA" || r.Items[0].Type != "MusicArtist" {
		t.Errorf("artists: %+v", r)
	}

	// An item in full carries its streams; a missing image is a 404.
	_, body = f.call(t, http.MethodGet, "/Users/"+uid+"/Items/"+jellyfin.ID(f.film.ID), tok, "")
	film := decode[jellyfin.BaseItem](t, body)
	if film.Name != "Heat" || len(film.MediaSources) != 1 || film.MediaSources[0].Container != "mkv" || film.MediaStreams[0].Codec != "h264" || film.Path != "" {
		t.Errorf("film detail: %+v", film)
	}
	if code, _ := f.call(t, http.MethodGet, "/Items/"+jellyfin.ID(f.film.ID)+"/Images/Primary", "", ""); code != 404 {
		t.Errorf("missing image: %d", code)
	}
	if _, body := f.call(t, http.MethodGet, "/Search/Hints?searchTerm=succ", tok, ""); !strings.Contains(body, `"Name":"Succession"`) {
		t.Errorf("hints: %s", body)
	}
	if _, body := f.call(t, http.MethodGet, "/DisplayPreferences/usersettings?userId="+uid+"&client=emby", tok, ""); !strings.Contains(body, `"SortBy":"SortName"`) {
		t.Errorf("display prefs: %s", body)
	}
}

func TestJellyfinPlayState(t *testing.T) {
	f := newJFFixture(t)
	ctx := context.Background()
	_, kid := f.signIn(t, "kid", "Correct-horse-1")
	tok := kid.AccessToken
	uid := kid.User.ID
	ep := jellyfin.ID(f.episodes[1].ID)

	// A progress report keeps the position; a stop keeps the last one and
	// ends the playback; marking played and favourite land as state.
	if code, _ := f.call(t, http.MethodPost, "/Sessions/Playing", tok, `{"ItemId":"`+ep+`","PositionTicks":0,"PlayMethod":"DirectPlay"}`); code != 204 {
		t.Errorf("playing: %d", code)
	}
	if code, _ := f.call(t, http.MethodPost, "/Sessions/Playing/Progress", tok, `{"ItemId":"`+ep+`","PositionTicks":`+itoa(int(jellyfin.Ticks(900)))+`,"IsPaused":false}`); code != 204 {
		t.Errorf("progress: %d", code)
	}
	ps, err := f.st.PlayStateFor(ctx, f.kid.ID, f.episodes[1].ID)
	if err != nil || ps.PositionSecs != 900 {
		t.Errorf("position: %v %+v", err, ps)
	}
	_, body := f.call(t, http.MethodGet, "/Sessions", tok, "")
	var sessions []jellyfin.SessionInfo
	if err := json.Unmarshal([]byte(body), &sessions); err != nil || len(sessions) != 1 || sessions[0].NowPlayingItem == nil || sessions[0].NowPlayingItem.Name != "Lifeboats" {
		t.Errorf("sessions: %v %s", err, body)
	}
	if code, _ := f.call(t, http.MethodPost, "/Sessions/Playing/Stopped", tok, `{"ItemId":"`+ep+`","PositionTicks":`+itoa(int(jellyfin.Ticks(950)))+`}`); code != 204 {
		t.Errorf("stopped: %d", code)
	}
	if ps, _ := f.st.PlayStateFor(ctx, f.kid.ID, f.episodes[1].ID); ps.PositionSecs != 950 {
		t.Errorf("position after stop: %+v", ps)
	}
	if _, body := f.call(t, http.MethodPost, "/Users/"+uid+"/PlayedItems/"+ep, tok, ""); !strings.Contains(body, `"Played":true`) {
		t.Errorf("mark played: %s", body)
	}
	if ps, _ := f.st.PlayStateFor(ctx, f.kid.ID, f.episodes[1].ID); !ps.Watched {
		t.Error("not watched after PlayedItems")
	}
	if _, body := f.call(t, http.MethodDelete, "/Users/"+uid+"/PlayedItems/"+ep, tok, ""); !strings.Contains(body, `"Played":false`) {
		t.Errorf("mark unplayed: %s", body)
	}
	if _, body := f.call(t, http.MethodPost, "/Users/"+uid+"/FavoriteItems/"+jellyfin.ID(f.film.ID), tok, ""); !strings.Contains(body, `"IsFavorite":true`) {
		t.Errorf("favourite: %s", body)
	}
	ratings, _ := f.st.RatingsByLibrary(ctx, f.kid.ID, f.films.ID)
	if ratings[f.film.ID] != 5 {
		t.Errorf("favourite is a five-star rating: %v", ratings)
	}
	if _, body := f.call(t, http.MethodPost, "/UserItems/"+ep+"/UserData", tok, `{"Played":true,"IsFavorite":false}`); !strings.Contains(body, `"Played":true`) {
		t.Errorf("user data post: %s", body)
	}
	// Marking a series marks every episode.
	if _, body := f.call(t, http.MethodDelete, "/Users/"+uid+"/PlayedItems/"+jellyfin.ID(f.series.ID), tok, ""); !strings.Contains(body, `"Played":false`) {
		t.Errorf("series unplayed: %s", body)
	}
	for _, e := range f.episodes {
		if ps, _ := f.st.PlayStateFor(ctx, f.kid.ID, e.ID); ps.Watched {
			t.Errorf("%s still watched", e.Title)
		}
	}
	// A film or episode the kid cannot see is not found.
	if code, _ := f.call(t, http.MethodPost, "/Users/"+uid+"/PlayedItems/"+jellyfin.ID(f.secret.ID), tok, ""); code != 404 {
		t.Errorf("hidden played: %d", code)
	}
	// The master playlist needs a live session; an unknown one is 404.
	if code, _ := f.call(t, http.MethodGet, "/Videos/"+ep+"/master.m3u8?PlaySessionId=nope", tok, ""); code != 404 {
		t.Errorf("unknown session: %d", code)
	}
}

// The shim must not claim paths it does not serve: registering it on an
// Echo group with middleware gave the root prefix catch-all routes, and
// the web client's own front page answered 401. A path the shim does not
// know stays a 404 for the server to handle, and a route registered
// afterwards at the root still wins.
func TestJellyfinClaimsOnlyItsRoutes(t *testing.T) {
	f := newJFFixture(t)
	e := echo.New()
	f.srv.jellyfinRoutes(e)
	e.GET("/", func(c echo.Context) error { return c.String(http.StatusOK, "client") })
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/", http.StatusOK},
		{"/libraries/Music", http.StatusNotFound},
		{"/System/Info/Public", http.StatusOK},
		{"/Users/Me", http.StatusUnauthorized},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("GET %s = %d, want %d", tc.path, rec.Code, tc.want)
		}
	}
}
