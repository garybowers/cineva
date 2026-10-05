package library

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// ErrNoProvider is returned when a metadata operation is asked for but no
// provider is configured — no TMDB key, so there is nothing to match
// against. Callers turn this into a clear message rather than a silent no-op.
var ErrNoProvider = fmt.Errorf("no metadata provider configured")

// HasVideoProvider reports whether metadata operations are available, so the
// admin UI can explain why the controls are disabled instead of offering
// buttons that cannot work.
func (m *Manager) HasVideoProvider() bool { return m.Video != nil }

// SearchMovies offers the candidates for a title, for an operator correcting
// a wrong match. An empty query falls back to the item's current title.
func (m *Manager) SearchMovies(ctx context.Context, title string, year int) ([]metadata.VideoResult, error) {
	if m.Video == nil {
		return nil, ErrNoProvider
	}
	results, err := m.Video.SearchMovies(ctx, title, year)
	if err != nil {
		return nil, err
	}
	return results, nil
}

// ApplyMatch pins an item to a specific TMDB title and rewrites its metadata
// from it — the "fix match" operation.
//
// The title TMDB holds replaces whatever the filename produced, because the
// operator has just asserted this is the right film; keeping a scene-release
// title after correcting the match would defeat the point.
func (m *Manager) ApplyMatch(ctx context.Context, itemID string, tmdbID int) (store.Item, error) {
	if m.Video == nil {
		return store.Item{}, ErrNoProvider
	}
	item, err := m.Store.Item(ctx, itemID)
	if err != nil {
		return store.Item{}, fmt.Errorf("apply match: %w", err)
	}
	res, err := m.Video.MovieByID(ctx, tmdbID)
	if err != nil {
		return store.Item{}, fmt.Errorf("apply match: %w", err)
	}
	item.Title = res.Title
	if res.Year > 0 {
		item.Year = res.Year
	}
	// applyVideoResult only fills a blank overview, which is right during a
	// scan but wrong here: a correction must replace what the wrong match
	// left behind.
	item.Overview = ""
	item.Poster = ""
	m.applyVideoResult(ctx, &item, res)
	saved, err := m.Store.UpsertItem(ctx, item)
	if err != nil {
		return store.Item{}, fmt.Errorf("apply match: %w", err)
	}
	// Replace outright: the previous match's genres must not survive a
	// correction, so this clears even when the new match has none.
	if err := m.Store.ReplaceGenres(ctx, saved.ID, res.Genres); err != nil {
		return store.Item{}, fmt.Errorf("apply match genres: %w", err)
	}
	saved.Genres = res.Genres
	m.saveCredits(ctx, saved.ID, res)
	return saved, nil
}

// RefreshItem re-runs matching for one item from its current title and year,
// replacing whatever metadata it holds.
func (m *Manager) RefreshItem(ctx context.Context, itemID string) (store.Item, error) {
	if m.Video == nil {
		return store.Item{}, ErrNoProvider
	}
	item, err := m.Store.Item(ctx, itemID)
	if err != nil {
		return store.Item{}, fmt.Errorf("refresh: %w", err)
	}
	// Re-derive from the path rather than the stored title, so a refresh
	// also recovers from a bad earlier match having overwritten the title.
	info := ParseMovie(item.Path)
	title, year := info.Title, info.Year
	if title == "" {
		title, year = item.Title, item.Year
	}
	res, err := m.Video.SearchMovie(ctx, title, year)
	if err != nil {
		return store.Item{}, fmt.Errorf("refresh %q: %w", title, err)
	}
	item.Title = res.Title
	if res.Year > 0 {
		item.Year = res.Year
	}
	item.Overview = ""
	item.Poster = ""
	m.applyVideoResult(ctx, &item, res)
	saved, err := m.Store.UpsertItem(ctx, item)
	if err != nil {
		return store.Item{}, fmt.Errorf("refresh: %w", err)
	}
	if err := m.Store.ReplaceGenres(ctx, saved.ID, res.Genres); err != nil {
		return store.Item{}, fmt.Errorf("refresh genres: %w", err)
	}
	saved.Genres = res.Genres
	m.saveCredits(ctx, saved.ID, res)
	return saved, nil
}

// RefreshProgress reports how far a library-wide metadata refresh has got.
type RefreshProgress struct {
	LibraryID string `json:"libraryId"`
	Done      int    `json:"done"`
	Total     int    `json:"total"`
	Matched   int    `json:"matched"`
	Failed    int    `json:"failed"`
	Running   bool   `json:"running"`
}

// refreshRate paces requests to the metadata provider. TMDB tolerates far
// more than this, but a library refresh is a background chore: there is no
// reason to spend the whole rate limit on it and leave a scan or a user's
// own lookup queuing behind it.
const refreshRate = 80 * time.Millisecond

// RefreshLibrary re-matches movies in a library, reporting progress as it
// goes. It stops early when ctx is cancelled, which is how the admin's Stop
// button works.
//
// only, when non-empty, limits the run to those item ids — the admin sends
// what its filter is currently showing, so "refresh" means the rows on
// screen rather than silently doing ten times the work. An empty slice
// refreshes every movie in the library.
//
// A title that does not match is left exactly as it was rather than being
// blanked: a failed lookup should never lose the metadata an item already
// had, and "no match" is the normal outcome for a film TMDB does not carry.
func (m *Manager) RefreshLibrary(ctx context.Context, libraryID string, only []string, report func(RefreshProgress)) error {
	if m.Video == nil {
		return ErrNoProvider
	}
	items, err := m.Store.ItemsByLibrary(ctx, libraryID)
	if err != nil {
		return fmt.Errorf("refresh library: %w", err)
	}
	wanted := make(map[string]bool, len(only))
	for _, id := range only {
		wanted[id] = true
	}
	movies := items[:0]
	for _, it := range items {
		if it.Kind != store.KindMovie {
			continue
		}
		if len(wanted) > 0 && !wanted[it.ID] {
			continue
		}
		movies = append(movies, it)
	}

	p := RefreshProgress{LibraryID: libraryID, Total: len(movies), Running: true}
	report(p)
	for _, it := range movies {
		if err := ctx.Err(); err != nil {
			p.Running = false
			report(p)
			return err
		}
		if _, err := m.RefreshItem(ctx, it.ID); err != nil {
			if errors.Is(err, context.Canceled) {
				p.Running = false
				report(p)
				return err
			}
			p.Failed++
		} else {
			p.Matched++
		}
		p.Done++
		report(p)
		select {
		case <-ctx.Done():
		case <-time.After(refreshRate):
		}
	}
	p.Running = false
	report(p)
	return nil
}
