package api

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/store"
)

// subsonicFixture is a server with the shim mounted over a small music
// library: one open library with an artist, an album and three tracks,
// one restricted library a regular user cannot see, a regular user with a
// password and an API key, and a disabled user.
type subsonicFixture struct {
	e              *echo.Echo
	st             *store.Store
	open           store.Library
	hidden         store.Library
	artist         store.Item
	album          store.Item
	tracks         []store.Item
	hiddenID       string
	apiKey, appKey string
}

const subsonicPassword = "Correct-horse-9"

func newSubsonicFixture(t *testing.T) subsonicFixture {
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
	f := subsonicFixture{st: st}
	if f.open, err = st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{"/music"}); err != nil {
		t.Fatal(err)
	}
	if f.hidden, err = st.CreateLibrary(ctx, "Grown-up music", store.MediaMusic, []string{"/grown"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{"/films"}); err != nil {
		t.Fatal(err)
	}
	if f.artist, err = st.UpsertItem(ctx, store.Item{LibraryID: f.open.ID, Kind: store.KindArtist, Title: "The Beatles", Artist: "The Beatles", Path: "artist://the beatles"}); err != nil {
		t.Fatal(err)
	}
	var saves []store.TrackSave
	for i, title := range []string{"Taxman", "Eleanor Rigby", "Love You To"} {
		saves = append(saves, store.TrackSave{Item: store.Item{LibraryID: f.open.ID, Kind: store.KindTrack, Title: title,
			Artist: "The Beatles", TrackNum: i + 1, Path: "/music/Beatles/Revolver/" + title + ".flac", RuntimeSecs: 160,
			Container: "flac", Bitrate: 900}, Genres: []string{"Rock"},
			Lyrics: &store.Lyrics{Synced: true, Text: "[00:01.00] " + title}})
	}
	if f.album, f.tracks, err = st.SaveAlbum(ctx, store.Item{LibraryID: f.open.ID, Kind: store.KindAlbum, ParentID: f.artist.ID,
		Title: "Revolver", Artist: "The Beatles", Year: 1966, Path: "/music/Beatles/Revolver"}, saves); err != nil {
		t.Fatal(err)
	}
	hArtist, err := st.UpsertItem(ctx, store.Item{LibraryID: f.hidden.ID, Kind: store.KindArtist, Title: "Secret", Path: "artist://secret"})
	if err != nil {
		t.Fatal(err)
	}
	f.hiddenID = hArtist.ID
	// An admin first, so the regular user is not the first account.
	hash, err := auth.HashPassword(subsonicPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(ctx, "admin", "", hash, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	kid, err := st.CreateUser(ctx, "kid", "Kid", hash, store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	off, err := st.CreateUser(ctx, "off", "", hash, store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateUser(ctx, off.ID, "", store.RoleUser, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryAccess(ctx, f.hidden.ID, store.LibraryAccess{Restricted: true}); err != nil {
		t.Fatal(err)
	}
	token, keyHash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	f.apiKey = auth.APIKeyPrefix + token
	if _, err := st.CreateAPIKey(ctx, kid.ID, "phone", auth.HashToken(f.apiKey), nil); err != nil {
		t.Fatal(err)
	}
	_ = keyHash
	// A key made for music apps: a sealed copy lets token sign-in work.
	appToken, _, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	f.appKey = auth.APIKeyPrefix + appToken
	sk, err := st.SecretsKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := auth.Seal(sk, f.appKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateAPIKey(ctx, kid.ID, "music app", auth.HashToken(f.appKey), sealed); err != nil {
		t.Fatal(err)
	}
	s := &server{store: st, throttle: auth.NewThrottle(), events: events.NewBus()}
	f.e = echo.New()
	s.subsonicRoutes(f.e)
	return f
}

// call makes a request with the given parameters and returns the HTTP
// status and the decoded subsonic-response (JSON unless f says otherwise).
func (f subsonicFixture) call(t *testing.T, method, name string, params url.Values) (int, map[string]any, string) {
	t.Helper()
	var req *http.Request
	if method == http.MethodPost {
		req = httptest.NewRequest(method, "/rest/"+name, strings.NewReader(params.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, "/rest/"+name+"?"+params.Encode(), nil)
	}
	rec := httptest.NewRecorder()
	f.e.ServeHTTP(rec, req)
	body := rec.Body.String()
	if params.Get("f") == "json" {
		var decoded map[string]map[string]any
		if err := json.Unmarshal([]byte(body), &decoded); err != nil {
			t.Fatalf("%s: %v in %s", name, err, body)
		}
		return rec.Code, decoded["subsonic-response"], body
	}
	return rec.Code, nil, body
}

func asKid(extra ...string) url.Values {
	v := url.Values{"u": {"kid"}, "p": {subsonicPassword}, "v": {"1.16.1"}, "c": {"test"}, "f": {"json"}}
	for i := 0; i+1 < len(extra); i += 2 {
		v.Add(extra[i], extra[i+1])
	}
	return v
}

func errorCode(env map[string]any) int {
	if e, ok := env["error"].(map[string]any); ok {
		return int(e["code"].(float64))
	}
	return -1
}

func TestSubsonicAuth(t *testing.T) {
	f := newSubsonicFixture(t)
	tests := []struct {
		name   string
		params url.Values
		status string
		code   int
	}{
		{"password", asKid(), "ok", -1},
		{"hex password", url.Values{"u": {"kid"}, "p": {"enc:" + hexOf(subsonicPassword)}, "f": {"json"}}, "ok", -1},
		{"api key", url.Values{"apiKey": {f.apiKey}, "f": {"json"}}, "ok", -1},
		{"api key with a user", url.Values{"apiKey": {f.apiKey}, "u": {"kid"}, "f": {"json"}}, "failed", 43},
		{"bad api key", url.Values{"apiKey": {"cnv_nope"}, "f": {"json"}}, "failed", 44},
		{"token with an app key", url.Values{"u": {"kid"}, "t": {saltedToken(f.appKey, "salt")}, "s": {"salt"}, "f": {"json"}}, "ok", -1},
		{"token, upper-case hex", url.Values{"u": {"kid"}, "t": {strings.ToUpper(saltedToken(f.appKey, "salt"))}, "s": {"salt"}, "f": {"json"}}, "ok", -1},
		{"wrong token", url.Values{"u": {"kid"}, "t": {"abc"}, "s": {"salt"}, "f": {"json"}}, "failed", 40},
		{"token without an app key", url.Values{"u": {"admin"}, "t": {"abc"}, "s": {"salt"}, "f": {"json"}}, "failed", 41},
		{"app key as the password", url.Values{"u": {"kid"}, "p": {f.appKey}, "f": {"json"}}, "ok", -1},
		{"plain key as the password", url.Values{"u": {"kid"}, "p": {f.apiKey}, "f": {"json"}}, "ok", -1},
		{"someone else's key as the password", url.Values{"u": {"admin"}, "p": {f.apiKey}, "f": {"json"}}, "failed", 40},
		{"wrong password", url.Values{"u": {"kid"}, "p": {"wrong"}, "f": {"json"}}, "failed", 40},
		{"unknown user", url.Values{"u": {"nobody"}, "p": {subsonicPassword}, "f": {"json"}}, "failed", 40},
		{"disabled user", url.Values{"u": {"off"}, "p": {subsonicPassword}, "f": {"json"}}, "failed", 40},
		{"no user", url.Values{"f": {"json"}}, "failed", 10},
		{"no password", url.Values{"u": {"kid"}, "f": {"json"}}, "failed", 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, env, body := f.call(t, http.MethodGet, "ping.view", tc.params)
			if status != http.StatusOK || env["status"] != tc.status || errorCode(env) != tc.code {
				t.Errorf("got %d %s", status, body)
			}
			if tc.code > 0 {
				if e := env["error"].(map[string]any); e["helpUrl"] == nil {
					t.Errorf("no helpUrl in %s", body)
				}
			}
		})
	}
	// The second password check is served from the cache: the throttle
	// never sees it, so many requests cannot trip it.
	for i := 0; i < 20; i++ {
		if _, env, body := f.call(t, http.MethodGet, "ping", asKid()); env["status"] != "ok" {
			t.Fatalf("request %d: %s", i, body)
		}
	}
}

// saltedToken is what a Subsonic client sends: md5(password + salt) in hex.
func saltedToken(password, salt string) string {
	sum := md5.Sum([]byte(password + salt)) //nolint:gosec // the protocol's own token
	return hex.EncodeToString(sum[:])
}

func hexOf(s string) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(s)*2)
	for i := 0; i < len(s); i++ {
		out = append(out, digits[s[i]>>4], digits[s[i]&15])
	}
	return string(out)
}

func TestSubsonicEnvelope(t *testing.T) {
	f := newSubsonicFixture(t)
	_, _, body := f.call(t, http.MethodGet, "ping", url.Values{"u": {"kid"}, "p": {subsonicPassword}})
	if !strings.HasPrefix(body, `<?xml`) || !strings.Contains(body, `status="ok"`) || !strings.Contains(body, `xmlns="http://subsonic.org/restapi"`) {
		t.Errorf("xml by default: %s", body)
	}
	_, _, body = f.call(t, http.MethodGet, "ping", url.Values{"u": {"kid"}, "p": {subsonicPassword}, "f": {"jsonp"}, "callback": {"cb"}})
	if !strings.HasPrefix(body, "cb({") {
		t.Errorf("jsonp: %s", body)
	}
	_, env, body := f.call(t, http.MethodGet, "getJukeboxOrSomething", asKid())
	if errorCode(env) != 70 {
		t.Errorf("unknown call: %s", body)
	}
	_, env, body = f.call(t, http.MethodGet, "getLicense", asKid())
	if env["license"].(map[string]any)["valid"] != true {
		t.Errorf("licence: %s", body)
	}
	_, env, _ = f.call(t, http.MethodGet, "getOpenSubsonicExtensions", asKid())
	if n := len(env["openSubsonicExtensions"].([]any)); n != 4 {
		t.Errorf("%d extensions", n)
	}
}

func TestSubsonicBrowsing(t *testing.T) {
	f := newSubsonicFixture(t)

	_, env, body := f.call(t, http.MethodGet, "getMusicFolders", asKid())
	folders := env["musicFolders"].(map[string]any)["musicFolder"].([]any)
	if len(folders) != 1 || folders[0].(map[string]any)["id"] != f.open.ID {
		t.Errorf("only the open music library is a folder: %s", body)
	}

	_, env, body = f.call(t, http.MethodGet, "getArtists", asKid())
	index := env["artists"].(map[string]any)["index"].([]any)
	if len(index) != 1 || index[0].(map[string]any)["name"] != "B" {
		t.Fatalf("artists index (The Beatles under B): %s", body)
	}
	artist := index[0].(map[string]any)["artist"].([]any)[0].(map[string]any)
	if artist["id"] != f.artist.ID || artist["albumCount"] != float64(1) {
		t.Errorf("artist entry: %v", artist)
	}

	_, env, body = f.call(t, http.MethodGet, "getArtist", asKid("id", f.artist.ID))
	albums := env["artist"].(map[string]any)["album"].([]any)
	if len(albums) != 1 || albums[0].(map[string]any)["songCount"] != float64(3) || albums[0].(map[string]any)["duration"] != float64(480) {
		t.Errorf("artist's albums: %s", body)
	}

	_, env, body = f.call(t, http.MethodGet, "getAlbum.view", asKid("id", f.album.ID))
	album := env["album"].(map[string]any)
	songs := album["song"].([]any)
	if album["name"] != "Revolver" || album["year"] != float64(1966) || album["genre"] != "Rock" || len(songs) != 3 {
		t.Fatalf("album: %s", body)
	}
	first := songs[0].(map[string]any)
	if first["title"] != "Taxman" || first["albumId"] != f.album.ID || first["artistId"] != f.artist.ID ||
		first["contentType"] != "audio/flac" || first["bitRate"] != float64(900) || first["isDir"] != false {
		t.Errorf("song: %v", first)
	}

	_, env, body = f.call(t, http.MethodGet, "getMusicDirectory", asKid("id", f.open.ID))
	if children := env["directory"].(map[string]any)["child"].([]any); len(children) != 1 || children[0].(map[string]any)["isDir"] != true {
		t.Errorf("folder directory: %s", body)
	}
	_, env, body = f.call(t, http.MethodGet, "getMusicDirectory", asKid("id", f.album.ID))
	if children := env["directory"].(map[string]any)["child"].([]any); len(children) != 3 {
		t.Errorf("album directory: %s", body)
	}

	_, env, body = f.call(t, http.MethodGet, "getGenres", asKid())
	genres := env["genres"].(map[string]any)["genre"].([]any)
	if len(genres) != 1 || genres[0].(map[string]any)["value"] != "Rock" || genres[0].(map[string]any)["songCount"] != float64(3) ||
		genres[0].(map[string]any)["albumCount"] != float64(1) {
		t.Errorf("genres: %s", body)
	}

	_, env, body = f.call(t, http.MethodGet, "getAlbumList2", asKid("type", "newest", "size", "5"))
	if list := env["albumList2"].(map[string]any)["album"].([]any); len(list) != 1 {
		t.Errorf("newest albums: %s", body)
	}
	_, env, body = f.call(t, http.MethodGet, "getAlbumList2", asKid("type", "byGenre", "genre", "Jazz"))
	if list := env["albumList2"].(map[string]any)["album"].([]any); len(list) != 0 {
		t.Errorf("jazz albums: %s", body)
	}
	_, env, body = f.call(t, http.MethodGet, "getRandomSongs", asKid("size", "2"))
	if list := env["randomSongs"].(map[string]any)["song"].([]any); len(list) != 2 {
		t.Errorf("random songs: %s", body)
	}

	// The hidden library's artist is not found, nor listed anywhere.
	_, env, _ = f.call(t, http.MethodGet, "getArtist", asKid("id", f.hiddenID))
	if errorCode(env) != 70 {
		t.Errorf("hidden artist answered %v", env)
	}
	_, env, _ = f.call(t, http.MethodGet, "getMusicDirectory", asKid("id", f.hidden.ID))
	if errorCode(env) != 70 {
		t.Errorf("hidden folder answered %v", env)
	}

	_, env, body = f.call(t, http.MethodGet, "getLyricsBySongId", asKid("id", f.tracks[1].ID))
	lyrics := env["lyricsList"].(map[string]any)["structuredLyrics"].([]any)[0].(map[string]any)
	if lyrics["synced"] != true || lyrics["line"].([]any)[0].(map[string]any)["value"] != "Eleanor Rigby" {
		t.Errorf("lyrics: %s", body)
	}
}

func TestSubsonicSearchAndStars(t *testing.T) {
	f := newSubsonicFixture(t)

	_, env, body := f.call(t, http.MethodGet, "search3", asKid("query", "elea"))
	res := env["searchResult3"].(map[string]any)
	if len(res["artist"].([]any)) != 0 || len(res["song"].([]any)) != 1 || res["song"].([]any)[0].(map[string]any)["title"] != "Eleanor Rigby" {
		t.Errorf("search3: %s", body)
	}
	// A POST carries the same parameters in its body (the formPost extension).
	_, env, body = f.call(t, http.MethodPost, "search3", asKid("query", "beat"))
	if len(env["searchResult3"].(map[string]any)["artist"].([]any)) != 1 {
		t.Errorf("search3 by POST: %s", body)
	}

	// star is a five-star rating; unstar clears it; the starred list shows
	// what is rated five.
	_, env, body = f.call(t, http.MethodGet, "star", asKid("id", f.tracks[0].ID, "albumId", f.album.ID))
	if env["status"] != "ok" {
		t.Fatalf("star: %s", body)
	}
	ratings, err := f.st.RatingsFor(context.Background(), userIDOf(t, f.st, "kid"), []string{f.tracks[0].ID, f.album.ID})
	if err != nil || ratings[f.tracks[0].ID] != 5 || ratings[f.album.ID] != 5 {
		t.Errorf("ratings after star: %v %v", ratings, err)
	}
	_, env, body = f.call(t, http.MethodGet, "getStarred2", asKid())
	starred := env["starred2"].(map[string]any)
	if len(starred["song"].([]any)) != 1 || len(starred["album"].([]any)) != 1 || starred["song"].([]any)[0].(map[string]any)["starred"] == nil {
		t.Errorf("starred2: %s", body)
	}
	_, _, _ = f.call(t, http.MethodGet, "unstar", asKid("id", f.tracks[0].ID))
	_, env, body = f.call(t, http.MethodGet, "getStarred2", asKid())
	if len(env["starred2"].(map[string]any)["song"].([]any)) != 0 {
		t.Errorf("after unstar: %s", body)
	}
	_, env, _ = f.call(t, http.MethodGet, "setRating", asKid("id", f.tracks[2].ID, "rating", "3"))
	if env["status"] != "ok" {
		t.Error("setRating failed")
	}
	_, env, body = f.call(t, http.MethodGet, "getSong", asKid("id", f.tracks[2].ID))
	if env["song"].(map[string]any)["userRating"] != float64(3) {
		t.Errorf("song rating: %s", body)
	}

	// Playlists: created with songs, listed, extended, deleted.
	_, env, body = f.call(t, http.MethodGet, "createPlaylist", asKid("name", "Mine", "songId", f.tracks[0].ID, "songId", f.tracks[2].ID))
	pl := env["playlist"].(map[string]any)
	if pl["songCount"] != float64(2) || !strings.HasPrefix(pl["id"].(string), "pl-") || len(pl["entry"].([]any)) != 2 {
		t.Fatalf("createPlaylist: %s", body)
	}
	plID := pl["id"].(string)
	_, env, body = f.call(t, http.MethodGet, "updatePlaylist", asKid("playlistId", plID, "songIdToAdd", f.tracks[1].ID, "songIndexToRemove", "0"))
	if env["status"] != "ok" {
		t.Fatalf("updatePlaylist: %s", body)
	}
	_, env, body = f.call(t, http.MethodGet, "getPlaylist", asKid("id", plID))
	entries := env["playlist"].(map[string]any)["entry"].([]any)
	if len(entries) != 2 || entries[0].(map[string]any)["title"] != "Love You To" || entries[1].(map[string]any)["title"] != "Eleanor Rigby" {
		t.Errorf("playlist after update: %s", body)
	}
	_, env, _ = f.call(t, http.MethodGet, "getPlaylists", asKid())
	if len(env["playlists"].(map[string]any)["playlist"].([]any)) != 1 {
		t.Error("playlists: expected one")
	}
	_, env, _ = f.call(t, http.MethodGet, "deletePlaylist", asKid("id", plID))
	if env["status"] != "ok" {
		t.Error("deletePlaylist failed")
	}

	// scrobble counts a play.
	_, env, _ = f.call(t, http.MethodGet, "scrobble", asKid("id", f.tracks[0].ID, "submission", "true"))
	if env["status"] != "ok" {
		t.Error("scrobble failed")
	}
	_, env, body = f.call(t, http.MethodGet, "getAlbum", asKid("id", f.album.ID))
	if env["album"].(map[string]any)["song"].([]any)[0].(map[string]any)["playCount"] != float64(1) {
		t.Errorf("play count after scrobble: %s", body)
	}

	// Users: a regular user sees themselves and not the others.
	_, env, body = f.call(t, http.MethodGet, "getUser", asKid("username", "kid"))
	if u := env["user"].(map[string]any); u["adminRole"] != false || u["streamRole"] != true || len(u["folder"].([]any)) != 1 {
		t.Errorf("getUser: %s", body)
	}
	_, env, _ = f.call(t, http.MethodGet, "getUsers", asKid())
	if errorCode(env) != 50 {
		t.Errorf("getUsers as a regular user: %v", env)
	}
	_, env, body = f.call(t, http.MethodGet, "getUsers", url.Values{"u": {"admin"}, "p": {subsonicPassword}, "f": {"json"}})
	if len(env["users"].(map[string]any)["user"].([]any)) != 3 {
		t.Errorf("getUsers as admin: %s", body)
	}
	_, env, body = f.call(t, http.MethodGet, "getPodcasts", asKid())
	if env["status"] != "ok" || env["podcasts"] == nil {
		t.Errorf("podcasts: %s", body)
	}
}

func userIDOf(t *testing.T, st *store.Store, name string) string {
	t.Helper()
	u, err := st.UserByUsername(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}
