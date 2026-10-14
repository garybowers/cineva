package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidRating is returned for a rating outside 0 to 5 stars.
var ErrInvalidRating = errors.New("a rating is 0 to 5 stars")

// SetRating records a user's star rating of an item, 1 to 5; 0 clears it.
func (s *Store) SetRating(ctx context.Context, userID, itemID string, stars int) error {
	if stars < 0 || stars > 5 {
		return ErrInvalidRating
	}
	if stars == 0 {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM ratings WHERE user_id = ? AND item_id = ?`, userID, itemID); err != nil {
			return fmt.Errorf("clear rating: %w", err)
		}
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ratings (user_id, item_id, stars) VALUES (?, ?, ?)
		 ON CONFLICT (user_id, item_id) DO UPDATE SET stars = excluded.stars, rated_at = datetime('now')`,
		userID, itemID, stars); err != nil {
		return fmt.Errorf("set rating: %w", err)
	}
	return nil
}

// RatingsByLibrary is a user's ratings of a library's items, by item id.
func (s *Store) RatingsByLibrary(ctx context.Context, userID, libraryID string) (map[string]int, error) {
	return s.ratings(ctx,
		`SELECT r.item_id, r.stars FROM ratings r JOIN items i ON i.id = r.item_id
		 WHERE r.user_id = ? AND i.library_id = ?`, userID, libraryID)
}

// RatingsFor is a user's ratings of the given items, by item id; items
// they have not rated are absent.
func (s *Store) RatingsFor(ctx context.Context, userID string, itemIDs []string) (map[string]int, error) {
	out := map[string]int{}
	// SQLite's default limit on bound parameters is well above this; the
	// chunks keep a long queue or playlist inside it.
	for start := 0; start < len(itemIDs); start += 500 {
		chunk := itemIDs[start:min(start+500, len(itemIDs))]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, userID)
		for _, id := range chunk {
			args = append(args, id)
		}
		got, err := s.ratings(ctx,
			`SELECT item_id, stars FROM ratings WHERE user_id = ? AND item_id IN (?`+
				strings.Repeat(", ?", len(chunk)-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for id, n := range got {
			out[id] = n
		}
	}
	return out, nil
}

func (s *Store) ratings(ctx context.Context, query string, args ...any) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query ratings: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("scan rating: %w", err)
		}
		out[id] = n
	}
	return out, rows.Err()
}
