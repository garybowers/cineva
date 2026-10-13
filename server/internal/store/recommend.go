package store

import "context"

// RecRow is one shelf on the Recommended tab: a heading and the films under
// it. Built server-side, like next-up, so the web client and any future
// native app show the same thing rather than each inventing its own rules.
type RecRow struct {
	// Kind lets a client style or reorder a row without parsing its title.
	Kind  string `json:"kind"` // "recent" | "because" | "toprated" | "genre" | "unwatched"
	Title string `json:"title"`
	// Genre is set on a "genre" row, so tapping the heading can open the
	// full genre listing rather than a dead end.
	Genre string `json:"genre,omitempty"`
	Items []Item `json:"items"`
}

const recRowSize = 12

// Recommendations builds the Recommended shelves for one viewer.
//
// Everything here is drawn from what the library already knows — what they
// have finished, what they have not, genres and ratings. There is no
// behavioural model and no cross-user data: a self-hosted server with one
// household's worth of history has nothing to learn from, and guessing badly
// is worse than an honest "here is what you have not seen".
func (s *Store) Recommendations(ctx context.Context, userID, libraryID string) ([]RecRow, error) {
	rows := []RecRow{}

	// "Because you watched X": the genres of the last film they finished,
	// minus anything they have already seen. The most recently watched film
	// is the best available signal of what they are in the mood for.
	if seed, ok, err := s.lastWatched(ctx, userID, libraryID); err != nil {
		return nil, err
	} else if ok {
		like, err := s.similarTo(ctx, userID, libraryID, seed.ID)
		if err != nil {
			return nil, err
		}
		if len(like) > 0 {
			rows = append(rows, RecRow{
				Kind: "because", Title: "Because you watched " + seed.Title, Items: like,
			})
		}
	}

	// "Highly rated, unseen": the strongest single filter most people apply.
	top, err := s.unwatchedByRating(ctx, userID, libraryID)
	if err != nil {
		return nil, err
	}
	if len(top) > 0 {
		rows = append(rows, RecRow{Kind: "toprated", Title: "Highly rated, unseen", Items: top})
	}

	// A couple of the library's biggest genres, unseen first. Which genres
	// those are depends entirely on the collection, so a library of horror
	// gets horror rows rather than a fixed vocabulary.
	genres, err := s.LibraryGenres(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	for i, g := range genres {
		if i >= 2 {
			break
		}
		items, err := s.unwatchedByGenre(ctx, userID, libraryID, g.Genre)
		if err != nil {
			return nil, err
		}
		if len(items) >= 3 { // a row of one or two is not worth a heading
			rows = append(rows, RecRow{
				Kind: "genre", Title: g.Genre, Genre: g.Genre, Items: items,
			})
		}
	}

	return rows, nil
}

// lastWatched is the film this viewer most recently finished in a library.
func (s *Store) lastWatched(ctx context.Context, userID, libraryID string) (Item, bool, error) {
	it, err := scanItem(s.db.QueryRowContext(ctx,
		`SELECT `+itemColumns("i")+` FROM play_state p
		 JOIN items i ON i.id = p.item_id
		 WHERE p.user_id = ? AND i.library_id = ? AND p.watched = 1 AND i.kind = 'movie'
		 ORDER BY p.updated_at DESC LIMIT 1`, userID, libraryID))
	if err != nil {
		return Item{}, false, nil // nothing watched yet is not an error
	}
	return it, true, nil
}

// similarTo finds unseen films sharing genres with the seed, most shared
// genres first. Crude by design: with one household's history there is
// nothing to train on, and "same genres, you have not seen it" is both
// explainable and usually right.
func (s *Store) similarTo(ctx context.Context, userID, libraryID, seedID string) ([]Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemColumns("i")+`
		 FROM items i
		 JOIN item_genres g ON g.item_id = i.id
		 LEFT JOIN play_state p ON p.item_id = i.id AND p.user_id = ?
		 WHERE i.library_id = ? AND i.version_of = '' AND i.id <> ?
		   AND COALESCE(p.watched, 0) = 0
		   AND g.genre IN (SELECT genre FROM item_genres WHERE item_id = ?)
		 GROUP BY i.id
		 ORDER BY COUNT(DISTINCT g.genre) DESC, i.rating DESC, i.title
		 LIMIT ?`, userID, libraryID, seedID, seedID, recRowSize)
}

// unwatchedByRating is the best-rated films this viewer has not seen.
func (s *Store) unwatchedByRating(ctx context.Context, userID, libraryID string) ([]Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemColumns("i")+` FROM items i
		 LEFT JOIN play_state p ON p.item_id = i.id AND p.user_id = ?
		 WHERE i.library_id = ? AND i.version_of = '' AND i.kind = 'movie'
		   AND COALESCE(p.watched, 0) = 0 AND i.rating >= 7
		 ORDER BY i.rating DESC, i.vote_count DESC, i.title
		 LIMIT ?`, userID, libraryID, recRowSize)
}

// unwatchedByGenre is one genre's unseen films, best first.
func (s *Store) unwatchedByGenre(ctx context.Context, userID, libraryID, genre string) ([]Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemColumns("i")+` FROM items i
		 JOIN item_genres g ON g.item_id = i.id
		 LEFT JOIN play_state p ON p.item_id = i.id AND p.user_id = ?
		 WHERE i.library_id = ? AND i.version_of = '' AND g.genre = ?
		   AND COALESCE(p.watched, 0) = 0
		 ORDER BY i.rating DESC, i.title
		 LIMIT ?`, userID, libraryID, genre, recRowSize)
}
