package library

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// artistRetry is how long an artist whose picture or biography was not
// found waits before being looked up again: long, since what is missing
// today is usually missing next week too, but not for ever — Wikidata
// gains pictures.
const artistRetry = 30 * 24 * time.Hour

// artistLookupTimeout bounds one lookup: two MusicBrainz requests under
// its rate limit, three to Wikimedia.
const artistLookupTimeout = time.Minute

// ArtistLookupDue reports whether an artist's picture and biography
// should be looked up: never looked up, or missing one of them since a
// lookup long enough ago. Never for the placeholder artists, nor when no
// provider can look artists up.
func (m *Manager) ArtistLookupDue(ctx context.Context, artist store.Item) (bool, error) {
	if _, ok := m.Music.(metadata.ArtistProvider); !ok || artist.Kind != store.KindArtist {
		return false, nil
	}
	switch artist.Title {
	case "", "Unknown Artist", "Various Artists":
		return false, nil
	}
	info, err := m.Store.ArtistInfoFor(ctx, artist.ID)
	if err != nil {
		return false, err
	}
	if info.LookedUpAt == 0 {
		return true, nil
	}
	if artist.Poster != "" && artist.Overview != "" {
		return false, nil
	}
	return time.Since(time.Unix(info.LookedUpAt, 0)) > artistRetry, nil
}

// LookUpArtistInBackground starts a lookup of an artist's picture and
// biography unless one is already running for them, and reports whether
// it started one. Started from an artist page being opened: looking up
// every artist during a scan would cost a large library hours at
// MusicBrainz's one request a second, for pages most will never open.
func (m *Manager) LookUpArtistInBackground(artist store.Item) bool {
	if _, busy := m.artists.LoadOrStore(artist.ID, true); busy {
		return false
	}
	go func() {
		defer m.artists.Delete(artist.ID)
		ctx, cancel := context.WithTimeout(context.Background(), artistLookupTimeout)
		defer cancel()
		if err := m.LookUpArtist(ctx, artist); err != nil {
			musicLog.Warn("artist lookup failed", "artist", artist.Title, "err", err)
		}
	}()
	return true
}

// LookUpArtist fetches an artist's picture and biography and stores them.
// A lookup that finds nothing is recorded, so it is not repeated until
// artistRetry has passed; one that fails (the network, a provider error)
// is not, so the next visit tries again.
func (m *Manager) LookUpArtist(ctx context.Context, artist store.Item) error {
	ap, ok := m.Music.(metadata.ArtistProvider)
	if !ok {
		return nil
	}
	info, err := ap.ArtistInfo(ctx, artist.Title)
	found := store.ArtistInfo{LookedUpAt: time.Now().Unix()}
	if errors.Is(err, metadata.ErrNotFound) {
		return m.Store.SetArtistInfo(ctx, artist.ID, "", "", found)
	}
	if err != nil {
		return fmt.Errorf("look up %s: %w", artist.Title, err)
	}
	found.MBID, found.BioURL = info.MBID, info.BioURL
	var poster string
	if len(info.Image) > 0 && m.ArtworkDir != "" {
		poster = m.writeArtwork("artist", artist.ID, info.Image)
	}
	if err := m.Store.SetArtistInfo(ctx, artist.ID, poster, info.Bio, found); err != nil {
		return err
	}
	if m.Events != nil {
		m.Events.Publish(events.Event{Type: "items", Data: map[string]string{"libraryId": artist.LibraryID, "itemId": artist.ID}})
	}
	musicLog.Info("artist looked up", "artist", artist.Title, "picture", poster != "", "biography", info.Bio != "")
	return nil
}
