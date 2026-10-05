package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type ItemKind string

const (
	KindMovie   ItemKind = "movie"
	KindSeries  ItemKind = "series"
	KindSeason  ItemKind = "season"
	KindEpisode ItemKind = "episode"
	KindArtist  ItemKind = "artist"
	KindAlbum   ItemKind = "album"
	KindTrack   ItemKind = "track"
)

type Item struct {
	ID          string   `json:"id"`
	LibraryID   string   `json:"libraryId"`
	Kind        ItemKind `json:"kind"`
	ParentID    string   `json:"parentId,omitempty"`
	Title       string   `json:"title"`
	Year        int      `json:"year,omitempty"`
	SeasonNum   int      `json:"seasonNum,omitempty"`
	EpisodeNum  int      `json:"episodeNum,omitempty"`
	DiscNum     int      `json:"discNum,omitempty"`
	TrackNum    int      `json:"trackNum,omitempty"`
	Artist      string   `json:"artist,omitempty"`
	Path        string   `json:"path"`
	SizeBytes   int64    `json:"sizeBytes,omitempty"`
	RuntimeSecs int      `json:"runtimeSecs,omitempty"`
	Container   string   `json:"container,omitempty"`
	VideoCodec  string   `json:"videoCodec,omitempty"`
	AudioCodec  string   `json:"audioCodec,omitempty"`
	Width       int      `json:"width,omitempty"`
	Height      int      `json:"height,omitempty"`
	TmdbID      int      `json:"tmdbId,omitempty"`
	Overview    string   `json:"overview,omitempty"`
	Poster      string   `json:"poster,omitempty"`
	AddedAt     string   `json:"addedAt"`
	UpdatedAt   string   `json:"updatedAt"`
	// VersionOf is the id of the item this one is an alternative version of
	// — the same film at a different quality. Empty for a primary, which is
	// what the library grid lists.
	VersionOf string `json:"versionOf,omitempty"`

	Rating         float64 `json:"rating,omitempty"` // provider score, 0-10
	VoteCount      int     `json:"voteCount,omitempty"`
	Tagline        string  `json:"tagline,omitempty"`
	Backdrop       string  `json:"backdrop,omitempty"`
	CollectionID   int     `json:"collectionId,omitempty"`
	CollectionName string  `json:"collectionName,omitempty"`
	// Certification is the content rating ("15", "PG-13"), free text because
	// the scale is per-country. Distinct from Rating, which is an audience
	// score. See migration 0015.
	Certification string `json:"certification,omitempty"`
	// LockedFields are fields an operator has edited by hand. Anything
	// automatic — a scan, a metadata refresh — leaves them alone, so a
	// correction is not undone by the provider it was correcting. See
	// migration 0017.
	LockedFields []string `json:"lockedFields,omitempty"`
	// Genres is filled by the handlers that need it, not by every item
	// query — the grid lists thousands of rows and does not use them.
	Genres []string `json:"genres,omitempty"`

	// Watched and PositionSecs are the viewer's own state, not the item's.
	// Like Genres they are filled in by the handlers that serve a signed-in
	// viewer; the scanner and the plain item queries leave them zero, so a
	// stored Item never carries another user's progress.
	Watched      bool `json:"watched,omitempty"`
	PositionSecs int  `json:"positionSecs,omitempty"`
	// WatchedAt is when this viewer last touched the item, for a
	// "recently watched" ordering. Empty when they never have.
	WatchedAt string `json:"watchedAt,omitempty"`
}

type ItemRepo interface {
	UpsertItem(ctx context.Context, item Item) (Item, error)
	ItemsByLibrary(ctx context.Context, libraryID string) ([]Item, error)
	ItemsByParent(ctx context.Context, parentID string) ([]Item, error)
	Item(ctx context.Context, id string) (Item, error)
	CountItems(ctx context.Context, libraryID string, kind ItemKind) (int, error)
}

var _ ItemRepo = (*Store)(nil)

// itemColumns returns the item SELECT column list, optionally alias-qualified.
func itemColumns(alias string) string {
	p := func(c string) string {
		if alias == "" {
			return c
		}
		return alias + "." + c
	}
	cols := []string{
		p("id"), p("library_id"), p("kind"), "COALESCE(" + p("parent_id") + ", '')",
		p("title"), p("year"), p("season_num"), p("episode_num"), p("disc_num"),
		p("track_num"), p("artist"), p("path"),
		p("size_bytes"), p("runtime_secs"), p("container"), p("video_codec"),
		p("audio_codec"), p("width"), p("height"), p("tmdb_id"), p("overview"),
		p("poster"), p("added_at"), p("updated_at"), p("version_of"),
		p("rating"), p("vote_count"), p("tagline"), p("backdrop"),
		p("collection_id"), p("collection_name"), p("certification"),
		p("locked_fields"),
	}
	return strings.Join(cols, ", ")
}

var itemCols = itemColumns("")

// itemScanTargets is the single definition of where an item's columns land,
// used by scanItem and by any query that selects itemColumns plus extras.
// Keeping one list means adding a column cannot leave a second scanner
// silently expecting the old count — which it did, twice.
func itemScanTargets(it *Item) []any {
	return []any{&it.ID, &it.LibraryID, &it.Kind, &it.ParentID, &it.Title, &it.Year,
		&it.SeasonNum, &it.EpisodeNum, &it.DiscNum, &it.TrackNum, &it.Artist,
		&it.Path, &it.SizeBytes, &it.RuntimeSecs,
		&it.Container, &it.VideoCodec, &it.AudioCodec, &it.Width, &it.Height,
		&it.TmdbID, &it.Overview, &it.Poster, &it.AddedAt, &it.UpdatedAt, &it.VersionOf,
		&it.Rating, &it.VoteCount, &it.Tagline, &it.Backdrop,
		&it.CollectionID, &it.CollectionName, &it.Certification,
		// Stored as CSV and split on the way out; see lockedFieldsScanner.
		lockedFieldsScanner{it},
	}
}

// lockedFieldsScanner reads the comma-separated locked_fields column into
// the Item's slice. Implementing sql.Scanner keeps it inside
// itemScanTargets, so the single-definition rule above still holds.
type lockedFieldsScanner struct{ it *Item }

func (l lockedFieldsScanner) Scan(src any) error {
	var raw string
	switch v := src.(type) {
	case nil:
	case string:
		raw = v
	case []byte:
		raw = string(v)
	default:
		return fmt.Errorf("locked_fields: unexpected type %T", src)
	}
	l.it.LockedFields = splitLockedFields(raw)
	return nil
}

// splitLockedFields parses the stored CSV, dropping blanks.
func splitLockedFields(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IsLocked reports whether a field was edited by hand and must be left alone
// by anything automatic.
func (i Item) IsLocked(field string) bool {
	for _, f := range i.LockedFields {
		if f == field {
			return true
		}
	}
	return false
}

func scanItem(row interface{ Scan(...any) error }) (Item, error) {
	var it Item
	err := row.Scan(itemScanTargets(&it)...)
	return it, err
}

// UpsertItem inserts the item or, if one exists at (library_id, path),
// refreshes its mutable fields, keeping the original id and added_at.
func (s *Store) UpsertItem(ctx context.Context, item Item) (Item, error) {
	var parent any
	if item.ParentID != "" {
		parent = item.ParentID
	}

	existing := s.db.QueryRowContext(ctx,
		`SELECT id FROM items WHERE library_id = ? AND path = ?`, item.LibraryID, item.Path)
	var id string
	err := existing.Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = uuid.NewString()
		_, err = s.db.ExecContext(ctx, `INSERT INTO items
			(id, library_id, kind, parent_id, title, year, season_num, episode_num,
			 disc_num, track_num, artist, path,
			 size_bytes, runtime_secs, container, video_codec, audio_codec, width, height,
			 tmdb_id, overview, poster,
			 rating, vote_count, tagline, backdrop, collection_id, collection_name,
			 certification)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, item.LibraryID, string(item.Kind), parent, item.Title, item.Year,
			item.SeasonNum, item.EpisodeNum, item.DiscNum, item.TrackNum, item.Artist,
			item.Path, item.SizeBytes, item.RuntimeSecs,
			item.Container, item.VideoCodec, item.AudioCodec, item.Width, item.Height,
			item.TmdbID, item.Overview, item.Poster,
			item.Rating, item.VoteCount, item.Tagline, item.Backdrop,
			item.CollectionID, item.CollectionName, item.Certification)
		if err != nil {
			return Item{}, fmt.Errorf("insert item %q: %w", item.Path, err)
		}
	case err != nil:
		return Item{}, fmt.Errorf("find item %q: %w", item.Path, err)
	default:
		// locked(field) is true when an operator edited that field by hand;
		// anything automatic — this upsert is reached from both the scanner
		// and the metadata refresh — must then leave it alone, or the
		// provider silently undoes the correction it was corrected with.
		// The commas either side make ',title,' match only a whole name.
		locked := func(field string) string {
			return `instr(',' || locked_fields || ',', ',` + field + `,') > 0`
		}
		_, err = s.db.ExecContext(ctx, `UPDATE items SET
			kind = ?, parent_id = ?,
			title = CASE WHEN `+locked("title")+` THEN title ELSE ? END,
			year = CASE WHEN `+locked("year")+` THEN year ELSE ? END,
			season_num = ?, episode_num = ?,
			disc_num = ?, track_num = ?, artist = ?,
			size_bytes = ?, runtime_secs = ?, container = ?, video_codec = ?, audio_codec = ?,
			width = ?, height = ?,
			tmdb_id = CASE WHEN ? != 0 THEN ? ELSE tmdb_id END,
			overview = CASE WHEN `+locked("overview")+` OR ? = '' THEN overview ELSE ? END,
			poster = CASE WHEN `+locked("poster")+` OR ? = '' THEN poster ELSE ? END,
			rating = CASE WHEN `+locked("rating")+` OR ? = 0 THEN rating ELSE ? END,
			vote_count = CASE WHEN ? != 0 THEN ? ELSE vote_count END,
			tagline = CASE WHEN `+locked("tagline")+` OR ? = '' THEN tagline ELSE ? END,
			backdrop = CASE WHEN `+locked("backdrop")+` OR ? = '' THEN backdrop ELSE ? END,
			collection_id = CASE WHEN ? != 0 THEN ? ELSE collection_id END,
			collection_name = CASE WHEN ? != '' THEN ? ELSE collection_name END,
			certification = CASE WHEN `+locked("certification")+` OR ? = '' THEN certification ELSE ? END,
			updated_at = datetime('now')
			WHERE id = ?`,
			string(item.Kind), parent, item.Title, item.Year, item.SeasonNum, item.EpisodeNum,
			item.DiscNum, item.TrackNum, item.Artist,
			item.SizeBytes, item.RuntimeSecs, item.Container, item.VideoCodec, item.AudioCodec,
			item.Width, item.Height,
			item.TmdbID, item.TmdbID, item.Overview, item.Overview, item.Poster, item.Poster,
			item.Rating, item.Rating, item.VoteCount, item.VoteCount,
			item.Tagline, item.Tagline, item.Backdrop, item.Backdrop,
			item.CollectionID, item.CollectionID, item.CollectionName, item.CollectionName,
			item.Certification, item.Certification,
			id)
		if err != nil {
			return Item{}, fmt.Errorf("update item %q: %w", item.Path, err)
		}
	}
	return s.Item(ctx, id)
}

func (s *Store) Item(ctx context.Context, id string) (Item, error) {
	it, err := scanItem(s.db.QueryRowContext(ctx,
		`SELECT `+itemCols+` FROM items WHERE id = ?`, id))
	if err != nil {
		return Item{}, fmt.Errorf("load item %s: %w", id, err)
	}
	return it, nil
}

func (s *Store) ItemsByLibrary(ctx context.Context, libraryID string) ([]Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemCols+` FROM items WHERE library_id = ? AND version_of = ''
		 ORDER BY kind, title, season_num, episode_num`,
		libraryID)
}

// ItemsByLibrarySince returns only the items touched at or after the given
// timestamp (SQLite datetime format), plus the library's current high-water
// mark to pass back next time.
//
// A scan rewrites `updated_at` on every item it sees, so a client polling
// during a scan can ask for just what changed instead of the whole list —
// which at four thousand items is ~200 KB a second for a handful of new
// tiles. The caller still needs the full list once, to start from.
//
// The comparison is inclusive (`>=`). SQLite's datetime('now') has
// one-second resolution, so an exclusive bound would silently drop any item
// written in the same second as the mark — during a scan, that is many. The
// cost is re-sending whatever shares the boundary second, which is small.
//
// Deletions are not expressible this way: a pruned row simply stops
// existing, as does one folded into another as an alternative version.
// Callers reconcile those with a full fetch on the "libraries" event at scan
// end, which is when pruning and version grouping happen.
func (s *Store) ItemsByLibrarySince(ctx context.Context, libraryID, since string) ([]Item, string, error) {
	var mark string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(updated_at), '') FROM items WHERE library_id = ?`, libraryID).Scan(&mark)
	if err != nil {
		return nil, "", fmt.Errorf("items high-water mark: %w", err)
	}
	items, err := s.queryItems(ctx,
		`SELECT `+itemCols+` FROM items
		 WHERE library_id = ? AND version_of = '' AND updated_at >= ?
		 ORDER BY kind, title, season_num, episode_num`,
		libraryID, since)
	if err != nil {
		return nil, "", err
	}
	return items, mark, nil
}

func (s *Store) ItemsByParent(ctx context.Context, parentID string) ([]Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemCols+` FROM items WHERE parent_id = ?
		 ORDER BY disc_num, track_num, season_num, episode_num, year, title`,
		parentID)
}

func (s *Store) CountItems(ctx context.Context, libraryID string, kind ItemKind) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE library_id = ? AND kind = ?`, libraryID, string(kind)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count items: %w", err)
	}
	return n, nil
}

// SearchItems finds items whose title (or artist) matches q, case-insensitive
// substring. Top-level entries (movies, series, artists, albums) rank above
// episodes and tracks.
// SearchItems matches a query against the title, the display artist, the
// release year and the names of credited people — so "Scorsese", "1995" and
// "Goodfellas" all find the same film.
//
// Ranked rather than merely filtered: a title match is what someone typing a
// title expects to see first, and a person match should not bury it. Within
// a rank, films come before episodes and tracks, then alphabetically.
func (s *Store) SearchItems(ctx context.Context, q string, limit int) ([]Item, error) {
	// A four-digit query is a year; anything else would match thousands of
	// runtimes and ids by coincidence.
	year := 0
	if len(q) == 4 {
		if y, err := strconv.Atoi(q); err == nil && y > 1800 && y < 2200 {
			year = y
		}
	}
	return s.queryItems(ctx,
		`SELECT `+itemCols+` FROM items i
		 WHERE i.kind IN ('movie', 'series', 'episode', 'artist', 'album', 'track')
		   AND i.version_of = ''
		   AND (
		     i.title LIKE '%' || ? || '%'
		     OR i.artist LIKE '%' || ? || '%'
		     OR (? != 0 AND i.year = ?)
		     OR EXISTS (
		       SELECT 1 FROM credits c JOIN people p ON p.id = c.person_id
		       WHERE c.item_id = i.id AND p.name LIKE '%' || ? || '%'
		     )
		   )
		 ORDER BY
		   CASE
		     WHEN i.title LIKE ? || '%' THEN 0        -- starts with the query
		     WHEN i.title LIKE '%' || ? || '%' THEN 1 -- contains it
		     WHEN ? != 0 AND i.year = ? THEN 2        -- the year asked for
		     ELSE 3                                   -- matched on a person
		   END,
		   CASE i.kind WHEN 'episode' THEN 1 WHEN 'track' THEN 1 ELSE 0 END,
		   i.title
		 LIMIT ?`,
		q, q, year, year, q,
		q, q, year, year,
		limit)
}

// PruneUnseen deletes a library's items not touched since the given cutoff
// (UTC, sqlite datetime format). Every scan upserts all current items, so
// anything older belongs to files no longer on disk. Cascades to subtitles
// and play state.
func (s *Store) PruneUnseen(ctx context.Context, libraryID, cutoff string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM items WHERE library_id = ? AND updated_at < ?`, libraryID, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune library %s: %w", libraryID, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Store) queryItems(ctx context.Context, query string, args ...any) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query items: %w", err)
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan item: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// GroupVersions links movies in a library that are the same film held more
// than once — a remux beside a 1080p encode, say — so the grid shows one
// entry with the others available as alternative versions.
//
// Films are matched on title and year, which is what the scanner derives
// from the path. The primary is the one a viewer would want by default:
// highest resolution, then largest file, so a remux wins over an encode of
// the same height.
//
// Every run clears the previous grouping first and derives it again from
// what is currently in the library. That makes it idempotent and
// self-healing: a pruned primary, a renamed file or a corrected title all
// resolve on the next scan rather than leaving an item orphaned behind a
// version pointer to something that no longer exists.
func (s *Store) GroupVersions(ctx context.Context, libraryID string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("group versions: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET version_of = '' WHERE library_id = ?`, libraryID); err != nil {
		return 0, fmt.Errorf("clear versions: %w", err)
	}

	// Grouping key: the TMDB id when both rows carry one, otherwise the
	// normalised title and year. The id is the real key — two files of the
	// same film agree on it even when one filename is missing the year or
	// spells the title differently — and title+year remains the fallback
	// for anything unmatched.
	const groupKey = `CASE WHEN tmdb_id > 0 THEN 'tmdb:' || tmdb_id
	                       ELSE 'ty:' || LOWER(title) || '|' || year END`

	// For each duplicate group, everything but the best row points at it.
	res, err := tx.ExecContext(ctx, `
		WITH keyed AS (
			SELECT id, height, size_bytes, `+groupKey+` AS gkey
			FROM items WHERE library_id = ? AND kind = 'movie'
		),
		best AS (
			SELECT gkey, id FROM (
				SELECT gkey, id, ROW_NUMBER() OVER (
					PARTITION BY gkey ORDER BY height DESC, size_bytes DESC, id
				) AS rn FROM keyed
			) WHERE rn = 1
		)
		UPDATE items SET version_of = (
			SELECT b.id FROM keyed k JOIN best b ON b.gkey = k.gkey WHERE k.id = items.id
		)
		WHERE library_id = ? AND kind = 'movie'
		  AND id <> (
			SELECT b.id FROM keyed k JOIN best b ON b.gkey = k.gkey WHERE k.id = items.id
		  )`, libraryID, libraryID)
	if err != nil {
		return 0, fmt.Errorf("link versions: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), tx.Commit()
}

// VersionsOf returns the alternative versions of a primary item, best first.
func (s *Store) VersionsOf(ctx context.Context, itemID string) ([]Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemCols+` FROM items WHERE version_of = ?
		 ORDER BY height DESC, size_bytes DESC`, itemID)
}

// ---- Genres ----

// GenreCount is one genre and how many films in the library carry it.
type GenreCount struct {
	Genre string `json:"genre"`
	Count int    `json:"count"`
}

// ReplaceGenres sets an item's genres, replacing whatever it had.
func (s *Store) ReplaceGenres(ctx context.Context, itemID string, genres []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace genres: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_genres WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("clear genres: %w", err)
	}
	for _, g := range genres {
		if g == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO item_genres (item_id, genre) VALUES (?, ?)`, itemID, g); err != nil {
			return fmt.Errorf("insert genre %q: %w", g, err)
		}
	}
	return tx.Commit()
}

func (s *Store) GenresForItem(ctx context.Context, itemID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT genre FROM item_genres WHERE item_id = ? ORDER BY genre`, itemID)
	if err != nil {
		return nil, fmt.Errorf("genres for %s: %w", itemID, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, fmt.Errorf("scan genre: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// LibraryGenres is the genre list for a library, built from what its films
// actually carry rather than a fixed vocabulary — so it reflects the
// collection, and a genre nothing is tagged with never appears.
//
// Alternative versions are excluded so a film held twice is counted once.
func (s *Store) LibraryGenres(ctx context.Context, libraryID string) ([]GenreCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.genre, COUNT(*) AS n
		FROM item_genres g
		JOIN items i ON i.id = g.item_id
		WHERE i.library_id = ? AND i.version_of = ''
		GROUP BY g.genre
		ORDER BY n DESC, g.genre`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("library genres: %w", err)
	}
	defer rows.Close()
	out := []GenreCount{}
	for rows.Next() {
		var gc GenreCount
		if err := rows.Scan(&gc.Genre, &gc.Count); err != nil {
			return nil, fmt.Errorf("scan genre count: %w", err)
		}
		out = append(out, gc)
	}
	return out, rows.Err()
}

// ItemsByGenre lists a library's films carrying a genre, primaries only.
func (s *Store) ItemsByGenre(ctx context.Context, libraryID, genre string) ([]Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemColumns("i")+` FROM items i
		 JOIN item_genres g ON g.item_id = i.id
		 WHERE i.library_id = ? AND g.genre = ? AND i.version_of = ''
		 ORDER BY i.title`, libraryID, genre)
}

// ItemEdit is an operator's manual correction. Only non-nil fields are
// applied, and every field applied is locked, so a later scan or metadata
// refresh leaves it alone — a correction that the provider could undo would
// not be a correction.
type ItemEdit struct {
	Title         *string   `json:"title"`
	Year          *int      `json:"year"`
	Overview      *string   `json:"overview"`
	Tagline       *string   `json:"tagline"`
	Certification *string   `json:"certification"`
	Rating        *float64  `json:"rating"`
	Poster        *string   `json:"poster"`
	Backdrop      *string   `json:"backdrop"`
	Genres        *[]string `json:"genres"`
}

// editFieldNames maps each settable field to the lock name recorded for it.
func (e ItemEdit) lockNames() []string {
	var out []string
	for name, set := range map[string]bool{
		"title":         e.Title != nil,
		"year":          e.Year != nil,
		"overview":      e.Overview != nil,
		"tagline":       e.Tagline != nil,
		"certification": e.Certification != nil,
		"rating":        e.Rating != nil,
		"poster":        e.Poster != nil,
		"backdrop":      e.Backdrop != nil,
		"genres":        e.Genres != nil,
	} {
		if set {
			out = append(out, name)
		}
	}
	sort.Strings(out) // stable, so the stored CSV does not churn
	return out
}

// UpdateItem applies a manual edit and locks every field it touched.
func (s *Store) UpdateItem(ctx context.Context, id string, e ItemEdit) (Item, error) {
	cur, err := s.Item(ctx, id)
	if err != nil {
		return Item{}, err
	}
	sets := []string{}
	args := []any{}
	add := func(col string, v any) {
		sets = append(sets, col+" = ?")
		args = append(args, v)
	}
	if e.Title != nil {
		if strings.TrimSpace(*e.Title) == "" {
			return Item{}, fmt.Errorf("title cannot be empty")
		}
		add("title", strings.TrimSpace(*e.Title))
	}
	if e.Year != nil {
		add("year", *e.Year)
	}
	if e.Overview != nil {
		add("overview", *e.Overview)
	}
	if e.Tagline != nil {
		add("tagline", *e.Tagline)
	}
	if e.Certification != nil {
		add("certification", strings.TrimSpace(*e.Certification))
	}
	if e.Rating != nil {
		add("rating", *e.Rating)
	}
	if e.Poster != nil {
		add("poster", *e.Poster)
	}
	if e.Backdrop != nil {
		add("backdrop", *e.Backdrop)
	}

	// Union with what was already locked: editing the title must not
	// un-lock a previously corrected year.
	locks := map[string]bool{}
	for _, f := range cur.LockedFields {
		locks[f] = true
	}
	for _, f := range e.lockNames() {
		locks[f] = true
	}
	merged := make([]string, 0, len(locks))
	for f := range locks {
		merged = append(merged, f)
	}
	sort.Strings(merged)
	add("locked_fields", strings.Join(merged, ","))

	if len(sets) > 0 {
		args = append(args, id)
		if _, err := s.db.ExecContext(ctx,
			`UPDATE items SET `+strings.Join(sets, ", ")+`, updated_at = datetime('now') WHERE id = ?`, args...); err != nil {
			return Item{}, fmt.Errorf("update item: %w", err)
		}
	}
	if e.Genres != nil {
		if err := s.ReplaceGenres(ctx, id, *e.Genres); err != nil {
			return Item{}, err
		}
	}
	return s.Item(ctx, id)
}

// SetLockedFields replaces an item's lock list outright, for clearing a lock
// so the provider may fill the field again.
func (s *Store) SetLockedFields(ctx context.Context, id string, fields []string) error {
	sort.Strings(fields)
	_, err := s.db.ExecContext(ctx,
		`UPDATE items SET locked_fields = ? WHERE id = ?`, strings.Join(fields, ","), id)
	if err != nil {
		return fmt.Errorf("set locked fields: %w", err)
	}
	return nil
}
