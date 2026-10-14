package store

import (
	"context"
	"fmt"
)

// IndexItem is a title as the importer matches against it (see
// docs/import.md): ids, numbering and path, and for a track its album's
// title.
type IndexItem struct {
	ID         string
	Kind       ItemKind
	ParentID   string
	LibraryID  string
	TmdbID     int
	SeasonNum  int
	EpisodeNum int
	Title      string
	Artist     string
	Album      string
	Path       string
}

// ImportIndex lists every film, series, season, episode and track, with a
// track's album title, for matching another server's titles to Cineva's.
// Alternative versions are left out: watch state belongs to the primary.
func (s *Store) ImportIndex(ctx context.Context) ([]IndexItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id, i.kind, COALESCE(i.parent_id, ''), i.library_id,
			COALESCE(i.tmdb_id, 0), i.season_num, i.episode_num, i.title, COALESCE(i.artist, ''),
			COALESCE(p.title, ''), COALESCE(i.path, '')
		FROM items i LEFT JOIN items p ON p.id = i.parent_id
		WHERE i.kind IN ('movie', 'series', 'season', 'episode', 'track') AND COALESCE(i.version_of, '') = ''`)
	if err != nil {
		return nil, fmt.Errorf("import index: %w", err)
	}
	defer rows.Close()
	out := []IndexItem{}
	for rows.Next() {
		var it IndexItem
		if err := rows.Scan(&it.ID, &it.Kind, &it.ParentID, &it.LibraryID, &it.TmdbID,
			&it.SeasonNum, &it.EpisodeNum, &it.Title, &it.Artist, &it.Album, &it.Path); err != nil {
			return nil, fmt.Errorf("import index: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ImportPlayState writes a watch state brought from another server, unless
// Cineva already has a state for that user and title at least as recent:
// an import never overwrites what was watched here since. at is when it
// was last played there ("2006-01-02 15:04:05", UTC). Reports whether it
// wrote.
func (s *Store) ImportPlayState(ctx context.Context, userID, itemID string, watched bool, positionSecs int, at string) (bool, error) {
	w := 0
	if watched {
		w, positionSecs = 1, 0
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO play_state (user_id, item_id, position_secs, watched, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id, item_id) DO UPDATE SET
			position_secs = excluded.position_secs, watched = excluded.watched, updated_at = excluded.updated_at
		WHERE excluded.updated_at > play_state.updated_at`, userID, itemID, positionSecs, w, at)
	if err != nil {
		return false, fmt.Errorf("import play state: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
