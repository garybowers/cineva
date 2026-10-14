package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// NewEpisodeDays is how far back "new" reaches for episodes: the New
// episodes row takes those that aired within it, and a series in Recently
// added is labelled with its newest episode when that arrived within it.
//
// Six weeks, because the common case is a weekly series: a six-part British
// run (Slow Horses, say) goes from premiere to finale in five weeks, so the
// whole of a running season stays on the row for someone who looks in every
// few weeks, while a season that finished two months ago has dropped off.
// Much shorter and a fortnight away loses episodes nobody has seen yet; much
// longer and the row fills with last season's news.
const NewEpisodeDays = 42

// newSince is the start of the NewEpisodeDays window ending at now.
func newSince(now time.Time) time.Time {
	return now.UTC().AddDate(0, 0, -NewEpisodeDays)
}

// RecentSeries returns the series a viewer has most recently played an
// episode of, newest first — the candidates for the home screen's Next Up.
func (s *Store) RecentSeries(ctx context.Context, userID string, limit int) ([]Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemColumns("sr")+` FROM items sr
		 JOIN (SELECT se.parent_id AS series_id, MAX(p.updated_at) AS last
		       FROM play_state p
		       JOIN items e  ON e.id = p.item_id AND e.kind = 'episode'
		       JOIN items se ON se.id = e.parent_id
		       WHERE p.user_id = ?
		       GROUP BY se.parent_id) r ON r.series_id = sr.id
		 ORDER BY r.last DESC LIMIT ?`, userID, limit)
}

// RecentlyAdded returns a library's newest top-level items: films, series,
// or albums by media type.
//
// A series is placed by its newest episode, not by when the series itself
// first appeared: a new season of a long-running show is news, and listing
// episodes instead would fill the row with one show. Series whose newest
// episodes arrived together fall back to which aired most recently.
func (s *Store) RecentlyAdded(ctx context.Context, lib Library, limit int) ([]Item, error) {
	return s.recentlyAdded(ctx, lib, limit, time.Now())
}

// recentlyAdded is RecentlyAdded as of now, which tests fix.
func (s *Store) recentlyAdded(ctx context.Context, lib Library, limit int, now time.Time) ([]Item, error) {
	switch lib.MediaType {
	case MediaTV:
		series, err := s.queryItems(ctx,
			`SELECT `+itemColumns("sr")+` FROM items sr
			 JOIN (SELECT se.parent_id AS series_id, MAX(e.added_at) AS newest,
			              MAX(e.air_date) AS aired
			       FROM items e JOIN items se ON se.id = e.parent_id
			       WHERE e.library_id = ? AND e.kind = 'episode'
			       GROUP BY se.parent_id) n ON n.series_id = sr.id
			 ORDER BY n.newest DESC, n.aired DESC LIMIT ?`, lib.ID, limit)
		if err != nil {
			return nil, err
		}
		if err := s.attachLatestEpisodes(ctx, lib.ID, series, newSince(now)); err != nil {
			return nil, err
		}
		return series, nil
	case MediaMovies:
		return s.queryItems(ctx,
			`SELECT `+itemCols+` FROM items
			 WHERE library_id = ? AND kind = 'movie' AND version_of = ''
			 ORDER BY added_at DESC LIMIT ?`, lib.ID, limit)
	case MediaMusic:
		return s.queryItems(ctx,
			`SELECT `+itemCols+` FROM items
			 WHERE library_id = ? AND kind = 'album'
			 ORDER BY added_at DESC LIMIT ?`, lib.ID, limit)
	}
	return nil, fmt.Errorf("recently added: no listing for %s libraries", lib.MediaType)
}

// EpisodeBrief is enough of an episode to label something else with it
// (EpisodeRef, in requests, is an episode asked for by number).
type EpisodeBrief struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	SeasonNum  int    `json:"seasonNum"`
	EpisodeNum int    `json:"episodeNum"`
	AddedAt    string `json:"addedAt"`
	AirDate    string `json:"airDate,omitempty"`
}

// attachLatestEpisodes sets LatestEpisode on each series that gained an
// episode since the given time: the newest to arrive, and of several that
// arrived together the latest in the running order. A returning show then
// reads as "S6 · E5" — what is new — rather than the year it began.
func (s *Store) attachLatestEpisodes(ctx context.Context, libraryID string, series []Item, since time.Time) error {
	if len(series) == 0 {
		return nil
	}
	byID := make(map[string]*Item, len(series))
	args := []any{libraryID, since.UTC().Format(time.DateTime)}
	for i := range series {
		byID[series[i].ID] = &series[i]
		args = append(args, series[i].ID)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT series_id, id, title, season_num, episode_num, added_at, air_date FROM (
		   SELECT se.parent_id AS series_id, e.id, e.title, e.season_num, e.episode_num,
		          e.added_at, e.air_date,
		          ROW_NUMBER() OVER (PARTITION BY se.parent_id
		                             ORDER BY e.added_at DESC, e.season_num DESC, e.episode_num DESC) AS rn
		   FROM items e JOIN items se ON se.id = e.parent_id
		   WHERE e.library_id = ? AND e.kind = 'episode' AND e.added_at >= ?
		     AND se.parent_id IN (?`+strings.Repeat(", ?", len(series)-1)+`)
		 ) WHERE rn = 1`, args...)
	if err != nil {
		return fmt.Errorf("latest episodes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var seriesID string
		var ep EpisodeBrief
		if err := rows.Scan(&seriesID, &ep.ID, &ep.Title, &ep.SeasonNum, &ep.EpisodeNum, &ep.AddedAt, &ep.AirDate); err != nil {
			return fmt.Errorf("scan latest episode: %w", err)
		}
		if sr, ok := byID[seriesID]; ok {
			sr.LatestEpisode = &ep
		}
	}
	return rows.Err()
}

// NewEpisodes is the New episodes row: episodes in the library that aired
// within NewEpisodeDays of now, newest broadcast first, and of those that
// aired the same day the latest to arrive. Each carries its series, since an
// episode title alone does not say which show it is, and the viewer's own
// watch state. libraryIDs scopes it as for ContinueWatching: nil means every
// library (admins), empty means none.
//
// Air date rather than arrival, because that is what "new" means to a
// viewer: a back catalogue fetched today is not new episodes. Episodes with
// no air date are left out, since nothing says when they were broadcast,
// and so is a file that was here more than a week before its air date: that
// is a mismatched episode (the wrong season, the wrong edition of a format
// show), not a new one. The week allows for streaming releases that come out
// ahead of the broadcast and for time zones.
func (s *Store) NewEpisodes(ctx context.Context, userID string, libraryIDs []string, now time.Time, limit int) ([]ResumeItem, error) {
	scope, args := "", []any{userID, newSince(now).Format(time.DateOnly)}
	if libraryIDs != nil {
		if len(libraryIDs) == 0 {
			return []ResumeItem{}, nil
		}
		scope = ` AND e.library_id IN (?` + strings.Repeat(", ?", len(libraryIDs)-1) + `)`
		for _, id := range libraryIDs {
			args = append(args, id)
		}
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+itemColumns("e")+`, COALESCE(p.position_secs, 0), COALESCE(p.watched, 0),
		        sr.id, sr.title, sr.poster, sr.backdrop
		 FROM items e
		 JOIN items se ON se.id = e.parent_id
		 JOIN items sr ON sr.id = se.parent_id
		 LEFT JOIN play_state p ON p.item_id = e.id AND p.user_id = ?
		 WHERE e.kind = 'episode' AND e.air_date != '' AND e.air_date >= ?
		   AND e.added_at >= date(e.air_date, '-7 days')`+scope+`
		 ORDER BY e.air_date DESC, e.added_at DESC, e.season_num DESC, e.episode_num DESC
		 LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("new episodes: %w", err)
	}
	defer rows.Close()
	out := []ResumeItem{}
	for rows.Next() {
		var r ResumeItem
		var watched int
		targets := append(itemScanTargets(&r.Item), &r.PositionSecs, &watched,
			&r.SeriesID, &r.SeriesTitle, &r.SeriesPoster, &r.SeriesBackdrop)
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("scan new episode: %w", err)
		}
		r.Watched = watched == 1
		r.Item.PositionSecs = r.PositionSecs
		out = append(out, r)
	}
	return out, rows.Err()
}
