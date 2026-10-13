package library

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// Unmatched films and series are looked up again on a slow cadence. An
// incremental scan never revisits them — their files have not changed — so
// without this a title the provider added after the first scan, or one a
// provider outage left unmatched, waited for someone to run a full rescan.
//
// The first retry comes a day after the last failed lookup, and each
// further failure doubles the wait, to a month at most: a title the provider
// will never know (a home video, an obscure short) settles at a lookup a
// month rather than one a day for ever.
const (
	retryBase    = 24 * time.Hour
	retryMaxWait = 32 * 24 * time.Hour
	// retryBatch caps the lookups one scan spends on retries, so a library
	// with thousands unmatched (a first scan while the provider was down)
	// catches up over a few scans rather than holding one open for an hour.
	retryBatch = 50
)

// retryWait is how long after a failed lookup a title is next tried, given
// how many lookups have failed so far.
func retryWait(attempts int) time.Duration {
	if attempts <= 1 {
		return retryBase
	}
	return min(retryBase<<min(attempts-1, 5), retryMaxWait)
}

// retryDue reports whether an unmatched title is due another lookup. One
// never tried — every row from before migration 0033 — is due at once.
func retryDue(it store.UnmatchedItem, now time.Time) bool {
	if it.TriedAt.IsZero() {
		return true
	}
	return !now.Before(it.TriedAt.Add(retryWait(it.Attempts)))
}

func (m *Manager) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// recordMatchFailure notes a lookup that found nothing. A failure to note
// it costs only an early retry, so it is logged rather than returned.
func (m *Manager) recordMatchFailure(ctx context.Context, itemID string) {
	if err := m.Store.RecordMatchFailure(context.WithoutCancel(ctx), itemID, m.now()); err != nil {
		metadataLog.Warn("recording failed match failed", "item", itemID, "err", err)
	}
}

// retryUnmatched looks up the library's unmatched films and series that are
// due another try. Run at the end of a scan, so it shares the scan's lock and
// never overlaps one; it reads nothing from the library's storage — every
// title and path it works from is already in the database — so it needs
// none of the background gate's care for the share. Lookups are spaced by
// the same pace a library refresh uses, leaving the provider's rate limit to
// anyone else asking.
//
// Only a match is worth a line at info level; a pass that matched nothing
// is the normal case for a library whose unmatched titles are home videos.
//
// skip names titles the scan itself has just looked up, which a retry
// would only ask about a second time.
func (m *Manager) retryUnmatched(ctx context.Context, lib store.Library, skip map[string]bool) error {
	if m.Video == nil || (lib.MediaType != store.MediaMovies && lib.MediaType != store.MediaTV) {
		return nil
	}
	items, err := m.Store.UnmatchedItems(ctx, lib.ID)
	if err != nil {
		return fmt.Errorf("retry unmatched: %w", err)
	}
	now := m.now()
	var tried int
	var matched []string
	for _, it := range items {
		if tried >= retryBatch {
			break
		}
		if skip[it.ID] || !retryDue(it, now) {
			continue
		}
		if tried > 0 && m.retryPace > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(m.retryPace):
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		tried++
		ok, err := m.retryOne(ctx, lib, it)
		switch {
		case ok:
			matched = append(matched, it.Title)
		case errors.Is(err, metadata.ErrNotFound):
			// Recorded; tried again after a longer wait.
		case err != nil && ctx.Err() == nil:
			var lookup lookupError
			if errors.As(err, &lookup) {
				// The provider could not be asked, which says nothing
				// about the title: leave the rest due for the next scan.
				metadataLog.Debug("retrying unmatched titles postponed: provider unavailable",
					"library", lib.Name, "err", err)
				return nil
			}
			return fmt.Errorf("retry %q: %w", it.Title, err)
		}
	}
	if len(matched) > 0 {
		shown := matched[:min(len(matched), 10)]
		metadataLog.Info("matched previously unmatched titles", "library", lib.Name,
			"matched", len(matched), "tried", tried, "titles", strings.Join(shown, "; "))
	} else if tried > 0 {
		metadataLog.Debug("retried unmatched titles, none matched", "library", lib.Name, "tried", tried)
	}
	return nil
}

// lookupError marks a failure to reach the metadata provider, as against a
// failure of our own store.
type lookupError struct{ err error }

func (e lookupError) Error() string { return e.err.Error() }
func (e lookupError) Unwrap() error { return e.err }

// retryOne looks one unmatched title up again and, on a match, saves what
// the provider has. It reports metadata.ErrNotFound (recorded against the
// item) when there is still no match.
func (m *Manager) retryOne(ctx context.Context, lib store.Library, it store.UnmatchedItem) (bool, error) {
	if it.Kind == store.KindSeries {
		saved, err := m.saveSeries(ctx, lib, it.Path, it.Title, it.Year)
		if err != nil {
			return false, err
		}
		if saved.TmdbID == 0 {
			if saved.lookupErr == nil || errors.Is(saved.lookupErr, metadata.ErrNotFound) {
				return false, metadata.ErrNotFound
			}
			return false, lookupError{saved.lookupErr}
		}
		if err := m.fillSeriesChildren(ctx, saved); err != nil {
			return true, err
		}
		return true, nil
	}

	item, err := m.Store.Item(ctx, it.ID)
	if err != nil {
		return false, err
	}
	// The title and year are the ones the scan settled on — the filename
	// corrected by tags or an .nfo — so nothing on the share is read again.
	// An id in the filename is still the operator's answer.
	var r metadata.VideoResult
	err = metadata.ErrNotFound
	if hint := ParseMovie(item.Path).TmdbID; hint > 0 {
		r, err = m.Video.MovieByID(ctx, hint)
	}
	if err != nil {
		r, err = m.Video.SearchMovie(ctx, item.Title, item.Year)
	}
	if err == nil && r.TmdbID == 0 {
		err = metadata.ErrNotFound
	}
	if errors.Is(err, metadata.ErrNotFound) {
		m.recordMatchFailure(ctx, item.ID)
		return false, metadata.ErrNotFound
	}
	if err != nil {
		return false, lookupError{err}
	}
	// What the scan took from the library itself outranks the provider, as
	// it does in a scan: an overview from the file's tags or its .nfo, and
	// artwork the library carries (copied in under a "local-" name).
	overview, poster, backdrop := item.Overview, item.Poster, item.Backdrop
	m.applyVideoResult(ctx, &item, r)
	if overview != "" {
		item.Overview = overview
	}
	if strings.HasPrefix(poster, "/artwork/local-") {
		item.Poster = poster
	}
	if strings.HasPrefix(backdrop, "/artwork/local-") {
		item.Backdrop = backdrop
	}
	saved, err := m.Store.UpsertItem(ctx, item)
	if err != nil {
		return false, err
	}
	m.saveGenres(ctx, saved.ID, item.Genres)
	m.saveCredits(ctx, saved.ID, r)
	return true, nil
}

// fillSeriesChildren brings a newly matched series' seasons and episodes up
// to what a scan of a matched series would have given them — season posters
// and overviews, episode titles, stills, air dates and guest stars — from
// the rows already stored, without reading a file.
//
// The exception is a daily show's episodes, numbered by their dates while
// the series was unmatched: those need placing by air date against the
// provider's seasons, which is the scanner's job, so their recorded mtimes
// are forgotten and the next scan rereads them.
func (m *Manager) fillSeriesChildren(ctx context.Context, series savedSeries) error {
	seasons := newSeasonCache(m.Video)
	children, err := m.Store.ItemsByParent(ctx, series.ID)
	if err != nil {
		return fmt.Errorf("fill series %q: %w", series.Title, err)
	}
	var forget []string
	for _, season := range children {
		if season.Kind != store.KindSeason {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		episodes, err := m.Store.ItemsByParent(ctx, season.ID)
		if err != nil {
			return fmt.Errorf("fill series %q: %w", series.Title, err)
		}
		if season.SeasonNum >= 1000 { // a year: see placeDatedEpisode
			for _, ep := range episodes {
				forget = append(forget, ep.ID)
			}
			continue
		}
		sr, ok := seasons.get(ctx, series.TmdbID, season.SeasonNum)
		if !ok {
			continue
		}
		m.applySeasonData(ctx, &season, series.TmdbID, sr)
		if _, err := m.Store.UpsertItem(ctx, season); err != nil {
			return fmt.Errorf("fill series %q: %w", series.Title, err)
		}
		for _, ep := range episodes {
			if ep.Kind != store.KindEpisode {
				continue
			}
			guests := m.applyEpisodeData(ctx, &ep, series.TmdbID, season.SeasonNum, ep.EpisodeNum, sr)
			saved, err := m.Store.UpsertItem(ctx, ep)
			if err != nil {
				return fmt.Errorf("fill series %q: %w", series.Title, err)
			}
			if len(guests) > 0 {
				m.saveCredits(ctx, saved.ID, metadata.VideoResult{Cast: guests})
			}
		}
	}
	if err := m.Store.ForgetFileStamps(ctx, forget); err != nil {
		return fmt.Errorf("fill series %q: %w", series.Title, err)
	}
	return nil
}
