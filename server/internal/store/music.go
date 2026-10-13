package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// PlayCount is one user's counted plays of an item (see migration 0035).
type PlayCount struct {
	Plays        int
	LastPlayedAt string
}

// PlayCountsByLibrary returns the viewer's play counts for a library's
// items, by item id. Albums and artists are not stored with counts of
// their own; they are summed from their tracks by the listing queries.
func (s *Store) PlayCountsByLibrary(ctx context.Context, userID, libraryID string) (map[string]PlayCount, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT pc.item_id, pc.plays, pc.last_played_at
		 FROM play_counts pc JOIN items i ON i.id = pc.item_id
		 WHERE pc.user_id = ? AND i.library_id = ?`, userID, libraryID)
	if err != nil {
		return nil, fmt.Errorf("play counts by library: %w", err)
	}
	defer rows.Close()
	out := map[string]PlayCount{}
	for rows.Next() {
		var id string
		var pc PlayCount
		if err := rows.Scan(&id, &pc.Plays, &pc.LastPlayedAt); err != nil {
			return nil, fmt.Errorf("scan play count: %w", err)
		}
		out[id] = pc
	}
	return out, rows.Err()
}

// queryItemsPlayed runs a query selecting itemColumns followed by a play
// count and a last-played time, which land on the items' viewer fields.
func (s *Store) queryItemsPlayed(ctx context.Context, query string, args ...any) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query items: %w", err)
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(append(itemScanTargets(&it), &it.PlayCount, &it.LastPlayedAt)...); err != nil {
			return nil, fmt.Errorf("scan item: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// RecentlyPlayedAlbums is the albums of a music library the viewer has
// played a track from, the most recent first, each with the viewer's
// total plays of its tracks.
func (s *Store) RecentlyPlayedAlbums(ctx context.Context, userID, libraryID string, limit int) ([]Item, error) {
	return s.queryItemsPlayed(ctx,
		`SELECT `+itemColumns("a")+`, r.plays, r.last FROM items a
		 JOIN (SELECT t.parent_id AS album_id, SUM(pc.plays) AS plays, MAX(pc.last_played_at) AS last
		       FROM play_counts pc JOIN items t ON t.id = pc.item_id AND t.kind = 'track'
		       WHERE pc.user_id = ? AND t.library_id = ?
		       GROUP BY t.parent_id) r ON r.album_id = a.id
		 ORDER BY r.last DESC LIMIT ?`, userID, libraryID, limit)
}

// MostPlayedTracks is the viewer's most played tracks in a music library.
func (s *Store) MostPlayedTracks(ctx context.Context, userID, libraryID string, limit int) ([]Item, error) {
	return s.queryItemsPlayed(ctx,
		`SELECT `+itemColumns("t")+`, pc.plays, pc.last_played_at
		 FROM play_counts pc JOIN items t ON t.id = pc.item_id
		 WHERE pc.user_id = ? AND t.library_id = ? AND t.kind = 'track'
		 ORDER BY pc.plays DESC, pc.last_played_at DESC LIMIT ?`, userID, libraryID, limit)
}

// MostPlayedAlbums is the albums of a music library whose tracks the viewer
// has played most, all plays of an album's tracks counting towards it.
func (s *Store) MostPlayedAlbums(ctx context.Context, userID, libraryID string, limit int) ([]Item, error) {
	return s.queryItemsPlayed(ctx,
		`SELECT `+itemColumns("a")+`, r.plays, r.last FROM items a
		 JOIN (SELECT t.parent_id AS album_id, SUM(pc.plays) AS plays, MAX(pc.last_played_at) AS last
		       FROM play_counts pc JOIN items t ON t.id = pc.item_id AND t.kind = 'track'
		       WHERE pc.user_id = ? AND t.library_id = ?
		       GROUP BY t.parent_id) r ON r.album_id = a.id
		 ORDER BY r.plays DESC, r.last DESC LIMIT ?`, userID, libraryID, limit)
}

// artistMatch is the condition for a track (alias t, on album a) being by
// an artist, with its arguments: on one of the artist's own albums, or
// credited to them on someone else's — a compilation, a soundtrack, a duet
// ("Name feat. Other", "Name & Other").
func artistMatch(artist Item) (string, []any) {
	name := strings.ToLower(artist.Title)
	esc := likeEscaper.Replace(name)
	cond := `(a.parent_id = ? OR lower(t.artist) = ?`
	args := []any{artist.ID, name}
	for _, joiner := range []string{" feat", " ft.", " & ", ", "} {
		cond += ` OR lower(t.artist) LIKE ? ESCAPE '\'`
		args = append(args, esc+joiner+"%")
	}
	return cond + `)`, args
}

// ArtistTopTracks is an artist's most played tracks in their library. They
// are ranked by the viewer's own plays, then by the household's, so an
// artist the viewer has never played still shows what others play most;
// tracks nobody has played are left out. Each carries the viewer's own
// count.
func (s *Store) ArtistTopTracks(ctx context.Context, userID string, artist Item, limit int) ([]Item, error) {
	cond, cargs := artistMatch(artist)
	args := append([]any{userID, artist.LibraryID}, cargs...)
	args = append(args, limit)
	return s.queryItemsPlayed(ctx,
		`SELECT `+itemColumns("t")+`, COALESCE(mine.plays, 0), COALESCE(mine.last_played_at, '')
		 FROM items t
		 JOIN items a ON a.id = t.parent_id
		 JOIN (SELECT item_id, SUM(plays) AS plays FROM play_counts GROUP BY item_id) everyone
		      ON everyone.item_id = t.id
		 LEFT JOIN play_counts mine ON mine.item_id = t.id AND mine.user_id = ?
		 WHERE t.kind = 'track' AND t.library_id = ? AND `+cond+`
		 ORDER BY COALESCE(mine.plays, 0) DESC, everyone.plays DESC, t.title
		 LIMIT ?`, args...)
}

// ArtistAppearsOn is the albums under other artists — compilations,
// soundtracks, collaborations — with at least one track credited to this
// artist, oldest first.
func (s *Store) ArtistAppearsOn(ctx context.Context, artist Item) ([]Item, error) {
	cond, cargs := artistMatch(artist)
	args := append([]any{artist.LibraryID, artist.ID}, cargs...)
	return s.queryItems(ctx,
		`SELECT `+itemColumns("x")+` FROM items x
		 WHERE x.kind = 'album' AND x.library_id = ? AND COALESCE(x.parent_id, '') <> ?
		   AND EXISTS (SELECT 1 FROM items t JOIN items a ON a.id = t.parent_id
		               WHERE t.parent_id = x.id AND t.kind = 'track' AND `+cond+`)
		 ORDER BY x.year, x.title`, args...)
}

// ArtistStats is how much of an artist the library holds and how often the
// viewer has played them.
type ArtistStats struct {
	Tracks int `json:"tracks"`
	Secs   int `json:"secs"`
	Plays  int `json:"plays"`
}

// ArtistStats counts an artist's own tracks (those on their albums) and the
// viewer's plays of them.
func (s *Store) ArtistStats(ctx context.Context, userID, artistID string) (ArtistStats, error) {
	var st ArtistStats
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(t.runtime_secs), 0), COALESCE(SUM(pc.plays), 0)
		 FROM items t JOIN items a ON a.id = t.parent_id
		 LEFT JOIN play_counts pc ON pc.item_id = t.id AND pc.user_id = ?
		 WHERE t.kind = 'track' AND a.parent_id = ?`, userID, artistID).Scan(&st.Tracks, &st.Secs, &st.Plays)
	if err != nil {
		return ArtistStats{}, fmt.Errorf("artist stats %s: %w", artistID, err)
	}
	return st, nil
}

// ArtistInfo is what a lookup found about an artist beyond the files.
// The picture and biography themselves are the artist's poster and
// overview.
type ArtistInfo struct {
	MBID string `json:"mbid,omitempty"`
	// BioURL is the page the biography was taken from, for its credit.
	BioURL     string `json:"bioUrl,omitempty"`
	LookedUpAt int64  `json:"-"`
}

// SetArtistInfo records a lookup for an artist: the picture (an /artwork/
// path) and biography it found, either possibly empty, and what it was
// found by. A field an admin has locked is kept, and so is anything an
// earlier lookup found that this one did not.
func (s *Store) SetArtistInfo(ctx context.Context, artistID, poster, overview string, info ArtistInfo) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("artist info %s: %w", artistID, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET
		   poster = CASE WHEN ? <> '' AND instr(',' || locked_fields || ',', ',poster,') = 0 THEN ? ELSE poster END,
		   overview = CASE WHEN ? <> '' AND instr(',' || locked_fields || ',', ',overview,') = 0 THEN ? ELSE overview END,
		   updated_at = datetime('now')
		 WHERE id = ? AND kind = 'artist'`,
		poster, poster, overview, overview, artistID); err != nil {
		return fmt.Errorf("artist info %s: %w", artistID, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO artist_info (item_id, mbid, bio_url, looked_up_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (item_id) DO UPDATE SET
		   mbid = CASE WHEN excluded.mbid <> '' THEN excluded.mbid ELSE mbid END,
		   bio_url = CASE WHEN excluded.bio_url <> '' THEN excluded.bio_url ELSE bio_url END,
		   looked_up_at = excluded.looked_up_at`,
		artistID, info.MBID, info.BioURL, info.LookedUpAt); err != nil {
		return fmt.Errorf("artist info %s: %w", artistID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("artist info %s: %w", artistID, err)
	}
	return nil
}

// ArtistInfoFor returns what lookups have found for an artist; the zero
// value (LookedUpAt 0) when none has run.
func (s *Store) ArtistInfoFor(ctx context.Context, artistID string) (ArtistInfo, error) {
	var info ArtistInfo
	err := s.db.QueryRowContext(ctx,
		`SELECT mbid, bio_url, looked_up_at FROM artist_info WHERE item_id = ?`, artistID).
		Scan(&info.MBID, &info.BioURL, &info.LookedUpAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtistInfo{}, nil
	}
	if err != nil {
		return ArtistInfo{}, fmt.Errorf("artist info %s: %w", artistID, err)
	}
	return info, nil
}

// SetAlbumArtwork replaces an album's cover and the copy its tracks carry,
// and records the cover files' fingerprint, without touching anything else
// about them — what a changed cover.jpg needs, rather than rereading every
// track. A poster an admin has locked is kept, as on any rescan.
func (s *Store) SetAlbumArtwork(ctx context.Context, albumID, poster string, stamp int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("album artwork %s: %w", albumID, err)
	}
	defer tx.Rollback()
	var locked string
	if err := tx.QueryRowContext(ctx, `SELECT locked_fields FROM items WHERE id = ?`, albumID).Scan(&locked); err != nil {
		return fmt.Errorf("album artwork %s: %w", albumID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE items SET sidecar_stamp = ? WHERE id = ?`, stamp, albumID); err != nil {
		return fmt.Errorf("album artwork %s: %w", albumID, err)
	}
	if poster != "" && !slices.Contains(splitLockedFields(locked), "poster") {
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET poster = ?, updated_at = datetime('now')
			 WHERE id = ? OR (parent_id = ? AND kind = 'track')`, poster, albumID, albumID); err != nil {
			return fmt.Errorf("album artwork %s: %w", albumID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("album artwork %s: %w", albumID, err)
	}
	return nil
}
