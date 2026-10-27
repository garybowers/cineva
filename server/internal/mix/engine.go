// Package mix grows a playlist from a seed: a track, an album or an
// artist, and later a playlist or a genre (server/docs/mix.md). The server
// decides and the clients show, so the web client and the native apps get
// the same mix for the same seed.
//
// A mix is scored from signals the library and the household already hold
// plus two open ListenBrainz sources, cached: which artists are alike, and
// which of an artist's songs the world listens to. The signals are small
// functions over the store; the score is their weighted sum; the top is
// then diversified so no artist takes over, and salted so the same seed
// does not give the same mix twice.
package mix

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/store"
)

var logger = logging.For("mix")

// Weights of the signals, on scores each in [0, 1] (the rating runs
// -1 to 1). Similarity leads, since it is what makes a mix a mix rather
// than a shuffle; the artist's best-known songs next, so a mix picks
// Dancing Queen before a B-side; the household's own listening is worth
// as much as similarity when both agree; genre and era only nudge.
const (
	wSimilar   = 3.0
	wTopSong   = 2.0
	wOwnPlays  = 1.5
	wHousehold = 0.75
	wRating    = 1.0
	wCoListen  = 1.5
	wGenre     = 0.75
	wEra       = 0.25
	wJitter    = 0.5
)

const (
	// DefaultSize and MaxSize bound a batch.
	DefaultSize = 20
	MaxSize     = 50
	// MaxExclude is how many already-queued ids a request may name.
	MaxExclude = 1000
	// cacheFor is how long ListenBrainz's answers are trusted.
	cacheFor = 90 * 24 * time.Hour
	// recentFor keeps what the viewer just played out of a mix.
	recentFor = 3 * time.Hour
	// coListenFor is how far back co-listening looks (history retention
	// is shorter in practice).
	coListenFor = 365 * 24 * time.Hour
	// maxSimilar caps how many similar artists are looked for locally.
	maxSimilar = 40
	// topFetch is how many similar artists get their top songs fetched
	// in the background on a first mix, so a first mix costs minutes of
	// requests at most, not an hour.
	topFetch = 10
	// minRuntime is the shortest track a mix will pick, in seconds.
	minRuntime = 30
	// perArtistWindow and perArtistMax: at most perArtistMax tracks by one
	// artist in any perArtistWindow picks.
	perArtistWindow = 20
	perArtistMax    = 2
	// seedAlbumMax is how many of the seed's own album may appear in the
	// first perArtistWindow of a mix carried on from a track.
	seedAlbumMax = 2
	// eraYears is how far from the seed's year a track still counts as
	// the same era.
	eraYears = 10
)

// Basis names the signals a mix had, for the client to say why.
const (
	BasisSimilar   = "similar-artists"
	BasisTopSongs  = "top-songs"
	BasisHousehold = "household"
	BasisCoListen  = "co-listening"
	BasisGenre     = "genre"
)

// Engine makes mixes. Store and Source are required; LookupArtist, when
// set, is asked to find a MusicBrainz id for an artist that has none (the
// library's artist lookup, which runs in the background).
type Engine struct {
	Store  *store.Store
	Source Source
	// LookupArtist starts a background lookup of an artist's details and
	// reports whether it did; nil means no lookups.
	LookupArtist func(artist store.Item) bool
	// Now and Rand are replaced by tests.
	Now  func() time.Time
	Rand func() float64

	fetching sync.Map // mbid -> true while a background fetch runs
	// tokenRefusedAt is when the source last refused for want of a user
	// token; top-song fetches pause for an hour after it rather than
	// failing on every mix.
	tokenRefusedAt atomic.Int64
}

// New makes an engine over the store and the source.
func New(st *store.Store, src Source) *Engine {
	return &Engine{Store: st, Source: src, Now: time.Now, Rand: rand.Float64}
}

// Options shape one batch.
type Options struct {
	// Size is how many tracks; clamped to 1..MaxSize, DefaultSize when 0.
	Size int
	// Exclude is what the queue already holds, never picked again.
	Exclude []string
	// After is the track the listener reached, so a long mix drifts from
	// where it is rather than from the seed alone. Optional.
	After *store.Item
	// Start says this is the first batch of a mix started on purpose: it
	// opens with the seed's best (a track first; an album's best spread
	// through; an artist's best-known leading).
	Start bool
	// LibraryIDs is what the viewer may see: nil for everything, an empty
	// list for nothing.
	LibraryIDs []string
}

// Result is one batch.
type Result struct {
	Seed   store.Item   `json:"seed"`
	Basis  []string     `json:"basis"`
	Tracks []store.Item `json:"tracks"`
}

// ErrSeedKind says the seed is not something a mix can grow from.
var ErrSeedKind = errors.New("mix: the seed must be a track, an album or an artist")

// seed is the seed resolved: its artist, album and genres.
type seed struct {
	item   store.Item
	artist store.Item // the album artist for a track or album
	album  store.Item // the album, for a track or album seed
	genres []string
	year   int
}

// candidate is a track with everything the score needs.
type candidate struct {
	track    store.Item
	artistID string // the album artist's item id
	sim      float64
	top      float64
	own      float64
	house    float64
	rating   float64
	colisten float64
	genre    float64
	era      float64
	score    float64
}

// Mix grows one batch for the viewer from the seed.
func (e *Engine) Mix(ctx context.Context, userID string, item store.Item, o Options) (Result, error) {
	if o.Size <= 0 {
		o.Size = DefaultSize
	}
	o.Size = min(o.Size, MaxSize)
	sd, err := e.resolveSeed(ctx, item)
	if err != nil {
		return Result{}, err
	}
	basis := map[string]bool{}

	// The artists a mix draws from: the seed's, and the ones alike.
	seedArtists := []store.Item{sd.artist}
	if o.After != nil {
		if after, err := e.resolveSeed(ctx, *o.After); err == nil && after.artist.ID != "" && after.artist.ID != sd.artist.ID {
			seedArtists = append(seedArtists, after.artist)
		}
	}
	simByArtist, similarNames := e.similarArtists(ctx, seedArtists)
	similar := []store.Item{}
	if len(simByArtist) > 0 {
		var mbids []string
		for mbid := range simByArtist {
			mbids = append(mbids, mbid)
		}
		found, err := e.Store.ArtistsByMBIDs(ctx, o.LibraryIDs, mbids)
		if err != nil {
			return Result{}, err
		}
		matched := map[string]bool{}
		for _, a := range found {
			matched[strings.ToLower(a.Title)] = true
		}
		var names []string
		for _, name := range similarNames {
			if !matched[strings.ToLower(name)] && name != "" {
				names = append(names, name)
			}
		}
		byName, err := e.Store.ArtistsByNames(ctx, o.LibraryIDs, names)
		if err != nil {
			return Result{}, err
		}
		similar = append(found, byName...)
	}
	// Each local artist's similarity, normalised to the best match.
	simScore := map[string]float64{}
	maxScore := 0.0
	for _, s := range simByArtist {
		maxScore = math.Max(maxScore, float64(s))
	}
	nameScore := map[string]float64{}
	for mbid, s := range simByArtist {
		nameScore[strings.ToLower(similarNames[mbid])] = float64(s)
	}
	for _, a := range similar {
		s := 0.0
		if mbid, _ := e.Store.ArtistMBID(ctx, a); mbid != "" {
			s = float64(simByArtist[mbid])
		}
		if s == 0 {
			s = nameScore[strings.ToLower(a.Title)]
		}
		if maxScore > 0 {
			simScore[a.ID] = s / maxScore
		}
	}
	sort.SliceStable(similar, func(i, j int) bool { return simScore[similar[i].ID] > simScore[similar[j].ID] })
	if len(similar) > maxSimilar {
		similar = similar[:maxSimilar]
	}
	if len(similar) > 0 {
		basis[BasisSimilar] = true
	}

	// The candidate pool: the seed artists' own tracks and the similar
	// artists'; without any similar artists, what the household played
	// beside the seed and the seed's genres and era fill in.
	artistIDs := []string{}
	for _, a := range seedArtists {
		artistIDs = append(artistIDs, a.ID)
	}
	for _, a := range similar {
		artistIDs = append(artistIDs, a.ID)
	}
	pool, err := e.Store.ArtistTracks(ctx, userID, artistIDs)
	if err != nil {
		return Result{}, err
	}
	since := e.Now().Add(-coListenFor).UTC().Format(time.DateTime)
	colisten, err := e.Store.CoListened(ctx, sd.artist.ID, since, 200)
	if err != nil {
		return Result{}, err
	}
	if len(similar) == 0 {
		seen := map[string]bool{}
		for _, t := range pool {
			seen[t.ID] = true
		}
		var ids []string
		for id := range colisten {
			if !seen[id] {
				ids = append(ids, id)
			}
		}
		extra, err := e.Store.TracksByIDs(ctx, userID, ids)
		if err != nil {
			return Result{}, err
		}
		pool = append(pool, extra...)
		if len(sd.genres) > 0 {
			from, to := 0, 0
			if sd.year > 0 {
				from, to = sd.year-eraYears, sd.year+eraYears
			}
			byGenre, err := e.Store.TracksByGenres(ctx, userID, o.LibraryIDs, sd.genres, from, to, 300)
			if err != nil {
				return Result{}, err
			}
			for _, t := range byGenre {
				if !seen[t.ID] {
					pool = append(pool, t)
					seen[t.ID] = true
				}
			}
			basis[BasisGenre] = true
		}
	}
	// Visibility: a track from a library the viewer cannot see is out,
	// whatever brought it in.
	if o.LibraryIDs != nil {
		visible := map[string]bool{}
		for _, id := range o.LibraryIDs {
			visible[id] = true
		}
		pool = slices.DeleteFunc(pool, func(t store.Item) bool { return !visible[t.LibraryID] })
	}

	// The signals over the pool.
	ids := make([]string, 0, len(pool))
	for _, t := range pool {
		ids = append(ids, t.ID)
	}
	house, err := e.Store.HouseholdPlays(ctx, ids)
	if err != nil {
		return Result{}, err
	}
	recent, err := e.Store.RecentlyPlayedIDs(ctx, userID, e.Now().Add(-recentFor).UTC().Format(time.DateTime))
	if err != nil {
		return Result{}, err
	}
	genres, err := e.Store.GenresFor(ctx, ids)
	if err != nil {
		return Result{}, err
	}
	topRank := e.topRanks(ctx, append(seedArtists, similar...))
	excluded := map[string]bool{}
	for _, id := range o.Exclude {
		excluded[id] = true
	}
	albumArtist, err := e.albumArtists(ctx, pool) // album id -> artist id
	if err != nil {
		return Result{}, err
	}

	maxOwn, maxHouse, maxCo := 0.0, 0.0, 0.0
	for _, t := range pool {
		maxOwn = math.Max(maxOwn, float64(t.PlayCount))
		maxHouse = math.Max(maxHouse, float64(house[t.ID]))
		maxCo = math.Max(maxCo, float64(colisten[t.ID]))
	}
	seedGenres := map[string]bool{}
	for _, g := range sd.genres {
		seedGenres[strings.ToLower(g)] = true
	}
	recordings := map[string]bool{} // the same recording twice is never
	var cands []candidate
	for _, t := range pool {
		if t.RuntimeSecs > 0 && t.RuntimeSecs < minRuntime {
			continue
		}
		if excluded[t.ID] || recent[t.ID] || t.UserRating == 1 {
			continue
		}
		// The seed itself only leads a mix started on purpose from a track.
		if t.ID == sd.item.ID && (!o.Start || sd.item.Kind != store.KindTrack) {
			continue
		}
		key := t.MBID
		if key == "" {
			key = strings.ToLower(store.Fold(t.Artist)) + "|" + strings.ToLower(store.Fold(t.Title))
		}
		if recordings[key] {
			continue
		}
		recordings[key] = true
		c := candidate{track: t, artistID: albumArtist[t.ParentID]}
		if c.artistID == sd.artist.ID || isSeedArtist(seedArtists, c.artistID) {
			c.sim = 1
		} else {
			c.sim = simScore[c.artistID]
		}
		if r := topRank[c.artistID][t.MBID]; r > 0 {
			c.top = 1 / (1 + math.Log(float64(r)))
		} else if r := topRank[c.artistID]["title:"+strings.ToLower(store.Fold(t.Title))]; r > 0 {
			c.top = 1 / (1 + math.Log(float64(r)))
		}
		if maxOwn > 0 {
			c.own = math.Log1p(float64(t.PlayCount)) / math.Log1p(maxOwn)
		}
		if maxHouse > 0 {
			c.house = math.Log1p(float64(house[t.ID])) / math.Log1p(maxHouse)
		}
		if t.UserRating > 0 {
			c.rating = float64(t.UserRating-3) / 2
		}
		if maxCo > 0 {
			c.colisten = math.Log1p(float64(colisten[t.ID])) / math.Log1p(maxCo)
		}
		if len(seedGenres) > 0 {
			hits := 0
			for _, g := range genres[t.ID] {
				if seedGenres[strings.ToLower(g)] {
					hits++
				}
			}
			c.genre = float64(hits) / float64(len(seedGenres))
		}
		if sd.year > 0 && t.Year > 0 && absInt(t.Year-sd.year) <= eraYears {
			c.era = 1
		}
		c.score = wSimilar*c.sim + wTopSong*c.top + wOwnPlays*c.own + wHousehold*c.house +
			wRating*c.rating + wCoListen*c.colisten + wGenre*c.genre + wEra*c.era + wJitter*e.Rand()
		if c.top > 0 {
			basis[BasisTopSongs] = true
		}
		if c.own > 0 || c.house > 0 || c.rating != 0 {
			basis[BasisHousehold] = true
		}
		if c.colisten > 0 {
			basis[BasisCoListen] = true
		}
		if c.genre > 0 && len(similar) == 0 {
			basis[BasisGenre] = true
		}
		cands = append(cands, c)
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })

	tracks := e.arrange(sd, seedArtists, cands, o, topRank)
	out := Result{Seed: sd.item, Tracks: tracks, Basis: []string{}}
	for _, b := range []string{BasisSimilar, BasisTopSongs, BasisHousehold, BasisCoListen, BasisGenre} {
		if basis[b] {
			out.Basis = append(out.Basis, b)
		}
	}
	return out, nil
}

func isSeedArtist(seeds []store.Item, id string) bool {
	for _, a := range seeds {
		if a.ID == id {
			return true
		}
	}
	return false
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// resolveSeed finds the seed's artist, album, genres and year.
func (e *Engine) resolveSeed(ctx context.Context, item store.Item) (seed, error) {
	sd := seed{item: item, year: item.Year}
	switch item.Kind {
	case store.KindArtist:
		sd.artist = item
	case store.KindAlbum:
		sd.album = item
		a, err := e.Store.ItemOrNone(ctx, item.ParentID)
		if err != nil {
			return seed{}, err
		}
		sd.artist = a
	case store.KindTrack:
		al, err := e.Store.ItemOrNone(ctx, item.ParentID)
		if err != nil {
			return seed{}, err
		}
		sd.album = al
		a, err := e.Store.ItemOrNone(ctx, al.ParentID)
		if err != nil {
			return seed{}, err
		}
		sd.artist = a
		if sd.year == 0 {
			sd.year = al.Year
		}
	default:
		return seed{}, ErrSeedKind
	}
	if sd.artist.ID == "" {
		return seed{}, ErrSeedKind
	}
	ids := []string{item.ID}
	if sd.album.ID != "" {
		ids = append(ids, sd.album.ID)
	}
	genres, err := e.Store.GenresFor(ctx, ids)
	if err != nil {
		return seed{}, err
	}
	seen := map[string]bool{}
	for _, id := range ids {
		for _, g := range genres[id] {
			if !seen[strings.ToLower(g)] {
				seen[strings.ToLower(g)] = true
				sd.genres = append(sd.genres, g)
			}
		}
	}
	return sd, nil
}

// similarArtists is the union of the seed artists' similar artists from
// the cache, by MusicBrainz id with the best score, and their names. An
// artist without a cached answer has one fetched in the background; one
// without a MusicBrainz id is sent for a lookup.
func (e *Engine) similarArtists(ctx context.Context, artists []store.Item) (map[string]int, map[string]string) {
	scores := map[string]int{}
	names := map[string]string{}
	for _, a := range artists {
		mbid, err := e.Store.ArtistMBID(ctx, a)
		if err != nil {
			logger.Warn("artist id lookup failed", "artist", a.Title, "err", err)
			continue
		}
		if mbid == "" {
			if e.LookupArtist != nil && e.LookupArtist(a) {
				logger.Debug("artist sent for lookup", "artist", a.Title)
			}
			continue
		}
		list, fetched, err := e.Store.SimilarArtists(ctx, mbid)
		if err != nil {
			logger.Warn("reading similar artists failed", "artist", a.Title, "err", err)
			continue
		}
		if fetched == 0 || e.Now().Sub(time.Unix(fetched, 0)) > cacheFor {
			e.fetchInBackground(mbid, a.Title)
		}
		for _, s := range list {
			if s.MBID == mbid {
				continue
			}
			if s.Score > scores[s.MBID] {
				scores[s.MBID] = s.Score
			}
			names[s.MBID] = s.Name
		}
	}
	return scores, names
}

// topRanks is each artist's best-known recordings from the cache, by
// recording id and by folded title, rank from 1. Artists without a cached
// answer are fetched in the background, the first topFetch of them.
func (e *Engine) topRanks(ctx context.Context, artists []store.Item) map[string]map[string]int {
	out := map[string]map[string]int{}
	fetches := 0
	for _, a := range artists {
		mbid, err := e.Store.ArtistMBID(ctx, a)
		if err != nil || mbid == "" {
			continue
		}
		list, fetched, err := e.Store.TopRecordings(ctx, mbid)
		if err != nil {
			continue
		}
		if fetched == 0 || e.Now().Sub(time.Unix(fetched, 0)) > cacheFor {
			refused := e.tokenRefusedAt.Load()
			if fetches < topFetch && (refused == 0 || e.Now().Sub(time.Unix(refused, 0)) > time.Hour) {
				e.fetchTopInBackground(mbid, a.Title)
				fetches++
			}
		}
		if len(list) == 0 {
			continue
		}
		ranks := map[string]int{}
		for _, r := range list {
			ranks[r.MBID] = r.Rank
			ranks["title:"+strings.ToLower(store.Fold(r.Name))] = r.Rank
		}
		out[a.ID] = ranks
	}
	return out
}

// albumArtists maps each pool track's album to its artist's id.
func (e *Engine) albumArtists(ctx context.Context, pool []store.Item) (map[string]string, error) {
	out := map[string]string{}
	for _, t := range pool {
		if _, ok := out[t.ParentID]; ok || t.ParentID == "" {
			continue
		}
		al, err := e.Store.ItemOrNone(ctx, t.ParentID)
		if err != nil {
			return nil, err
		}
		out[t.ParentID] = al.ParentID
	}
	return out, nil
}

// fetchInBackground fetches and caches an artist's similar artists once;
// a second ask while it runs does nothing. The next mix sees the answer.
func (e *Engine) fetchInBackground(mbid, name string) {
	if e.Source == nil {
		return
	}
	if _, busy := e.fetching.LoadOrStore("similar:"+mbid, true); busy {
		return
	}
	go func() {
		defer e.fetching.Delete("similar:" + mbid)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		list, err := e.Source.SimilarArtists(ctx, mbid)
		if err != nil && !errors.Is(err, ErrNotFound) {
			logger.Warn("similar artists lookup failed", "artist", name, "err", err)
			return
		}
		if err := e.Store.SetSimilarArtists(ctx, mbid, list, e.Now().Unix()); err != nil {
			logger.Warn("caching similar artists failed", "artist", name, "err", err)
			return
		}
		logger.Debug("similar artists cached", "artist", name, "found", len(list))
	}()
}

func (e *Engine) fetchTopInBackground(mbid, name string) {
	if e.Source == nil {
		return
	}
	if _, busy := e.fetching.LoadOrStore("top:"+mbid, true); busy {
		return
	}
	go func() {
		defer e.fetching.Delete("top:" + mbid)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		list, err := e.Source.TopRecordings(ctx, mbid)
		if errors.Is(err, ErrToken) {
			if e.tokenRefusedAt.Swap(e.Now().Unix()) == 0 {
				logger.Warn("top songs unavailable: ListenBrainz wants a user token", "artist", name,
					"fix", "set metadata.listenbrainz.token (CINEVA_METADATA_LISTENBRAINZ_TOKEN) to a ListenBrainz account's token")
			}
			return
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			logger.Warn("top recordings lookup failed", "artist", name, "err", err)
			return
		}
		if err := e.Store.SetTopRecordings(ctx, mbid, list, e.Now().Unix()); err != nil {
			logger.Warn("caching top recordings failed", "artist", name, "err", err)
			return
		}
		logger.Debug("top recordings cached", "artist", name, "found", len(list))
	}()
}

// Wait blocks until background fetches have finished: for tests and for
// the end of a request that wants the next mix to be the better one.
func (e *Engine) Wait() {
	for {
		busy := false
		e.fetching.Range(func(_, _ any) bool { busy = true; return false })
		if !busy {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// arrange turns the scored candidates into a batch: the seed's best first
// when the mix is started on purpose, then the rest diversified.
func (e *Engine) arrange(sd seed, seedArtists []store.Item, cands []candidate, o Options, topRank map[string]map[string]int) []store.Item {
	size := o.Size
	var lead []candidate // what the batch opens with, in order
	rest := cands
	seedArtistCap := (size + 3) / 4
	albumCap := seedAlbumMax
	if o.Start {
		switch sd.item.Kind {
		case store.KindTrack:
			// The track itself first.
			for i, c := range rest {
				if c.track.ID == sd.item.ID {
					lead = append(lead, c)
					rest = append(rest[:i:i], rest[i+1:]...)
					break
				}
			}
		case store.KindAlbum:
			// Three or four of the album's best, spread through the batch
			// rather than in a block, and nothing else from the album.
			var own []candidate
			rest = slices.DeleteFunc(rest, func(c candidate) bool {
				if c.track.ParentID == sd.album.ID {
					own = append(own, c)
					return true
				}
				return false
			})
			sort.SliceStable(own, func(i, j int) bool { return leadBefore(own[i], own[j]) })
			n := 4
			if size < 16 {
				n = 3
			}
			lead = own[:min(n, len(own))]
			albumCap = 0
		case store.KindArtist:
			// The artist's best-known songs leading.
			var own []candidate
			rest = slices.DeleteFunc(rest, func(c candidate) bool {
				if c.artistID == sd.artist.ID {
					own = append(own, c)
					return true
				}
				return false
			})
			sort.SliceStable(own, func(i, j int) bool { return leadBefore(own[i], own[j]) })
			n := min(3, len(own))
			lead = own[:n]
			rest = append(rest, own[n:]...)
			sort.SliceStable(rest, func(i, j int) bool { return rest[i].score > rest[j].score })
		}
	}
	picked := e.diversify(rest, size-len(lead), sd, seedArtists, seedArtistCap-countSeedArtist(lead, seedArtists), albumCap, lead)
	out := make([]store.Item, 0, size)
	if o.Start && sd.item.Kind == store.KindAlbum && len(lead) > 0 {
		// Spread the album's tracks: the first opens the batch, the rest
		// sit at even intervals through it.
		total := len(lead) + len(picked)
		spacing := max(total/len(lead), 1)
		merged := make([]store.Item, 0, total)
		li, pi := 0, 0
		for i := 0; i < total; i++ {
			if li < len(lead) && (i >= li*spacing || pi >= len(picked)) {
				merged = append(merged, lead[li].track)
				li++
				continue
			}
			merged = append(merged, picked[pi].track)
			pi++
		}
		return merged
	}
	for _, c := range lead {
		out = append(out, c.track)
	}
	for _, c := range picked {
		out = append(out, c.track)
	}
	return out
}

// albumBest ranks an album's or artist's own tracks for a lead: the
// household's plays and ratings, then the world's rank. Chance is left
// out, so with nothing known the order falls to the album's own.
func albumBest(c candidate) float64 {
	return 2*c.own + c.house + c.rating + 2*c.top
}

// leadBefore orders lead candidates: the best known first, and with
// nothing known about either, disc and track order.
func leadBefore(a, b candidate) bool {
	if x, y := albumBest(a), albumBest(b); x != y {
		return x > y
	}
	if a.track.DiscNum != b.track.DiscNum {
		return a.track.DiscNum < b.track.DiscNum
	}
	return a.track.TrackNum < b.track.TrackNum
}

func countSeedArtist(cs []candidate, seeds []store.Item) int {
	n := 0
	for _, c := range cs {
		if isSeedArtist(seeds, c.artistID) {
			n++
		}
	}
	return n
}

// diversify picks n candidates in score order under the rules: no two in
// a row by one artist, at most perArtistMax by one artist in any
// perArtistWindow picks, the seed artists capped at seedCap over the
// batch, the seed's album at most albumCap in the first window. When no
// candidate satisfies them all, the rules relax one at a time, the
// per-artist window first and the seed album's cap last, so a small
// library still fills the batch with the least damage.
func (e *Engine) diversify(cands []candidate, n int, sd seed, seeds []store.Item, seedCap, albumCap int, lead []candidate) []candidate {
	var out []candidate
	used := map[string]bool{}
	lastArtist := ""
	if len(lead) > 0 {
		lastArtist = lead[len(lead)-1].artistID
	}
	seedCount := 0
	albumCount := 0
	for _, c := range lead {
		if c.track.ParentID == sd.album.ID {
			albumCount++
		}
	}
	window := func(artistID string) int {
		cnt := 0
		start := max(0, len(out)-perArtistWindow+1)
		for _, c := range out[start:] {
			if c.artistID == artistID {
				cnt++
			}
		}
		return cnt
	}
	for len(out) < n {
		pick := -1
		for relax := 0; relax < 5 && pick < 0; relax++ {
			for i, c := range cands {
				if used[c.track.ID] {
					continue
				}
				if relax < 1 && window(c.artistID) >= perArtistMax {
					continue
				}
				if relax < 2 && isSeedArtist(seeds, c.artistID) && seedCount >= seedCap {
					continue
				}
				if relax < 3 && c.artistID == lastArtist && c.artistID != "" {
					continue
				}
				if relax < 4 && len(out)+len(lead) < perArtistWindow && c.track.ParentID == sd.album.ID && sd.album.ID != "" && albumCount >= albumCap {
					continue
				}
				pick = i
				break
			}
		}
		if pick < 0 {
			break
		}
		c := cands[pick]
		used[c.track.ID] = true
		out = append(out, c)
		lastArtist = c.artistID
		if isSeedArtist(seeds, c.artistID) {
			seedCount++
		}
		if c.track.ParentID == sd.album.ID {
			albumCount++
		}
	}
	return out
}
