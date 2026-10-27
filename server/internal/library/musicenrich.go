package library

import (
	"context"
	"errors"
	"time"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// discPos is a track's place on a release: disc and position, both from 1.
type discPos struct{ disc, pos int }

// albumRetry is how long an album a lookup found nothing for waits before
// being asked about again (see artistRetry).
const albumRetry = 30 * 24 * time.Hour

// albumLookupTimeout bounds one album's lookup: a search, a release and a
// cover, each under MusicBrainz's rate limit.
const albumLookupTimeout = time.Minute

// enrichMusic runs the MusicBrainz lookups a scan no longer waits for: the
// library's albums missing a cover, a year or a release id are looked up
// one by one at the provider's one request a second, which for a big
// untagged library is hours — in the background on the server, and in
// line for a CLI scan, which has nothing else to do. untitled names the
// tracks the scan found without a title tag, by album, which take the
// release's canonical titles when the album matches; the rest keep what
// the scan derived from their file names.
//
// One pass per library at a time; a scan finishing while one runs leaves
// it to run on, and the next scan starts another.
func (m *Manager) enrichMusic(lib store.Library, untitled map[string]map[string]discPos) {
	if m.Music == nil {
		return
	}
	if _, busy := m.enriching.LoadOrStore(lib.ID, true); busy {
		return
	}
	run := func() {
		defer m.enriching.Delete(lib.ID)
		m.enrichAlbums(context.Background(), lib, untitled)
	}
	if m.Events == nil {
		run()
		return
	}
	go run()
}

func (m *Manager) enrichAlbums(ctx context.Context, lib store.Library, untitled map[string]map[string]discPos) {
	albums, err := m.Store.AlbumsForLookup(ctx, lib.ID, time.Now().Add(-albumRetry))
	if err != nil {
		musicLog.Warn("listing albums for lookup failed", "library", lib.Name, "err", err)
		return
	}
	// Albums with untitled tracks are worth a lookup for the titles alone.
	listed := map[string]bool{}
	for _, a := range albums {
		listed[a.ID] = true
	}
	for id := range untitled {
		if listed[id] {
			continue
		}
		if a, err := m.Store.Item(ctx, id); err == nil && a.Artist != "Various Artists" && a.Artist != "Unknown Artist" {
			albums = append(albums, a)
		}
	}
	if len(albums) == 0 {
		return
	}
	musicLog.Info("looking up albums", "library", lib.Name, "albums", len(albums))
	found, changed := 0, 0
	for i, album := range albums {
		if ctx.Err() != nil {
			return
		}
		ok, err := m.enrichAlbum(ctx, album, untitled[album.ID])
		if err != nil {
			musicLog.Warn("album lookup failed", "album", album.Title, "artist", album.Artist, "err", err)
			continue
		}
		if ok {
			found++
			changed++
		}
		// Browsers refresh as covers arrive, a few albums at a time rather
		// than once per album.
		if m.Events != nil && (changed >= 10 || (i == len(albums)-1 && changed > 0)) {
			changed = 0
			m.Events.Publish(events.Event{Type: "items", Data: itemsEvent{LibraryID: lib.ID}})
		}
	}
	musicLog.Info("albums looked up", "library", lib.Name, "albums", len(albums), "found", found)
}

// enrichAlbum looks one album up and records what it found, or that it
// found nothing. A failed request records nothing, so it is tried again
// next time.
func (m *Manager) enrichAlbum(ctx context.Context, album store.Item, untitled map[string]discPos) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, albumLookupTimeout)
	defer cancel()
	now := time.Now()
	rel, err := m.Music.SearchRelease(ctx, album.Artist, album.Title)
	if errors.Is(err, metadata.ErrNotFound) {
		return false, m.Store.SetAlbumLookup(ctx, album.ID, "", 0, "", nil, now)
	}
	if err != nil {
		return false, err
	}
	var poster string
	if album.Poster == "" {
		if img, err := m.Music.FrontCover(ctx, rel.MBID); err == nil && len(img) > 0 {
			poster = m.cacheArtwork("mb", album.Path, img)
		}
	}
	var titles map[string]string
	if len(untitled) > 0 {
		titles = map[string]string{}
		byPos := map[discPos]string{}
		for _, t := range rel.Tracks {
			byPos[discPos{disc: max(t.Disc, 1), pos: t.Pos}] = t.Title
		}
		for trackID, at := range untitled {
			if title := byPos[at]; title != "" {
				titles[trackID] = title
			}
		}
	}
	if err := m.Store.SetAlbumLookup(ctx, album.ID, rel.MBID, rel.Year, poster, titles, now); err != nil {
		return false, err
	}
	musicLog.Debug("musicbrainz match", "artist", album.Artist, "album", album.Title, "mbid", rel.MBID,
		"cover", poster != "", "titles", len(titles))
	return true, nil
}
