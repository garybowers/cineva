package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Lyrics are a track's words, from its tags or an .lrc file beside it.
type Lyrics struct {
	// Synced means the lines are timed ("[01:23.45] ...").
	Synced bool   `json:"synced"`
	Text   string `json:"text"`
}

// TrackSave is one track to write with its album: the item, its genres
// and its lyrics (nil for none, which clears any stored).
type TrackSave struct {
	Item   Item
	Genres []string
	Lyrics *Lyrics
}

// SaveAlbum writes an album and the tracks of it the scan read, in one
// transaction: the album row, each track with its genres and lyrics, the
// album's cover copied onto its tracks, and the album's genres as the
// union of all its tracks' (migration 0045). One transaction per album
// rather than a handful of autocommit statements per track is most of
// what makes a first scan of a large library quick.
//
// The album's Artist, Album title and Compilation flag are written onto
// every one of its tracks (changed or not) so the denormalised columns
// never disagree with the album row. Returns the saved album and tracks.
func (s *Store) SaveAlbum(ctx context.Context, album Item, tracks []TrackSave) (Item, []Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, nil, fmt.Errorf("save album: %w", err)
	}
	defer tx.Rollback()

	saved, err := s.upsertItem(ctx, tx, album)
	if err != nil {
		return Item{}, nil, err
	}
	out := make([]Item, 0, len(tracks))
	for _, t := range tracks {
		it := t.Item
		it.ParentID = saved.ID
		it.Album, it.AlbumArtist = saved.Title, saved.Artist
		if it.Poster == "" {
			it.Poster = saved.Poster
		}
		st, err := s.upsertItem(ctx, tx, it)
		if err != nil {
			return Item{}, nil, err
		}
		if err := replaceGenres(ctx, tx, st.ID, t.Genres); err != nil {
			return Item{}, nil, err
		}
		if err := setLyrics(ctx, tx, st.ID, t.Lyrics); err != nil {
			return Item{}, nil, err
		}
		out = append(out, st)
	}
	// Unchanged tracks of the album follow the album row too.
	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET album = ?, album_artist = ?, updated_at = datetime('now')
		 WHERE parent_id = ? AND kind = 'track' AND (album <> ? OR album_artist <> ?)`,
		saved.Title, saved.Artist, saved.ID, saved.Title, saved.Artist); err != nil {
		return Item{}, nil, fmt.Errorf("save album tracks: %w", err)
	}
	if saved.Poster != "" && !saved.IsLocked("poster") {
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET poster = ?, updated_at = datetime('now')
			 WHERE parent_id = ? AND kind = 'track' AND poster <> ?`,
			saved.Poster, saved.ID, saved.Poster); err != nil {
			return Item{}, nil, fmt.Errorf("save album cover: %w", err)
		}
	}
	if !saved.IsLocked("genres") {
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_genres WHERE item_id = ?`, saved.ID); err != nil {
			return Item{}, nil, fmt.Errorf("album genres: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO item_genres (item_id, genre)
			 SELECT ?, g.genre FROM item_genres g JOIN items t ON t.id = g.item_id
			 WHERE t.parent_id = ? AND t.kind = 'track'`, saved.ID, saved.ID); err != nil {
			return Item{}, nil, fmt.Errorf("album genres: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Item{}, nil, fmt.Errorf("save album: %w", err)
	}
	return saved, out, nil
}

func setLyrics(ctx context.Context, q dbtx, itemID string, l *Lyrics) error {
	if l == nil || strings.TrimSpace(l.Text) == "" {
		if _, err := q.ExecContext(ctx, `DELETE FROM lyrics WHERE item_id = ?`, itemID); err != nil {
			return fmt.Errorf("clear lyrics: %w", err)
		}
		return nil
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO lyrics (item_id, synced, text) VALUES (?, ?, ?)
		 ON CONFLICT (item_id) DO UPDATE SET synced = excluded.synced, text = excluded.text`,
		itemID, l.Synced, l.Text); err != nil {
		return fmt.Errorf("save lyrics: %w", err)
	}
	return nil
}

// LyricsFor returns a track's lyrics, or nil when it has none.
func (s *Store) LyricsFor(ctx context.Context, itemID string) (*Lyrics, error) {
	var l Lyrics
	err := s.db.QueryRowContext(ctx, `SELECT synced, text FROM lyrics WHERE item_id = ?`, itemID).
		Scan(&l.Synced, &l.Text)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lyrics for %s: %w", itemID, err)
	}
	return &l, nil
}

// AlbumsForLookup is the albums of a library a MusicBrainz lookup could
// improve: no cover or no year yet, never looked up or not since the given
// time, and not by the placeholder artists. An album missing only its
// MusicBrainz id is not worth a request: the id is kept when a lookup runs
// for another reason, or comes from the tags.
func (s *Store) AlbumsForLookup(ctx context.Context, libraryID string, notSince time.Time) ([]Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemColumns("a")+` FROM items a
		 LEFT JOIN album_lookups l ON l.item_id = a.id
		 WHERE a.library_id = ? AND a.kind = 'album'
		   AND (a.poster = '' OR a.year = 0)
		   AND a.artist NOT IN ('', 'Unknown Artist', 'Various Artists')
		   AND COALESCE(l.looked_up_at, 0) < ?
		 ORDER BY a.added_at DESC`, libraryID, notSince.Unix())
}

// SetAlbumLookup records what a MusicBrainz lookup found for an album —
// the release id, a year, a cover (an /artwork/ path) and canonical track
// titles by disc and position, any of which may be empty — and when it
// ran. A locked field is kept; a year or cover the album already has is
// kept too, since the lookup only fills gaps. Track titles are applied
// only to the tracks named, which the scanner limits to untagged ones.
func (s *Store) SetAlbumLookup(ctx context.Context, albumID, mbid string, year int, poster string, titles map[string]string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("album lookup %s: %w", albumID, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET
		   mbid = CASE WHEN ? <> '' THEN ? ELSE mbid END,
		   year = CASE WHEN year = 0 AND ? <> 0 AND instr(',' || locked_fields || ',', ',year,') = 0 THEN ? ELSE year END,
		   poster = CASE WHEN poster = '' AND ? <> '' AND instr(',' || locked_fields || ',', ',poster,') = 0 THEN ? ELSE poster END,
		   updated_at = datetime('now')
		 WHERE id = ? AND kind = 'album'`,
		mbid, mbid, year, year, poster, poster, albumID); err != nil {
		return fmt.Errorf("album lookup %s: %w", albumID, err)
	}
	if poster != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET poster = (SELECT poster FROM items WHERE id = ?), updated_at = datetime('now')
			 WHERE parent_id = ? AND kind = 'track' AND poster = ''`, albumID, albumID); err != nil {
			return fmt.Errorf("album lookup %s: %w", albumID, err)
		}
	}
	for trackID, title := range titles {
		if title == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET title = ?, sort_title = ?, updated_at = datetime('now')
			 WHERE id = ? AND parent_id = ? AND instr(',' || locked_fields || ',', ',title,') = 0`,
			title, SortKey(title), trackID, albumID); err != nil {
			return fmt.Errorf("album lookup %s: %w", albumID, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO album_lookups (item_id, looked_up_at) VALUES (?, ?)
		 ON CONFLICT (item_id) DO UPDATE SET looked_up_at = excluded.looked_up_at`,
		albumID, at.Unix()); err != nil {
		return fmt.Errorf("album lookup %s: %w", albumID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("album lookup %s: %w", albumID, err)
	}
	return nil
}

// TrackArtists is the distinct artists credited on an album's tracks, for
// deciding whether a partly rescanned album is a compilation.
func (s *Store) TrackArtists(ctx context.Context, albumID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT artist FROM items WHERE parent_id = ? AND kind = 'track' AND artist <> ''`, albumID)
	if err != nil {
		return nil, fmt.Errorf("track artists %s: %w", albumID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, fmt.Errorf("track artists %s: %w", albumID, err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
