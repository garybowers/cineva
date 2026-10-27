package api

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/rand/v2"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/store"
	"github.com/garybowers/cineva/server/internal/subsonic"
)

// The Subsonic-compatible API at /rest/ (server/docs/subsonic.md): a shim
// over the same store, listings, playlists, ratings and streaming the
// web client uses, so Symfonium, DSub, play:Sub, Feishin and the rest can
// play a Cineva library. Thin on purpose: nothing in Cineva changes shape
// for it.

var subsonicLog = logging.For("subsonic")

// subsonicCall answers one API call. It returns the envelope to send, a
// *subsonic.Error for a failed one, or errSubsonicWritten when it wrote
// the response itself (a stream, a picture).
type subsonicCall func(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error)

var errSubsonicWritten = errors.New("subsonic: response written")

const ctxSubsonicParams = "cineva.subsonic.params"

// Page sizes: the API's defaults and the most one call answers.
const (
	subsonicDefaultCount = 10
	subsonicMaxCount     = 500
	subsonicSearchCount  = 20
	subsonicHelpPath     = "/settings"
)

// subsonicPasswordCache remembers (user, password) pairs that verified, for
// subsonicPasswordTTL, so a client's many requests do not each cost an
// argon2id check. The password is kept only as a hash.
type subsonicPasswordCache struct {
	mu   sync.Mutex
	seen map[string]time.Time // userID + ":" + sha256(password) -> verified at
}

const subsonicPasswordTTL = 10 * time.Minute

func (p *subsonicPasswordCache) key(userID, password string) string {
	sum := sha256.Sum256([]byte(password))
	return userID + ":" + hex.EncodeToString(sum[:])
}

func (p *subsonicPasswordCache) has(userID, password string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	at, ok := p.seen[p.key(userID, password)]
	return ok && time.Since(at) < subsonicPasswordTTL
}

func (p *subsonicPasswordCache) add(userID, password string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen == nil {
		p.seen = map[string]time.Time{}
	}
	now := time.Now()
	for k, at := range p.seen {
		if now.Sub(at) > subsonicPasswordTTL {
			delete(p.seen, k)
		}
	}
	p.seen[p.key(userID, password)] = now
}

// subsonicRoutes mounts the API. Every call is one route, GET or POST
// (the formPost extension), with or without the ".view" suffix.
func (s *server) subsonicRoutes(e *echo.Echo) {
	calls := map[string]subsonicCall{
		"ping":                      func(echo.Context, store.User, url.Values) (*subsonic.Response, error) { return subsonic.OK(), nil },
		"getLicense":                s.ssLicense,
		"getOpenSubsonicExtensions": s.ssExtensions,
		"getMusicFolders":           s.ssMusicFolders,
		"getIndexes":                s.ssIndexes,
		"getArtists":                s.ssArtists,
		"getMusicDirectory":         s.ssMusicDirectory,
		"getGenres":                 s.ssGenres,
		"getArtist":                 s.ssArtist,
		"getAlbum":                  s.ssAlbum,
		"getSong":                   s.ssSong,
		"getArtistInfo":             s.ssArtistInfo,
		"getArtistInfo2":            s.ssArtistInfo,
		"getAlbumInfo":              s.ssAlbumInfo,
		"getAlbumInfo2":             s.ssAlbumInfo,
		"getAlbumList":              s.ssAlbumList,
		"getAlbumList2":             s.ssAlbumList,
		"getRandomSongs":            s.ssRandomSongs,
		"getSongsByGenre":           s.ssSongsByGenre,
		"getTopSongs":               s.ssTopSongs,
		"getNowPlaying":             s.ssNowPlaying,
		"getStarred":                s.ssStarred,
		"getStarred2":               s.ssStarred,
		"search2":                   s.ssSearch,
		"search3":                   s.ssSearch,
		"getPlaylists":              s.ssPlaylists,
		"getPlaylist":               s.ssPlaylist,
		"createPlaylist":            s.ssCreatePlaylist,
		"updatePlaylist":            s.ssUpdatePlaylist,
		"deletePlaylist":            s.ssDeletePlaylist,
		"stream":                    s.ssStream,
		"download":                  s.ssDownload,
		"getCoverArt":               s.ssCoverArt,
		"getAvatar":                 s.ssAvatar,
		"getLyrics":                 s.ssLyrics,
		"getLyricsBySongId":         s.ssLyricsBySongID,
		"star":                      s.ssStar,
		"unstar":                    s.ssUnstar,
		"setRating":                 s.ssSetRating,
		"scrobble":                  s.ssScrobble,
		"getUser":                   s.ssUser,
		"getUsers":                  s.ssUsers,
		"getScanStatus":             s.ssScanStatus,
		"startScan":                 s.ssStartScan,
		"getPlayQueue":              func(echo.Context, store.User, url.Values) (*subsonic.Response, error) { return subsonic.OK(), nil },
		"savePlayQueue":             func(echo.Context, store.User, url.Values) (*subsonic.Response, error) { return subsonic.OK(), nil },
		"getBookmarks":              emptyCall(func(r *subsonic.Response) { r.Bookmarks = &subsonic.Empty{} }),
		"getInternetRadioStations":  emptyCall(func(r *subsonic.Response) { r.InternetRadioStations = &subsonic.Empty{} }),
		"getPodcasts":               emptyCall(func(r *subsonic.Response) { r.Podcasts = &subsonic.Empty{} }),
		"getNewestPodcasts":         emptyCall(func(r *subsonic.Response) { r.NewestPodcasts = &subsonic.Empty{} }),
		"getVideos":                 emptyCall(func(r *subsonic.Response) { r.Videos = &subsonic.Empty{} }),
		"getShares":                 emptyCall(func(r *subsonic.Response) { r.Shares = &subsonic.Empty{} }),
		"getChatMessages":           emptyCall(func(r *subsonic.Response) { r.ChatMessages = &subsonic.Empty{} }),
	}
	dispatch := func(c echo.Context) error {
		name := strings.TrimSuffix(c.Param("call"), ".view")
		call, ok := calls[name]
		if !ok {
			return s.writeSubsonic(c, subsonic.Failed(subsonic.NewError(subsonic.ErrNotFound, "%s is not served by Cineva", name)))
		}
		u := currentUser(c)
		p, _ := c.Get(ctxSubsonicParams).(url.Values)
		resp, err := call(c, u, p)
		switch {
		case errors.Is(err, errSubsonicWritten):
			return nil
		case err != nil:
			var se *subsonic.Error
			if errors.As(err, &se) {
				return s.writeSubsonic(c, subsonic.Failed(se))
			}
			var he *echo.HTTPError
			if errors.As(err, &he) {
				code := subsonic.ErrGeneric
				if he.Code == http.StatusNotFound {
					code = subsonic.ErrNotFound
				}
				return s.writeSubsonic(c, subsonic.Failed(subsonic.NewError(code, "%v", he.Message)))
			}
			subsonicLog.Warn("call failed", "call", name, "user", u.Username, "err", err)
			return s.writeSubsonic(c, subsonic.Failed(subsonic.NewError(subsonic.ErrGeneric, "%s failed", name)))
		}
		return s.writeSubsonic(c, resp)
	}
	g := e.Group("/rest", s.subsonicAuth)
	g.GET("/:call", dispatch)
	g.POST("/:call", dispatch)
}

func emptyCall(set func(*subsonic.Response)) subsonicCall {
	return func(echo.Context, store.User, url.Values) (*subsonic.Response, error) {
		r := subsonic.OK()
		set(r)
		return r, nil
	}
}

// subsonicParams merges the query string and a form body, as the API
// allows parameters in either.
func subsonicParams(c echo.Context) url.Values {
	if p, ok := c.Get(ctxSubsonicParams).(url.Values); ok {
		return p
	}
	p := url.Values{}
	for k, v := range c.QueryParams() {
		p[k] = append(p[k], v...)
	}
	if form, err := c.FormParams(); err == nil {
		for k, v := range form {
			if _, seen := p[k]; !seen {
				p[k] = append(p[k], v...)
			}
		}
	}
	c.Set(ctxSubsonicParams, p)
	return p
}

// writeSubsonic sends an envelope in the format the request asked for.
// Always HTTP 200: the API reports failure inside the envelope.
func (s *server) writeSubsonic(c echo.Context, r *subsonic.Response) error {
	format, callback := subsonic.Format(subsonicParams(c))
	var buf strings.Builder
	ct, err := subsonic.Encode(&buf, format, callback, r)
	if err != nil {
		return err
	}
	return c.Blob(http.StatusOK, ct, []byte(buf.String()))
}

// subsonicAuth resolves the caller from the API's parameters: apiKey (the
// OpenSubsonic extension) onto Cineva's API keys, or u with p (the account
// password, plain or enc:hex). Salt-and-token auth needs a password the
// server can read back, which argon2id does not give, so it is refused
// with code 41 and a message saying what to use instead.
func (s *server) subsonicAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		p := subsonicParams(c)
		fail := func(code int, msg string) error {
			e := subsonic.NewError(code, "%s", msg)
			e.HelpURL = c.Scheme() + "://" + c.Request().Host + subsonicHelpPath
			return s.writeSubsonic(c, subsonic.Failed(e))
		}
		username, apiKey := p.Get("u"), p.Get("apiKey")
		password, token := p.Get("p"), p.Get("t")
		switch {
		case apiKey != "":
			if username != "" || password != "" || token != "" {
				return fail(subsonic.ErrAuthConflict, "send apiKey on its own, without u, p, t or s")
			}
			_, u, err := s.store.APIKeyByHash(ctx, auth.HashToken(apiKey))
			if err != nil || u.Disabled {
				return fail(subsonic.ErrInvalidAPIKey, "invalid API key")
			}
			c.Set(ctxUser, u)
			return next(c)
		case username == "":
			return fail(subsonic.ErrMissingParam, "u (or apiKey) is required")
		case token != "" || p.Get("s") != "":
			return s.subsonicTokenAuth(c, next, username, token, p.Get("s"), p.Get("c"), fail)
		case password == "":
			return fail(subsonic.ErrMissingParam, "p is required")
		}
		if enc, ok := strings.CutPrefix(password, "enc:"); ok {
			raw, err := hex.DecodeString(enc)
			if err != nil {
				return fail(subsonic.ErrWrongCredential, "wrong username or password")
			}
			password = string(raw)
		}
		u, err := s.store.UserByUsername(ctx, username)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if u.ID != "" && !u.Disabled && s.subsonicPasswords.has(u.ID, password) {
			c.Set(ctxUser, u)
			return next(c)
		}
		// An API key typed into the app's password box: the one made in
		// Settings, hashed, so the lookup is as cheap as a session's.
		if u.ID != "" && !u.Disabled {
			if _, ku, err := s.store.APIKeyByHash(ctx, auth.HashToken(password)); err == nil && ku.ID == u.ID {
				c.Set(ctxUser, u)
				return next(c)
			}
		}
		key := "subsonic:" + strings.ToLower(strings.TrimSpace(username))
		if ok, _ := s.throttle.Allow(key); !ok {
			return fail(subsonic.ErrWrongCredential, "too many failed attempts, try again later")
		}
		hash := u.PasswordHash
		if hash == "" {
			hash = dummyHash
		}
		if !auth.VerifyPassword(password, hash) || u.ID == "" || u.Disabled {
			s.throttle.Fail(key)
			subsonicLog.Warn("sign-in failed", "user", username, "client", p.Get("c"), "ip", c.RealIP())
			return fail(subsonic.ErrWrongCredential, "wrong username or password")
		}
		s.throttle.Reset(key)
		s.subsonicPasswords.add(u.ID, password)
		c.Set(ctxUser, u)
		return next(c)
	}
}

// subsonicTokenAuth checks a salted token, t = md5(secret + s), against
// the user's API keys made for apps, whose sealed copies the server can
// read back (the account password cannot be: argon2id gives nothing to
// hash with). A user with no such key is told what to make.
func (s *server) subsonicTokenAuth(c echo.Context, next echo.HandlerFunc, username, token, salt, client string,
	fail func(code int, msg string) error) error {
	ctx := c.Request().Context()
	key := "subsonic:" + strings.ToLower(strings.TrimSpace(username))
	if ok, _ := s.throttle.Allow(key); !ok {
		return fail(subsonic.ErrWrongCredential, "too many failed attempts, try again later")
	}
	u, err := s.store.UserByUsername(ctx, username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	var appKeys []store.APIKey
	if u.ID != "" && !u.Disabled {
		keys, err := s.store.APIKeys(ctx, u.ID)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if k.ForApps {
				appKeys = append(appKeys, k)
			}
		}
	}
	if len(appKeys) == 0 {
		s.throttle.Fail(key)
		// Worth a line by default: to the app's user this looks like a
		// wrong password, and this is the only place that says otherwise.
		subsonicLog.Warn("sign-in refused: token authentication without an app key", "user", username, "client", client,
			"fix", "make an API key for music apps in Settings and use it as the password, or turn on the app's legacy authentication")
		return fail(subsonic.ErrTokenUnsupported,
			"token authentication needs an API key made for music apps in Settings, used as the password; or send the password itself (p)")
	}
	sk, err := s.store.SecretsKey(ctx)
	if err != nil {
		return err
	}
	want := strings.ToLower(token)
	for _, k := range appKeys {
		secret, err := auth.Open(sk, k.SecretEnc)
		if err != nil {
			subsonicLog.Warn("unsealing an api key failed", "key", k.ID, "err", err)
			continue
		}
		sum := md5.Sum([]byte(secret + salt)) //nolint:gosec // the Subsonic protocol's own token
		if hex.EncodeToString(sum[:]) == want {
			s.throttle.Reset(key)
			_, _, _ = s.store.APIKeyByHash(ctx, k.KeyHash) // records last use
			c.Set(ctxUser, u)
			return next(c)
		}
	}
	s.throttle.Fail(key)
	subsonicLog.Warn("sign-in failed", "user", username, "client", client, "ip", c.RealIP())
	return fail(subsonic.ErrWrongCredential, "wrong username or password")
}

// ---- helpers ----

func ssInt(p url.Values, name string, def int) int {
	if v := p.Get(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func ssBool(p url.Values, name string, def bool) bool {
	if v := p.Get(name); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func ssCount(p url.Values, name string, def int) int {
	return min(max(ssInt(p, name, def), 0), subsonicMaxCount)
}

func ssRequire(p url.Values, name string) (string, error) {
	v := p.Get(name)
	if v == "" {
		return "", subsonic.NewError(subsonic.ErrMissingParam, "required parameter %s is missing", name)
	}
	return v, nil
}

var errSSNotFound = subsonic.NewError(subsonic.ErrNotFound, "not found")

// ssItem loads an item the user may see.
func (s *server) ssItem(c echo.Context, u store.User, id string) (store.Item, error) {
	it, err := s.store.Item(c.Request().Context(), id)
	if err != nil || !canSeeLibrary(u, it.LibraryID) {
		return store.Item{}, errSSNotFound
	}
	return it, nil
}

// ssMusicLibraries is the music libraries the user may see, narrowed to
// musicFolderId when given.
func (s *server) ssMusicLibraries(c echo.Context, u store.User, p url.Values) ([]store.Library, error) {
	libs, err := s.store.Libraries(c.Request().Context())
	if err != nil {
		return nil, err
	}
	want := p.Get("musicFolderId")
	var out []store.Library
	for _, l := range libs {
		if l.MediaType == store.MediaMusic && canSeeLibrary(u, l.ID) && (want == "" || want == l.ID) {
			out = append(out, l)
		}
	}
	return out, nil
}

// ssVisibleTracks keeps the tracks the user may see.
func ssVisibleTracks(u store.User, items []store.Item) []store.Item {
	out := items[:0]
	for _, it := range items {
		if canSeeLibrary(u, it.LibraryID) {
			out = append(out, it)
		}
	}
	return out
}

// artworkURL is the absolute address of an /artwork/ path, which is served
// without sign-in, for the image URLs the artist and album info calls carry.
func artworkURL(c echo.Context, poster string) string {
	if poster == "" {
		return ""
	}
	return c.Scheme() + "://" + c.Request().Host + poster
}

// slicePage cuts offset and count out of a list, as the API's paged calls
// ask for them.
func slicePage[T any](items []T, offset, count int) []T {
	if offset >= len(items) {
		return []T{}
	}
	end := min(len(items), offset+count)
	if count <= 0 {
		end = len(items)
	}
	return items[offset:end]
}

// ssAllArtists is every artist in the libraries, by name.
func (s *server) ssAllArtists(c echo.Context, u store.User, libs []store.Library) ([]subsonic.Artist, error) {
	var out []subsonic.Artist
	for _, lib := range libs {
		for page := 1; ; page++ {
			res, err := s.store.Artists(c.Request().Context(), u.ID, lib.ID, store.MusicQuery{Sort: "name", Page: page, Size: subsonicMaxCount})
			if err != nil {
				return nil, err
			}
			for _, a := range res.Items {
				out = append(out, subsonic.ArtistFrom(a, artworkURL(c, a.Poster)))
			}
			if len(res.Items) < subsonicMaxCount {
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SortName < out[j].SortName })
	return out, nil
}

// ssAlbumsID3 maps albums with their lengths.
func (s *server) ssAlbumsID3(c echo.Context, albums []store.Item) ([]subsonic.AlbumID3, error) {
	ids := make([]string, len(albums))
	for i, a := range albums {
		ids[i] = a.ID
	}
	lengths, err := s.store.AlbumLengths(c.Request().Context(), ids)
	if err != nil {
		return nil, err
	}
	out := make([]subsonic.AlbumID3, 0, len(albums))
	for _, a := range albums {
		out = append(out, subsonic.AlbumFrom(a, lengths[a.ID]))
	}
	return out, nil
}

func songsFrom(tracks []store.Item) []subsonic.Child {
	out := make([]subsonic.Child, 0, len(tracks))
	for _, t := range tracks {
		out = append(out, subsonic.SongFrom(t))
	}
	return out
}

// ---- system ----

func (s *server) ssLicense(echo.Context, store.User, url.Values) (*subsonic.Response, error) {
	r := subsonic.OK()
	r.License = &subsonic.License{Valid: true}
	return r, nil
}

func (s *server) ssExtensions(echo.Context, store.User, url.Values) (*subsonic.Response, error) {
	r := subsonic.OK()
	r.OpenSubsonicExtensions = []subsonic.Extension{
		{Name: "formPost", Versions: []int{1}},
		{Name: "apiKeyAuthentication", Versions: []int{1}},
		{Name: "transcodeOffset", Versions: []int{1}},
		{Name: "songLyrics", Versions: []int{1}},
	}
	return r, nil
}

// ---- browsing ----

func (s *server) ssMusicFolders(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	libs, err := s.ssMusicLibraries(c, u, nil)
	if err != nil {
		return nil, err
	}
	r := subsonic.OK()
	r.MusicFolders = &subsonic.MusicFolders{MusicFolder: []subsonic.MusicFolder{}}
	for _, l := range libs {
		r.MusicFolders.MusicFolder = append(r.MusicFolders.MusicFolder, subsonic.MusicFolder{ID: l.ID, Name: l.Name})
	}
	return r, nil
}

func (s *server) ssIndexes(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	libs, err := s.ssMusicLibraries(c, u, p)
	if err != nil {
		return nil, err
	}
	artists, err := s.ssAllArtists(c, u, libs)
	if err != nil {
		return nil, err
	}
	r := subsonic.OK()
	r.Indexes = &subsonic.Indexes{LastModified: time.Now().UnixMilli(), IgnoredArticles: subsonic.IgnoredArticles,
		Index: subsonic.IndexArtists(artists)}
	return r, nil
}

func (s *server) ssArtists(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	libs, err := s.ssMusicLibraries(c, u, p)
	if err != nil {
		return nil, err
	}
	artists, err := s.ssAllArtists(c, u, libs)
	if err != nil {
		return nil, err
	}
	r := subsonic.OK()
	r.Artists = &subsonic.ArtistsID3{IgnoredArticles: subsonic.IgnoredArticles, Index: subsonic.IndexArtists(artists)}
	return r, nil
}

func (s *server) ssMusicDirectory(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	ctx := c.Request().Context()
	r := subsonic.OK()
	// A music folder's directory is its artists.
	if lib, err := s.store.Library(ctx, id); err == nil && lib.MediaType == store.MediaMusic && canSeeLibrary(u, lib.ID) {
		dir := &subsonic.Directory{ID: lib.ID, Name: lib.Name, Child: []subsonic.Child{}}
		for page := 1; ; page++ {
			res, err := s.store.Artists(ctx, u.ID, lib.ID, store.MusicQuery{Sort: "name", Page: page, Size: subsonicMaxCount})
			if err != nil {
				return nil, err
			}
			for _, a := range res.Items {
				dir.Child = append(dir.Child, subsonic.ArtistChild(a))
			}
			if len(res.Items) < subsonicMaxCount {
				break
			}
		}
		r.Directory = dir
		return r, nil
	}
	it, err := s.ssItem(c, u, id)
	if err != nil {
		return nil, err
	}
	dir := &subsonic.Directory{ID: it.ID, Parent: it.ParentID, Name: it.Title, UserRating: it.UserRating, Child: []subsonic.Child{}}
	switch it.Kind {
	case store.KindArtist:
		res, err := s.store.Albums(ctx, u.ID, it.LibraryID, store.MusicQuery{Artist: it.ID, Sort: "year", Page: 1, Size: subsonicMaxCount})
		if err != nil {
			return nil, err
		}
		for _, a := range res.Items {
			dir.Child = append(dir.Child, subsonic.AlbumChild(a))
		}
	case store.KindAlbum:
		tracks, err := s.store.TracksUnder(ctx, u.ID, it)
		if err != nil {
			return nil, err
		}
		dir.Child = songsFrom(tracks)
	default:
		return nil, errSSNotFound
	}
	r.Directory = dir
	return r, nil
}

func (s *server) ssGenres(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	libs, err := s.ssMusicLibraries(c, u, nil)
	if err != nil {
		return nil, err
	}
	songs, albums := map[string]int{}, map[string]int{}
	var names []string
	for _, lib := range libs {
		counts, err := s.store.LibraryGenres(c.Request().Context(), lib.ID)
		if err != nil {
			return nil, err
		}
		for _, gc := range counts {
			if _, ok := songs[gc.Genre]; !ok {
				names = append(names, gc.Genre)
			}
			songs[gc.Genre] += gc.Tracks
			albums[gc.Genre] += gc.Count
		}
	}
	sort.Strings(names)
	r := subsonic.OK()
	r.Genres = &subsonic.Genres{Genre: []subsonic.Genre{}}
	for _, n := range names {
		r.Genres.Genre = append(r.Genres.Genre, subsonic.Genre{Value: n, SongCount: songs[n], AlbumCount: albums[n]})
	}
	return r, nil
}

func (s *server) ssArtist(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	it, err := s.ssItem(c, u, id)
	if err != nil || it.Kind != store.KindArtist {
		return nil, errSSNotFound
	}
	ctx := c.Request().Context()
	res, err := s.store.Albums(ctx, u.ID, it.LibraryID, store.MusicQuery{Artist: it.ID, Sort: "year", Page: 1, Size: subsonicMaxCount})
	if err != nil {
		return nil, err
	}
	albums, err := s.ssAlbumsID3(c, res.Items)
	if err != nil {
		return nil, err
	}
	it.ChildCount = len(albums)
	r := subsonic.OK()
	r.Artist = &subsonic.ArtistWithAlbumsID3{ArtistID3: subsonic.ArtistFrom(it, artworkURL(c, it.Poster)), Album: albums}
	return r, nil
}

func (s *server) ssAlbum(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	it, err := s.ssItem(c, u, id)
	if err != nil || it.Kind != store.KindAlbum {
		return nil, errSSNotFound
	}
	ctx := c.Request().Context()
	tracks, err := s.store.TracksUnder(ctx, u.ID, it)
	if err != nil {
		return nil, err
	}
	secs := 0
	for _, t := range tracks {
		secs += t.RuntimeSecs
	}
	it.ChildCount = len(tracks)
	if ratings, err := s.store.RatingsFor(ctx, u.ID, []string{it.ID}); err == nil {
		it.UserRating = ratings[it.ID]
	}
	album := subsonic.AlbumFrom(it, secs)
	if genres, err := s.store.GenresForItem(ctx, it.ID); err == nil && len(genres) > 0 {
		album.Genre = genres[0]
	}
	songs := songsFrom(tracks)
	for i := range songs {
		songs[i].ArtistID = it.ParentID
		songs[i].Genre = album.Genre
	}
	r := subsonic.OK()
	r.Album = &subsonic.AlbumWithSongsID3{AlbumID3: album, Song: songs}
	return r, nil
}

func (s *server) ssSong(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	it, err := s.ssItem(c, u, id)
	if err != nil || it.Kind != store.KindTrack {
		return nil, errSSNotFound
	}
	ctx := c.Request().Context()
	if ratings, err := s.store.RatingsFor(ctx, u.ID, []string{it.ID}); err == nil {
		it.UserRating = ratings[it.ID]
	}
	song := subsonic.SongFrom(it)
	if genres, err := s.store.GenresForItem(ctx, it.ID); err == nil && len(genres) > 0 {
		song.Genre = genres[0]
	}
	if album, err := s.store.Item(ctx, it.ParentID); err == nil {
		song.ArtistID = album.ParentID
	}
	r := subsonic.OK()
	r.Song = &song
	return r, nil
}

func (s *server) ssArtistInfo(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	it, err := s.ssItem(c, u, id)
	if err != nil || it.Kind != store.KindArtist {
		return nil, errSSNotFound
	}
	ctx := c.Request().Context()
	info, err := s.store.ArtistInfoFor(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	// Opening an artist starts the picture and biography lookup when due,
	// as the web client's artist page does.
	if s.manager != nil {
		if due, err := s.manager.ArtistLookupDue(ctx, it); err == nil && due {
			s.manager.LookUpArtistInBackground(it)
		}
	}
	mbid := info.MBID
	if mbid == "" {
		mbid = it.MBID
	}
	img := artworkURL(c, it.Poster)
	ai := &subsonic.ArtistInfo{Biography: it.Overview, MusicBrainzID: mbid, SmallImageURL: img, MediumImageURL: img, LargeImageURL: img}
	r := subsonic.OK()
	if strings.TrimSuffix(c.Param("call"), ".view") == "getArtistInfo2" {
		r.ArtistInfo2 = ai
	} else {
		r.ArtistInfo = ai
	}
	return r, nil
}

func (s *server) ssAlbumInfo(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	it, err := s.ssItem(c, u, id)
	if err != nil || it.Kind != store.KindAlbum {
		return nil, errSSNotFound
	}
	img := artworkURL(c, it.Poster)
	r := subsonic.OK()
	r.AlbumInfo = &subsonic.AlbumInfo{MusicBrainzID: it.MBID, SmallImageURL: img, MediumImageURL: img, LargeImageURL: img}
	return r, nil
}

// ---- lists ----

// ssAlbumsAcross lists albums across libraries with one query, in
// library order, and cuts the page asked for. The order across libraries
// is by library, not merged: most households have one music library.
func (s *server) ssAlbumsAcross(c echo.Context, u store.User, libs []store.Library, q store.MusicQuery, offset, count int) ([]store.Item, error) {
	var all []store.Item
	for _, lib := range libs {
		q.Page, q.Size = 1, min(offset+count, 2000)
		if q.Sort == "random" {
			q.Size = count
		}
		res, err := s.store.Albums(c.Request().Context(), u.ID, lib.ID, q)
		if err != nil {
			return nil, err
		}
		all = append(all, res.Items...)
	}
	if q.Sort == "random" {
		rand.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
		return slicePage(all, 0, count), nil
	}
	return slicePage(all, offset, count), nil
}

func (s *server) ssAlbumList(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	kind, err := ssRequire(p, "type")
	if err != nil {
		return nil, err
	}
	libs, err := s.ssMusicLibraries(c, u, p)
	if err != nil {
		return nil, err
	}
	count, offset := ssCount(p, "size", subsonicDefaultCount), max(ssInt(p, "offset", 0), 0)
	var albums []store.Item
	switch kind {
	case "starred":
		albums, err = s.store.RatedItems(c.Request().Context(), u.ID, 5, store.KindAlbum, visibleLibraryIDs(u))
		if err != nil {
			return nil, err
		}
		albums = slicePage(albums, offset, count)
	case "byYear":
		from, to := ssInt(p, "fromYear", 0), ssInt(p, "toYear", 0)
		if from > to {
			from, to = to, from
		}
		all, err := s.ssAlbumsAcross(c, u, libs, store.MusicQuery{Sort: "year"}, 0, 2000)
		if err != nil {
			return nil, err
		}
		for _, a := range all {
			if a.Year >= from && a.Year <= to {
				albums = append(albums, a)
			}
		}
		albums = slicePage(albums, offset, count)
	default:
		q := store.MusicQuery{Sort: map[string]string{
			"random": "random", "newest": "added", "frequent": "plays", "recent": "played", "highest": "rating",
			"alphabeticalByName": "name", "alphabeticalByArtist": "artist", "byGenre": "name",
		}[kind]}
		if q.Sort == "" {
			return nil, subsonic.NewError(subsonic.ErrGeneric, "unknown list type %s", kind)
		}
		if kind == "byGenre" {
			if q.Genre, err = ssRequire(p, "genre"); err != nil {
				return nil, err
			}
		}
		if albums, err = s.ssAlbumsAcross(c, u, libs, q, offset, count); err != nil {
			return nil, err
		}
	}
	r := subsonic.OK()
	if strings.TrimSuffix(c.Param("call"), ".view") == "getAlbumList2" {
		id3, err := s.ssAlbumsID3(c, albums)
		if err != nil {
			return nil, err
		}
		r.AlbumList2 = &subsonic.AlbumList2{Album: id3}
		return r, nil
	}
	list := &subsonic.AlbumList{Album: []subsonic.Child{}}
	for _, a := range albums {
		list.Album = append(list.Album, subsonic.AlbumChild(a))
	}
	r.AlbumList = list
	return r, nil
}

func (s *server) ssTracksAcross(c echo.Context, u store.User, libs []store.Library, q store.MusicQuery, offset, count int) ([]store.Item, error) {
	var all []store.Item
	for _, lib := range libs {
		q.Page, q.Size = 1, min(offset+count, 1000)
		if q.Sort == "random" {
			q.Size = count
		}
		res, err := s.store.Tracks(c.Request().Context(), u.ID, lib.ID, q)
		if err != nil {
			return nil, err
		}
		all = append(all, res.Items...)
	}
	if q.Sort == "random" {
		rand.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
		return slicePage(all, 0, count), nil
	}
	return slicePage(all, offset, count), nil
}

func (s *server) ssRandomSongs(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	libs, err := s.ssMusicLibraries(c, u, p)
	if err != nil {
		return nil, err
	}
	tracks, err := s.ssTracksAcross(c, u, libs, store.MusicQuery{Sort: "random", Genre: p.Get("genre")}, 0, ssCount(p, "size", subsonicDefaultCount))
	if err != nil {
		return nil, err
	}
	r := subsonic.OK()
	r.RandomSongs = &subsonic.Songs{Song: songsFrom(tracks)}
	return r, nil
}

func (s *server) ssSongsByGenre(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	genre, err := ssRequire(p, "genre")
	if err != nil {
		return nil, err
	}
	libs, err := s.ssMusicLibraries(c, u, p)
	if err != nil {
		return nil, err
	}
	tracks, err := s.ssTracksAcross(c, u, libs, store.MusicQuery{Sort: "album", Genre: genre},
		max(ssInt(p, "offset", 0), 0), ssCount(p, "count", subsonicDefaultCount))
	if err != nil {
		return nil, err
	}
	r := subsonic.OK()
	r.SongsByGenre = &subsonic.Songs{Song: songsFrom(tracks)}
	return r, nil
}

func (s *server) ssTopSongs(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	name, err := ssRequire(p, "artist")
	if err != nil {
		return nil, err
	}
	libs, err := s.ssMusicLibraries(c, u, nil)
	if err != nil {
		return nil, err
	}
	r := subsonic.OK()
	r.TopSongs = &subsonic.TopSongs{Song: []subsonic.Child{}}
	ctx := c.Request().Context()
	for _, lib := range libs {
		res, err := s.store.Artists(ctx, u.ID, lib.ID, store.MusicQuery{Query: name, Page: 1, Size: 5})
		if err != nil {
			return nil, err
		}
		for _, a := range res.Items {
			if !strings.EqualFold(a.Title, name) {
				continue
			}
			tracks, err := s.store.ArtistTopTracks(ctx, u.ID, a, ssCount(p, "count", 50))
			if err != nil {
				return nil, err
			}
			r.TopSongs.Song = append(r.TopSongs.Song, songsFrom(tracks)...)
			return r, nil
		}
	}
	return r, nil
}

func (s *server) ssNowPlaying(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	ctx := c.Request().Context()
	r := subsonic.OK()
	r.NowPlaying = &subsonic.NowPlaying{Entry: []subsonic.NowPlayingEntry{}}
	names := map[string]string{}
	for i, pb := range s.playbacks.List() {
		if pb.Kind != string(store.KindTrack) {
			continue
		}
		it, err := s.store.Item(ctx, pb.ItemID)
		if err != nil || !canSeeLibrary(u, it.LibraryID) {
			continue
		}
		name, ok := names[pb.UserID]
		if !ok {
			if who, err := s.store.UserByID(ctx, pb.UserID); err == nil {
				name = who.Username
			}
			names[pb.UserID] = name
		}
		player := pb.Client.Browser
		if player == "" {
			player = "Cineva"
		}
		r.NowPlaying.Entry = append(r.NowPlaying.Entry, subsonic.NowPlayingEntry{
			Child: subsonic.SongFrom(it), Username: name, PlayerID: i + 1, PlayerName: player,
			MinutesAgo: int(time.Since(pb.StartedAt).Minutes()),
		})
	}
	return r, nil
}

func (s *server) ssStarred(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	ctx := c.Request().Context()
	scope := visibleLibraryIDs(u)
	artists, err := s.store.RatedItems(ctx, u.ID, 5, store.KindArtist, scope)
	if err != nil {
		return nil, err
	}
	albums, err := s.store.RatedItems(ctx, u.ID, 5, store.KindAlbum, scope)
	if err != nil {
		return nil, err
	}
	tracks, err := s.store.RatedItems(ctx, u.ID, 5, store.KindTrack, scope)
	if err != nil {
		return nil, err
	}
	return s.ssResultSets(c, strings.TrimSuffix(c.Param("call"), ".view") == "getStarred2", artists, albums, tracks)
}

// ssResultSets builds a starred or search result in the folder-style or
// ID3 shape, which share their elements.
func (s *server) ssResultSets(c echo.Context, id3 bool, artists, albums, tracks []store.Item) (*subsonic.Response, error) {
	r := subsonic.OK()
	call := strings.TrimSuffix(c.Param("call"), ".view")
	arts := make([]subsonic.Artist, 0, len(artists))
	for _, a := range artists {
		arts = append(arts, subsonic.ArtistFrom(a, artworkURL(c, a.Poster)))
	}
	songs := songsFrom(tracks)
	if id3 {
		id3Albums, err := s.ssAlbumsID3(c, albums)
		if err != nil {
			return nil, err
		}
		set := &subsonic.Starred2{Artist: arts, Album: id3Albums, Song: songs}
		if strings.HasPrefix(call, "search") {
			r.SearchResult3 = set
		} else {
			r.Starred2 = set
		}
		return r, nil
	}
	children := make([]subsonic.Child, 0, len(albums))
	for _, a := range albums {
		children = append(children, subsonic.AlbumChild(a))
	}
	set := &subsonic.Starred{Artist: arts, Album: children, Song: songs}
	if strings.HasPrefix(call, "search") {
		r.SearchResult2 = set
	} else {
		r.Starred = set
	}
	return r, nil
}

func (s *server) ssSearch(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	query := strings.Trim(p.Get("query"), `"`)
	libs, err := s.ssMusicLibraries(c, u, p)
	if err != nil {
		return nil, err
	}
	ctx := c.Request().Context()
	var artists, albums, tracks []store.Item
	ac, ao := ssCount(p, "artistCount", subsonicSearchCount), max(ssInt(p, "artistOffset", 0), 0)
	alc, alo := ssCount(p, "albumCount", subsonicSearchCount), max(ssInt(p, "albumOffset", 0), 0)
	sc, so := ssCount(p, "songCount", subsonicSearchCount), max(ssInt(p, "songOffset", 0), 0)
	for _, lib := range libs {
		if ac > 0 {
			res, err := s.store.Artists(ctx, u.ID, lib.ID, store.MusicQuery{Query: query, Page: 1, Size: ao + ac})
			if err != nil {
				return nil, err
			}
			artists = append(artists, res.Items...)
		}
		if alc > 0 {
			res, err := s.store.Albums(ctx, u.ID, lib.ID, store.MusicQuery{Query: query, Page: 1, Size: alo + alc})
			if err != nil {
				return nil, err
			}
			albums = append(albums, res.Items...)
		}
		if sc > 0 {
			res, err := s.store.Tracks(ctx, u.ID, lib.ID, store.MusicQuery{Query: query, Page: 1, Size: so + sc})
			if err != nil {
				return nil, err
			}
			tracks = append(tracks, res.Items...)
		}
	}
	return s.ssResultSets(c, strings.TrimSuffix(c.Param("call"), ".view") == "search3",
		slicePage(artists, ao, ac), slicePage(albums, alo, alc), slicePage(tracks, so, sc))
}

// ---- playlists ----

func (s *server) ssPlaylists(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	lists, err := s.store.Playlists(c.Request().Context(), u.ID, visibleLibraryIDs(u))
	if err != nil {
		return nil, err
	}
	r := subsonic.OK()
	r.Playlists = &subsonic.Playlists{Playlist: []subsonic.Playlist{}}
	for _, pl := range lists {
		r.Playlists.Playlist = append(r.Playlists.Playlist, subsonic.PlaylistFrom(pl, u.Username))
	}
	return r, nil
}

func (s *server) ssPlaylistWithSongs(c echo.Context, u store.User, id string) (*subsonic.Response, error) {
	pid, ok := subsonic.PlaylistID(id)
	if !ok {
		pid = id
	}
	pl, tracks, err := s.store.PlaylistWithTracks(c.Request().Context(), u.ID, pid, visibleLibraryIDs(u))
	if errors.Is(err, store.ErrNotFound) {
		return nil, errSSNotFound
	}
	if err != nil {
		return nil, err
	}
	r := subsonic.OK()
	r.Playlist = &subsonic.PlaylistWithSongs{Playlist: subsonic.PlaylistFrom(pl, u.Username), Entry: songsFrom(tracks)}
	return r, nil
}

func (s *server) ssPlaylist(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	return s.ssPlaylistWithSongs(c, u, id)
}

// ssTrackIDs expands ids (tracks, albums, artists) into the tracks the
// user may see.
func (s *server) ssTrackIDs(c echo.Context, u store.User, ids []string) ([]string, error) {
	tracks, err := s.store.ExpandTracks(c.Request().Context(), ids)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(tracks))
	for _, t := range ssVisibleTracks(u, tracks) {
		out = append(out, t.ID)
	}
	return out, nil
}

func (s *server) ssCreatePlaylist(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	ctx := c.Request().Context()
	ids, err := s.ssTrackIDs(c, u, p["songId"])
	if err != nil {
		return nil, err
	}
	// With playlistId the call replaces an existing playlist's songs.
	if existing := p.Get("playlistId"); existing != "" {
		pid, ok := subsonic.PlaylistID(existing)
		if !ok {
			pid = existing
		}
		if err := s.store.SetPlaylistTracks(ctx, u.ID, pid, ids); err != nil {
			return nil, ssPlaylistError(err)
		}
		if name := p.Get("name"); name != "" {
			if err := s.store.UpdatePlaylist(ctx, u.ID, pid, &name, nil); err != nil {
				return nil, ssPlaylistError(err)
			}
		}
		return s.ssPlaylistWithSongs(c, u, pid)
	}
	name, err := ssRequire(p, "name")
	if err != nil {
		return nil, err
	}
	pl, err := s.store.CreatePlaylist(ctx, u.ID, name, nil)
	if err != nil {
		return nil, ssPlaylistError(err)
	}
	if len(ids) > 0 {
		if err := s.store.SetPlaylistTracks(ctx, u.ID, pl.ID, ids); err != nil {
			return nil, ssPlaylistError(err)
		}
	}
	return s.ssPlaylistWithSongs(c, u, pl.ID)
}

func ssPlaylistError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errSSNotFound
	case errors.Is(err, store.ErrInvalidPlaylist):
		return subsonic.NewError(subsonic.ErrNotAuthorised, "%v", err)
	}
	return err
}

func (s *server) ssUpdatePlaylist(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "playlistId")
	if err != nil {
		return nil, err
	}
	pid, ok := subsonic.PlaylistID(id)
	if !ok {
		pid = id
	}
	ctx := c.Request().Context()
	pl, tracks, err := s.store.PlaylistWithTracks(ctx, u.ID, pid, visibleLibraryIDs(u))
	if err != nil {
		return nil, ssPlaylistError(err)
	}
	if name := p.Get("name"); name != "" && name != pl.Name {
		if err := s.store.UpdatePlaylist(ctx, u.ID, pid, &name, nil); err != nil {
			return nil, ssPlaylistError(err)
		}
	}
	if pl.Smart != nil {
		if len(p["songIdToAdd"]) > 0 || len(p["songIndexToRemove"]) > 0 {
			return nil, subsonic.NewError(subsonic.ErrNotAuthorised, "a smart playlist's songs come from its rules")
		}
		return subsonic.OK(), nil
	}
	ids := make([]string, 0, len(tracks))
	for _, t := range tracks {
		ids = append(ids, t.ID)
	}
	if removes := p["songIndexToRemove"]; len(removes) > 0 {
		drop := map[int]bool{}
		for _, r := range removes {
			if n, err := strconv.Atoi(r); err == nil {
				drop[n] = true
			}
		}
		kept := ids[:0]
		for i, id := range ids {
			if !drop[i] {
				kept = append(kept, id)
			}
		}
		ids = kept
	}
	if adds := p["songIdToAdd"]; len(adds) > 0 {
		more, err := s.ssTrackIDs(c, u, adds)
		if err != nil {
			return nil, err
		}
		ids = append(ids, more...)
	}
	if err := s.store.SetPlaylistTracks(ctx, u.ID, pid, ids); err != nil {
		return nil, ssPlaylistError(err)
	}
	return subsonic.OK(), nil
}

func (s *server) ssDeletePlaylist(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	pid, ok := subsonic.PlaylistID(id)
	if !ok {
		pid = id
	}
	if err := s.store.DeletePlaylist(c.Request().Context(), u.ID, pid); err != nil {
		return nil, ssPlaylistError(err)
	}
	return subsonic.OK(), nil
}

// ---- media ----

// ssQualityFor picks the transcode bit rate step at or under maxBitRate,
// the default when none is asked for.
func ssQualityFor(maxBitRate int) int {
	if maxBitRate <= 0 {
		return 192
	}
	best := 64
	for q := range allowedBitrates {
		if q <= maxBitRate && q > best {
			best = q
		}
	}
	return best
}

var ssTranscodeFormats = map[string]bool{"mp3": true, "opus": true, "aac": true, "ogg": true}

func (s *server) ssStream(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	it, err := s.ssItem(c, u, id)
	if err != nil || it.Kind != store.KindTrack {
		return nil, errSSNotFound
	}
	format := strings.ToLower(p.Get("format"))
	maxBitRate := ssInt(p, "maxBitRate", 0)
	// A bit rate cap the file already meets is no reason to transcode; one
	// it does not is, in the default format when the client named none.
	transcode := ssTranscodeFormats[format]
	if !transcode && format != "raw" && maxBitRate > 0 && (it.Bitrate == 0 || it.Bitrate > maxBitRate) {
		transcode, format = true, "mp3"
	}
	if transcode {
		quality := ssQualityFor(maxBitRate)
		path, _, err := s.transcodedTrack(c.Request().Context(), it, format, quality)
		if err != nil {
			return nil, err
		}
		s.registerPlayback(c, it, "transcode", format+" "+strconv.Itoa(quality)+"k", "")
		if err := c.File(path); err != nil {
			return nil, err
		}
		return nil, errSubsonicWritten
	}
	s.registerPlayback(c, it, "direct", "original", "")
	if err := s.serveMedia(c, it, it.Path, ""); err != nil {
		return nil, err
	}
	return nil, errSubsonicWritten
}

func (s *server) ssDownload(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	it, err := s.ssItem(c, u, id)
	if err != nil {
		return nil, err
	}
	switch it.Kind {
	case store.KindTrack:
		err = s.serveMedia(c, it, it.Path, filepath.Base(it.Path))
	case store.KindAlbum, store.KindArtist:
		err = s.downloadArchive(c, it, "", 0)
	default:
		return nil, errSSNotFound
	}
	if err != nil {
		return nil, err
	}
	return nil, errSubsonicWritten
}

// ssArtworkFile is the file behind an /artwork/ path.
func (s *server) ssArtworkFile(poster string) (string, bool) {
	name, ok := strings.CutPrefix(poster, "/artwork/")
	if !ok || s.artworkDir == "" {
		return "", false
	}
	name, _, _ = strings.Cut(name, "?")
	name = filepath.Base(name)
	if name == "." || name == "" {
		return "", false
	}
	return filepath.Join(s.artworkDir, name), true
}

func (s *server) ssCoverArt(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	ctx := c.Request().Context()
	var poster string
	if pid, ok := subsonic.PlaylistID(id); ok {
		lists, err := s.store.Playlists(ctx, u.ID, visibleLibraryIDs(u))
		if err != nil {
			return nil, err
		}
		for _, pl := range lists {
			if pl.ID == pid && len(pl.Covers) > 0 {
				poster = pl.Covers[0]
			}
		}
	} else if it, err := s.ssItem(c, u, id); err == nil {
		poster = it.Poster
	}
	path, ok := s.ssArtworkFile(poster)
	if !ok {
		return nil, errSSNotFound
	}
	if err := c.File(path); err != nil {
		return nil, errSSNotFound
	}
	return nil, errSubsonicWritten
}

func (s *server) ssAvatar(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	name := p.Get("username")
	who := u
	if name != "" && !strings.EqualFold(name, u.Username) {
		other, err := s.store.UserByUsername(c.Request().Context(), name)
		if err != nil {
			return nil, errSSNotFound
		}
		who = other
	}
	path, ok := s.ssArtworkFile(who.Avatar)
	if !ok {
		return nil, errSSNotFound
	}
	if err := c.File(path); err != nil {
		return nil, errSSNotFound
	}
	return nil, errSubsonicWritten
}

func (s *server) ssLyrics(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	artist, title := p.Get("artist"), p.Get("title")
	r := subsonic.OK()
	r.Lyrics = &subsonic.Lyrics{Artist: artist, Title: title}
	if title == "" {
		return r, nil
	}
	libs, err := s.ssMusicLibraries(c, u, nil)
	if err != nil {
		return nil, err
	}
	ctx := c.Request().Context()
	for _, lib := range libs {
		res, err := s.store.Tracks(ctx, u.ID, lib.ID, store.MusicQuery{Query: strings.TrimSpace(title + " " + artist), Page: 1, Size: 20})
		if err != nil {
			return nil, err
		}
		for _, t := range res.Items {
			if !strings.EqualFold(t.Title, title) || (artist != "" && !strings.EqualFold(t.Artist, artist)) {
				continue
			}
			if l, err := s.store.LyricsFor(ctx, t.ID); err == nil && l != nil {
				r.Lyrics.Value = subsonic.PlainLyrics(*l)
				return r, nil
			}
		}
	}
	return r, nil
}

func (s *server) ssLyricsBySongID(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	it, err := s.ssItem(c, u, id)
	if err != nil || it.Kind != store.KindTrack {
		return nil, errSSNotFound
	}
	l, err := s.store.LyricsFor(c.Request().Context(), it.ID)
	if err != nil {
		return nil, err
	}
	r := subsonic.OK()
	r.LyricsList = &subsonic.LyricsList{StructuredLyrics: []subsonic.StructuredLyrics{}}
	if l != nil {
		r.LyricsList.StructuredLyrics = append(r.LyricsList.StructuredLyrics, subsonic.LyricsFrom(*l, it.Artist, it.Title))
	}
	return r, nil
}

// ---- annotation ----

// ssRateAll sets a rating on every id the call names (id, albumId and
// artistId alike) that the user may see.
func (s *server) ssRateAll(c echo.Context, u store.User, p url.Values, stars int) (*subsonic.Response, error) {
	ctx := c.Request().Context()
	ids := append(append(append([]string{}, p["id"]...), p["albumId"]...), p["artistId"]...)
	if len(ids) == 0 {
		return nil, subsonic.NewError(subsonic.ErrMissingParam, "required parameter id is missing")
	}
	for _, id := range ids {
		it, err := s.ssItem(c, u, id)
		if err != nil {
			continue
		}
		if err := s.store.SetRating(ctx, u.ID, it.ID, stars); err != nil {
			return nil, err
		}
		s.events.Publish(events.Event{Type: "rating", User: u.ID, Data: map[string]any{"itemId": it.ID, "stars": stars}})
	}
	return subsonic.OK(), nil
}

// star and unstar map onto a five-star rating and clearing it: Cineva has
// stars and no separate favourite flag (server/docs/subsonic.md).
func (s *server) ssStar(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	return s.ssRateAll(c, u, p, 5)
}

func (s *server) ssUnstar(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	return s.ssRateAll(c, u, p, 0)
}

func (s *server) ssSetRating(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	id, err := ssRequire(p, "id")
	if err != nil {
		return nil, err
	}
	stars := ssInt(p, "rating", -1)
	if stars < 0 || stars > 5 {
		return nil, subsonic.NewError(subsonic.ErrMissingParam, "rating must be 0 to 5")
	}
	it, err := s.ssItem(c, u, id)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetRating(c.Request().Context(), u.ID, it.ID, stars); err != nil {
		return nil, err
	}
	s.events.Publish(events.Event{Type: "rating", User: u.ID, Data: map[string]any{"itemId": it.ID, "stars": stars}})
	return subsonic.OK(), nil
}

// scrobble with submission=true counts a play of each song: the client
// says it was played, so the half-way rule our own player uses does not
// apply. submission=false is the now-playing report, which registers the
// playback for Activity and "Continue here".
func (s *server) ssScrobble(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	ids := p["id"]
	if len(ids) == 0 {
		return nil, subsonic.NewError(subsonic.ErrMissingParam, "required parameter id is missing")
	}
	ctx := c.Request().Context()
	submit := ssBool(p, "submission", true)
	for _, id := range ids {
		it, err := s.ssItem(c, u, id)
		if err != nil || it.Kind != store.KindTrack {
			continue
		}
		if !submit {
			s.registerPlayback(c, it, "direct", "original", "")
			continue
		}
		eventID, err := s.store.RecordPlay(ctx, u.ID, it.ID, it.Kind)
		if err != nil {
			return nil, err
		}
		if err := s.store.FinishPlay(ctx, eventID, it.RuntimeSecs, true, true); err != nil {
			return nil, err
		}
		subsonicLog.Debug("scrobbled", "user", u.Username, "title", it.Title, "client", p.Get("c"))
	}
	if submit {
		s.events.Publish(events.Event{Type: "plays"})
	}
	return subsonic.OK(), nil
}

// ---- users and scans ----

func (s *server) ssUserFrom(c echo.Context, u store.User) (subsonic.User, error) {
	libs, err := s.ssMusicLibraries(c, u, nil)
	if err != nil {
		return subsonic.User{}, err
	}
	out := subsonic.User{
		Username: u.Username, ScrobblingEnabled: true, AdminRole: u.IsAdmin(), SettingsRole: false,
		DownloadRole: true, UploadRole: false, PlaylistRole: true, CoverArtRole: true, CommentRole: false,
		PodcastRole: false, StreamRole: true, JukeboxRole: false, ShareRole: false, VideoConversionRole: false,
		Folder: []string{},
	}
	for _, l := range libs {
		out.Folder = append(out.Folder, l.ID)
	}
	return out, nil
}

func (s *server) ssUser(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	name := p.Get("username")
	who := u
	if name != "" && !strings.EqualFold(name, u.Username) {
		if !u.IsAdmin() {
			return nil, subsonic.NewError(subsonic.ErrNotAuthorised, "only an administrator may see other users")
		}
		other, err := s.store.UserByUsername(c.Request().Context(), name)
		if err != nil {
			return nil, errSSNotFound
		}
		who = other
	}
	su, err := s.ssUserFrom(c, who)
	if err != nil {
		return nil, err
	}
	r := subsonic.OK()
	r.User = &su
	return r, nil
}

func (s *server) ssUsers(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	if !u.IsAdmin() {
		return nil, subsonic.NewError(subsonic.ErrNotAuthorised, "only an administrator may list users")
	}
	users, err := s.store.Users(c.Request().Context())
	if err != nil {
		return nil, err
	}
	r := subsonic.OK()
	r.Users = &subsonic.Users{User: []subsonic.User{}}
	for _, who := range users {
		su, err := s.ssUserFrom(c, who)
		if err != nil {
			return nil, err
		}
		r.Users.User = append(r.Users.User, su)
	}
	return r, nil
}

func (s *server) ssScanStatus(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	libs, err := s.ssMusicLibraries(c, u, nil)
	if err != nil {
		return nil, err
	}
	st := &subsonic.ScanStatus{}
	for _, lib := range libs {
		if s.manager != nil && s.manager.Scanning(lib.ID) {
			st.Scanning = true
		}
		if n, err := s.store.CountItems(c.Request().Context(), lib.ID, store.KindTrack); err == nil {
			st.Count += int64(n)
		}
	}
	r := subsonic.OK()
	r.ScanStatus = st
	return r, nil
}

func (s *server) ssStartScan(c echo.Context, u store.User, p url.Values) (*subsonic.Response, error) {
	if !u.IsAdmin() {
		return nil, subsonic.NewError(subsonic.ErrNotAuthorised, "only an administrator may start a scan")
	}
	libs, err := s.ssMusicLibraries(c, u, nil)
	if err != nil {
		return nil, err
	}
	if s.manager != nil {
		for _, lib := range libs {
			if _, err := s.manager.StartScan(c.Request().Context(), lib, false); err != nil {
				subsonicLog.Debug("scan not started", "library", lib.Name, "err", err)
			}
		}
	}
	return s.ssScanStatus(c, u, p)
}
