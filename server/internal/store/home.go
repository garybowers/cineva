package store

import (
	"context"
	"fmt"
)

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
	switch lib.MediaType {
	case MediaTV:
		return s.queryItems(ctx,
			`SELECT `+itemColumns("sr")+` FROM items sr
			 JOIN (SELECT se.parent_id AS series_id, MAX(e.added_at) AS newest,
			              MAX(e.air_date) AS aired
			       FROM items e JOIN items se ON se.id = e.parent_id
			       WHERE e.library_id = ? AND e.kind = 'episode'
			       GROUP BY se.parent_id) n ON n.series_id = sr.id
			 ORDER BY n.newest DESC, n.aired DESC LIMIT ?`, lib.ID, limit)
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
