package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type ItemKind string

const (
	KindMovie   ItemKind = "movie"
	KindSeries  ItemKind = "series"
	KindSeason  ItemKind = "season"
	KindEpisode ItemKind = "episode"
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
}

type ItemRepo interface {
	UpsertItem(ctx context.Context, item Item) (Item, error)
	ItemsByLibrary(ctx context.Context, libraryID string) ([]Item, error)
	ItemsByParent(ctx context.Context, parentID string) ([]Item, error)
	Item(ctx context.Context, id string) (Item, error)
	CountItems(ctx context.Context, libraryID string, kind ItemKind) (int, error)
}

var _ ItemRepo = (*Store)(nil)

const itemCols = `id, library_id, kind, COALESCE(parent_id, ''), title, year, season_num,
	episode_num, path, size_bytes, runtime_secs, container, video_codec, audio_codec,
	width, height, tmdb_id, overview, poster, added_at, updated_at`

func scanItem(row interface{ Scan(...any) error }) (Item, error) {
	var it Item
	err := row.Scan(&it.ID, &it.LibraryID, &it.Kind, &it.ParentID, &it.Title, &it.Year,
		&it.SeasonNum, &it.EpisodeNum, &it.Path, &it.SizeBytes, &it.RuntimeSecs,
		&it.Container, &it.VideoCodec, &it.AudioCodec, &it.Width, &it.Height,
		&it.TmdbID, &it.Overview, &it.Poster, &it.AddedAt, &it.UpdatedAt)
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
			(id, library_id, kind, parent_id, title, year, season_num, episode_num, path,
			 size_bytes, runtime_secs, container, video_codec, audio_codec, width, height,
			 tmdb_id, overview, poster)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, item.LibraryID, string(item.Kind), parent, item.Title, item.Year,
			item.SeasonNum, item.EpisodeNum, item.Path, item.SizeBytes, item.RuntimeSecs,
			item.Container, item.VideoCodec, item.AudioCodec, item.Width, item.Height,
			item.TmdbID, item.Overview, item.Poster)
		if err != nil {
			return Item{}, fmt.Errorf("insert item %q: %w", item.Path, err)
		}
	case err != nil:
		return Item{}, fmt.Errorf("find item %q: %w", item.Path, err)
	default:
		_, err = s.db.ExecContext(ctx, `UPDATE items SET
			kind = ?, parent_id = ?, title = ?, year = ?, season_num = ?, episode_num = ?,
			size_bytes = ?, runtime_secs = ?, container = ?, video_codec = ?, audio_codec = ?,
			width = ?, height = ?,
			tmdb_id = CASE WHEN ? != 0 THEN ? ELSE tmdb_id END,
			overview = CASE WHEN ? != '' THEN ? ELSE overview END,
			poster = CASE WHEN ? != '' THEN ? ELSE poster END,
			updated_at = datetime('now')
			WHERE id = ?`,
			string(item.Kind), parent, item.Title, item.Year, item.SeasonNum, item.EpisodeNum,
			item.SizeBytes, item.RuntimeSecs, item.Container, item.VideoCodec, item.AudioCodec,
			item.Width, item.Height,
			item.TmdbID, item.TmdbID, item.Overview, item.Overview, item.Poster, item.Poster,
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
		`SELECT `+itemCols+` FROM items WHERE library_id = ? ORDER BY kind, title, season_num, episode_num`,
		libraryID)
}

func (s *Store) ItemsByParent(ctx context.Context, parentID string) ([]Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemCols+` FROM items WHERE parent_id = ? ORDER BY season_num, episode_num, title`,
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
