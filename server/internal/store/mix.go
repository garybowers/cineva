package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// What a mix needs from the store (server/docs/mix.md): the ListenBrainz
// caches, the library's artists found by MusicBrainz id or by name, the
// tracks under a set of artists with the viewer's state, the household's
// plays, what was played beside an artist, and what the viewer played
// lately. The engine in internal/mix does the scoring; nothing here ranks.

// SimilarArtist is one of ListenBrainz's similar artists for one of ours.
type SimilarArtist struct {
	MBID  string
	Name  string
	Score int
}

// TopRecording is one of an artist's best-known recordings, by rank from 1.
type TopRecording struct {
	MBID string
	Name string
	Rank int
}

// SimilarArtists returns the cached similar artists for an artist's
// MusicBrainz id and when they were fetched (Unix seconds; 0 when never).
// A cached miss comes back as an empty list with a fetch time.
func (s *Store) SimilarArtists(ctx context.Context, mbid string) ([]SimilarArtist, int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT similar_mbid, name, score, fetched_at FROM artist_similar WHERE artist_mbid = ? ORDER BY score DESC`, mbid)
	if err != nil {
		return nil, 0, fmt.Errorf("similar artists %s: %w", mbid, err)
	}
	defer rows.Close()
	var out []SimilarArtist
	var fetched int64
	for rows.Next() {
		var a SimilarArtist
		if err := rows.Scan(&a.MBID, &a.Name, &a.Score, &fetched); err != nil {
			return nil, 0, fmt.Errorf("similar artists %s: %w", mbid, err)
		}
		if a.MBID != "" { // the miss marker carries no artist
			out = append(out, a)
		}
	}
	return out, fetched, rows.Err()
}

// SetSimilarArtists replaces the cache for an artist. An empty list is a
// miss, kept as one marker row so it is not asked for again.
func (s *Store) SetSimilarArtists(ctx context.Context, mbid string, list []SimilarArtist, at int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("similar artists %s: %w", mbid, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM artist_similar WHERE artist_mbid = ?`, mbid); err != nil {
		return fmt.Errorf("similar artists %s: %w", mbid, err)
	}
	if len(list) == 0 {
		list = []SimilarArtist{{}}
	}
	for _, a := range list {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO artist_similar (artist_mbid, similar_mbid, name, score, fetched_at) VALUES (?, ?, ?, ?, ?)`,
			mbid, a.MBID, a.Name, a.Score, at); err != nil {
			return fmt.Errorf("similar artists %s: %w", mbid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("similar artists %s: %w", mbid, err)
	}
	return nil
}

// TopRecordings returns an artist's cached best-known recordings, as
// SimilarArtists does for similarity.
func (s *Store) TopRecordings(ctx context.Context, mbid string) ([]TopRecording, int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT recording_mbid, name, rank, fetched_at FROM artist_top WHERE artist_mbid = ? ORDER BY rank`, mbid)
	if err != nil {
		return nil, 0, fmt.Errorf("top recordings %s: %w", mbid, err)
	}
	defer rows.Close()
	var out []TopRecording
	var fetched int64
	for rows.Next() {
		var r TopRecording
		if err := rows.Scan(&r.MBID, &r.Name, &r.Rank, &fetched); err != nil {
			return nil, 0, fmt.Errorf("top recordings %s: %w", mbid, err)
		}
		if r.MBID != "" {
			out = append(out, r)
		}
	}
	return out, fetched, rows.Err()
}

// SetTopRecordings replaces the cache for an artist; an empty list is a
// miss, kept as one marker row.
func (s *Store) SetTopRecordings(ctx context.Context, mbid string, list []TopRecording, at int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("top recordings %s: %w", mbid, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM artist_top WHERE artist_mbid = ?`, mbid); err != nil {
		return fmt.Errorf("top recordings %s: %w", mbid, err)
	}
	if len(list) == 0 {
		list = []TopRecording{{}}
	}
	for _, r := range list {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO artist_top (artist_mbid, recording_mbid, name, rank, fetched_at) VALUES (?, ?, ?, ?, ?)`,
			mbid, r.MBID, r.Name, r.Rank, at); err != nil {
			return fmt.Errorf("top recordings %s: %w", mbid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("top recordings %s: %w", mbid, err)
	}
	return nil
}

// ArtistMBID is an artist's MusicBrainz id: the one its files carried,
// else the one the artist lookup found; "" when neither.
func (s *Store) ArtistMBID(ctx context.Context, artist Item) (string, error) {
	if artist.MBID != "" {
		return artist.MBID, nil
	}
	info, err := s.ArtistInfoFor(ctx, artist.ID)
	if err != nil {
		return "", err
	}
	return info.MBID, nil
}

// inList is an IN (...) clause and its arguments for a list of strings.
func inList(values []string) (string, []any) {
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	return `(?` + strings.Repeat(", ?", len(values)-1) + `)`, args
}

// chunked runs f over the list in pieces, keeping every query inside
// SQLite's bound-parameter limit however long the list is.
func chunked(values []string, size int, f func([]string) error) error {
	for len(values) > 0 {
		n := min(size, len(values))
		if err := f(values[:n]); err != nil {
			return err
		}
		values = values[n:]
	}
	return nil
}

// ArtistsByMBIDs is the artists of the given libraries (nil for all) whose
// MusicBrainz id, from their files or from a lookup, is in the list.
func (s *Store) ArtistsByMBIDs(ctx context.Context, libraryIDs, mbids []string) ([]Item, error) {
	if len(mbids) == 0 {
		return []Item{}, nil
	}
	scope, sargs := libraryScope("i.library_id", libraryIDs)
	out := []Item{}
	err := chunked(mbids, 400, func(part []string) error {
		in, args := inList(part)
		items, err := s.queryItems(ctx,
			`SELECT `+itemColumns("i")+` FROM items i
			 LEFT JOIN artist_info ai ON ai.item_id = i.id
			 WHERE i.kind = 'artist' AND `+scope+`
			   AND (i.mbid IN `+in+` OR ai.mbid IN `+in+`)`,
			append(append(sargs, args...), args...)...)
		if err != nil {
			return err
		}
		out = append(out, items...)
		return nil
	})
	return out, err
}

// ArtistsByNames is the artists of the given libraries whose name matches
// one of the list, insensitive to case and accents (see Fold).
func (s *Store) ArtistsByNames(ctx context.Context, libraryIDs, names []string) ([]Item, error) {
	if len(names) == 0 {
		return []Item{}, nil
	}
	folded := make([]string, 0, len(names))
	for _, n := range names {
		if f := asciiLower(Fold(strings.TrimSpace(n))); f != "" {
			folded = append(folded, f)
		}
	}
	scope, sargs := libraryScope("i.library_id", libraryIDs)
	out := []Item{}
	err := chunked(folded, 400, func(part []string) error {
		in, args := inList(part)
		items, err := s.queryItems(ctx,
			`SELECT `+itemColumns("i")+` FROM items i
			 WHERE i.kind = 'artist' AND `+scope+` AND lower(unifold(i.title)) IN `+in,
			append(sargs, args...)...)
		if err != nil {
			return err
		}
		out = append(out, items...)
		return nil
	})
	return out, err
}

// ArtistTracks is every track on the given artists' own albums, with the
// viewer's plays and rating, longest list first by nothing in particular:
// the engine ranks.
func (s *Store) ArtistTracks(ctx context.Context, userID string, artistIDs []string) ([]Item, error) {
	if len(artistIDs) == 0 {
		return []Item{}, nil
	}
	out := []Item{}
	err := chunked(artistIDs, 400, func(part []string) error {
		in, args := inList(part)
		items, err := s.queryTracks(ctx, trackQuery()+` WHERE t.kind = 'track' AND a.parent_id IN `+in,
			append([]any{userID, userID}, args...)...)
		if err != nil {
			return err
		}
		out = append(out, items...)
		return nil
	})
	return out, err
}

// TracksByIDs is the tracks with the given ids, with the viewer's plays
// and rating, in no particular order.
func (s *Store) TracksByIDs(ctx context.Context, userID string, ids []string) ([]Item, error) {
	if len(ids) == 0 {
		return []Item{}, nil
	}
	out := []Item{}
	err := chunked(ids, 400, func(part []string) error {
		in, args := inList(part)
		items, err := s.queryTracks(ctx, trackQuery()+` WHERE t.kind = 'track' AND t.id IN `+in,
			append([]any{userID, userID}, args...)...)
		if err != nil {
			return err
		}
		out = append(out, items...)
		return nil
	})
	return out, err
}

// TracksByGenres is up to limit tracks of the given libraries carrying any
// of the genres, from years within the range when both bounds are set,
// picked at random, with the viewer's plays and rating.
func (s *Store) TracksByGenres(ctx context.Context, userID string, libraryIDs, genres []string, yearFrom, yearTo, limit int) ([]Item, error) {
	if len(genres) == 0 || limit <= 0 {
		return []Item{}, nil
	}
	scope, sargs := libraryScope("t.library_id", libraryIDs)
	in, gargs := inList(genres)
	where := ` WHERE t.kind = 'track' AND ` + scope + ` AND t.id IN (SELECT item_id FROM item_genres WHERE genre IN ` + in + `)`
	args := append([]any{userID, userID}, sargs...)
	args = append(args, gargs...)
	if yearFrom > 0 && yearTo > 0 {
		where += ` AND t.year BETWEEN ? AND ?`
		args = append(args, yearFrom, yearTo)
	}
	args = append(args, limit)
	return s.queryTracks(ctx, trackQuery()+where+` ORDER BY random() LIMIT ?`, args...)
}

// HouseholdPlays is every user's plays of the given tracks summed, by id.
func (s *Store) HouseholdPlays(ctx context.Context, trackIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(trackIDs) == 0 {
		return out, nil
	}
	err := chunked(trackIDs, 400, func(part []string) error {
		in, args := inList(part)
		rows, err := s.db.QueryContext(ctx,
			`SELECT item_id, SUM(plays) FROM play_counts WHERE item_id IN `+in+` GROUP BY item_id`, args...)
		if err != nil {
			return fmt.Errorf("household plays: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				return fmt.Errorf("household plays: %w", err)
			}
			out[id] = n
		}
		return rows.Err()
	})
	return out, err
}

// CoListened is the tracks any user played within an hour of a track by
// the artist, since the given time (SQLite datetime), with how often, the
// most often first. Play history is kept for the retention period only
// (90 days by default), so "the last year" is as far as that reaches.
func (s *Store) CoListened(ctx context.Context, artistID, since string, limit int) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT b.item_id, COUNT(*) AS n
		 FROM play_events a
		 JOIN items t ON t.id = a.item_id AND t.kind = 'track'
		 JOIN items al ON al.id = t.parent_id
		 JOIN play_events b ON b.user_id = a.user_id AND b.kind = 'track' AND b.item_id <> a.item_id
		      AND abs(julianday(b.started_at) - julianday(a.started_at)) <= 1.0 / 24
		 WHERE a.kind = 'track' AND a.started_at >= ? AND al.parent_id = ?
		 GROUP BY b.item_id ORDER BY n DESC LIMIT ?`, since, artistID, limit)
	if err != nil {
		return nil, fmt.Errorf("co-listened %s: %w", artistID, err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("co-listened %s: %w", artistID, err)
		}
		out[id] = n
	}
	return out, rows.Err()
}

// RecentlyPlayedIDs is what the viewer played since the given time.
func (s *Store) RecentlyPlayedIDs(ctx context.Context, userID, since string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT item_id FROM play_events WHERE user_id = ? AND started_at >= ?`, userID, since)
	if err != nil {
		return nil, fmt.Errorf("recently played: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("recently played: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// GenresFor is the genres of each of the given items, by id; items with
// none are absent.
func (s *Store) GenresFor(ctx context.Context, itemIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	err := chunked(itemIDs, 400, func(part []string) error {
		in, args := inList(part)
		rows, err := s.db.QueryContext(ctx,
			`SELECT item_id, genre FROM item_genres WHERE item_id IN `+in+` ORDER BY item_id, genre`, args...)
		if err != nil {
			return fmt.Errorf("genres: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, g string
			if err := rows.Scan(&id, &g); err != nil {
				return fmt.Errorf("genres: %w", err)
			}
			out[id] = append(out[id], g)
		}
		return rows.Err()
	})
	return out, err
}

// ItemOrNone is Item without the not-found error: the zero Item when the
// id names nothing, for walking a track's album and artist.
func (s *Store) ItemOrNone(ctx context.Context, id string) (Item, error) {
	if id == "" {
		return Item{}, nil
	}
	it, err := s.Item(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, nil
	}
	return it, err
}
