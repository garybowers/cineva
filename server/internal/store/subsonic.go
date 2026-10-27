package store

import (
	"context"
	"fmt"
	"strings"
)

// Queries the Subsonic shim needs that nothing else does (see
// server/docs/subsonic.md).

// RatedItems is the items of the given kind a user has rated exactly
// stars, newest rating first, within the libraries given (nil for all):
// what a Subsonic client's "starred" list is made of, star being mapped
// onto five stars.
func (s *Store) RatedItems(ctx context.Context, userID string, stars int, kind ItemKind, libraryIDs []string) ([]Item, error) {
	scope, args := libraryScope("i.library_id", libraryIDs)
	args = append([]any{userID, stars, string(kind)}, args...)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+itemColumns("i")+`, r.stars FROM ratings r JOIN items i ON i.id = r.item_id
		 WHERE r.user_id = ? AND r.stars = ? AND i.kind = ? AND `+scope+`
		 ORDER BY r.rated_at DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("rated items: %w", err)
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(append(itemScanTargets(&it), &it.UserRating)...); err != nil {
			return nil, fmt.Errorf("scan rated item: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// AlbumLengths is the total running time of each album's tracks, by album
// id, for the albums given.
func (s *Store) AlbumLengths(ctx context.Context, albumIDs []string) (map[string]int, error) {
	out := map[string]int{}
	for start := 0; start < len(albumIDs); start += 500 {
		chunk := albumIDs[start:min(start+500, len(albumIDs))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT parent_id, COALESCE(SUM(runtime_secs), 0) FROM items
			 WHERE kind = 'track' AND parent_id IN (?`+strings.Repeat(", ?", len(chunk)-1)+`)
			 GROUP BY parent_id`, args...)
		if err != nil {
			return nil, fmt.Errorf("album lengths: %w", err)
		}
		for rows.Next() {
			var id string
			var secs int
			if err := rows.Scan(&id, &secs); err != nil {
				rows.Close()
				return nil, fmt.Errorf("album lengths: %w", err)
			}
			out[id] = secs
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("album lengths: %w", err)
		}
	}
	return out, nil
}
