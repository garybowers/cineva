package store

import (
	"context"
	"fmt"
	"time"
)

// VideoSave is one film, series, season or episode to write with
// everything that hangs off it, in one transaction with the rest of its
// batch (SaveFilms, SaveSeries). A scan prepares a batch first, with
// every lookup, download and probe done, so the transaction holds the
// write lock for the writes alone.
type VideoSave struct {
	Item Item
	// Genres replaces the item's genres when non-empty and the operator
	// has not locked them: a match that produced none leaves what is
	// there, the rule the other metadata fields follow.
	Genres []string
	// Cast and Crew replace the item's credits when either is non-empty.
	Cast, Crew []Person
	// Streams says the file's subtitle and audio tracks (Subtitles,
	// AudioTracks) are recorded, replacing what was there: true for a
	// film or an episode, false for a series or a season, which have no
	// file.
	Streams     bool
	Subtitles   []SubtitleTrack
	AudioTracks []AudioTrack
	// MatchFailedAt, when set and the saved row has no provider id,
	// records a lookup the provider could not answer, which spaces out
	// its retries.
	MatchFailedAt time.Time
	// CreditsAnalysed says the film's end credits were read from its
	// chapters, found (Credits) or not (nil), and is recorded against the
	// file's mtime.
	CreditsAnalysed bool
	Credits         *Segment
}

// SeriesSave is a series with the seasons and episodes of it one batch of
// a scan read. The series row is either written here (Series) or was
// written by an earlier batch (SeriesID); a season likewise.
type SeriesSave struct {
	Series   *VideoSave
	SeriesID string
	Seasons  []SeasonSave
	// Saved is the series row as written, when Series was given.
	Saved Item
}

// SeasonSave is one season of a SeriesSave and the episodes of it the
// batch read.
type SeasonSave struct {
	Season   *Item
	SeasonID string
	Episodes []VideoSave
	// Saved is the season row as written, when Season was given;
	// SavedEpisodes the episodes, in order.
	Saved         Item
	SavedEpisodes []Item
}

// SaveFilms writes a batch of films in one transaction and returns the
// rows as written, in order.
func (s *Store) SaveFilms(ctx context.Context, films []VideoSave) ([]Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("save films: %w", err)
	}
	defer tx.Rollback()
	out := make([]Item, 0, len(films))
	for _, f := range films {
		saved, err := s.saveVideo(ctx, tx, f, "")
		if err != nil {
			return nil, err
		}
		out = append(out, saved)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("save films: %w", err)
	}
	return out, nil
}

// SaveSeries writes a series and the seasons and episodes of it a batch
// read, in one transaction, filling in the Saved fields. The series row,
// when written here, comes first, so its seasons have its id; seasons
// likewise before their episodes.
func (s *Store) SaveSeries(ctx context.Context, sv *SeriesSave) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save series: %w", err)
	}
	defer tx.Rollback()
	seriesID := sv.SeriesID
	if sv.Series != nil {
		saved, err := s.saveVideo(ctx, tx, *sv.Series, "")
		if err != nil {
			return err
		}
		sv.Saved, seriesID = saved, saved.ID
	}
	for i := range sv.Seasons {
		season := &sv.Seasons[i]
		seasonID := season.SeasonID
		if season.Season != nil {
			it := *season.Season
			it.ParentID = seriesID
			saved, err := s.upsertItem(ctx, tx, it)
			if err != nil {
				return err
			}
			season.Saved, seasonID = saved, saved.ID
		}
		season.SavedEpisodes = make([]Item, 0, len(season.Episodes))
		for _, ep := range season.Episodes {
			saved, err := s.saveVideo(ctx, tx, ep, seasonID)
			if err != nil {
				return err
			}
			season.SavedEpisodes = append(season.SavedEpisodes, saved)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save series: %w", err)
	}
	return nil
}

// saveVideo writes one item and what hangs off it inside a transaction.
// parentID, when given, is the item's parent.
func (s *Store) saveVideo(ctx context.Context, tx dbtx, v VideoSave, parentID string) (Item, error) {
	it := v.Item
	if parentID != "" {
		it.ParentID = parentID
	}
	saved, err := s.upsertItem(ctx, tx, it)
	if err != nil {
		return Item{}, err
	}
	if len(v.Genres) > 0 && !saved.IsLocked("genres") {
		if err := replaceGenres(ctx, tx, saved.ID, v.Genres); err != nil {
			return Item{}, err
		}
	}
	if len(v.Cast) > 0 || len(v.Crew) > 0 {
		if err := replaceCredits(ctx, tx, saved.ID, v.Cast, v.Crew); err != nil {
			return Item{}, err
		}
	}
	if v.Streams {
		if err := replaceSubtitles(ctx, tx, saved.ID, v.Subtitles); err != nil {
			return Item{}, err
		}
		if err := replaceAudioTracks(ctx, tx, saved.ID, v.AudioTracks); err != nil {
			return Item{}, err
		}
	}
	if !v.MatchFailedAt.IsZero() && saved.TmdbID == 0 {
		if err := recordMatchFailure(ctx, tx, saved.ID, v.MatchFailedAt); err != nil {
			return Item{}, err
		}
	}
	if v.CreditsAnalysed {
		if err := saveSegmentAnalysis(ctx, tx, saved.ID, SegmentCredits, v.Item.FileMtime, v.Credits); err != nil {
			return Item{}, err
		}
	}
	return saved, nil
}
