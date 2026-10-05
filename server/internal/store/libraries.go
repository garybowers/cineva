package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type MediaType string

const (
	MediaMovies     MediaType = "movies"
	MediaTV         MediaType = "tv"
	MediaMusic      MediaType = "music"
	MediaAudiobooks MediaType = "audiobooks"
	MediaPodcasts   MediaType = "podcasts"
	MediaBooks      MediaType = "books"
)

func ValidMediaType(t MediaType) bool {
	switch t {
	case MediaMovies, MediaTV, MediaMusic, MediaAudiobooks, MediaPodcasts, MediaBooks:
		return true
	}
	return false
}

type Library struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MediaType MediaType `json:"mediaType"`
	Source    string    `json:"source"`
	Paths     []string  `json:"paths"`
	CreatedAt string    `json:"createdAt"`
	UpdatedAt string    `json:"updatedAt"`
}

// LibraryRepo is what services depend on; *Store implements it.
type LibraryRepo interface {
	CreateLibrary(ctx context.Context, name string, mediaType MediaType, paths []string) (Library, error)
	Libraries(ctx context.Context) ([]Library, error)
	Library(ctx context.Context, id string) (Library, error)
	DeleteLibrary(ctx context.Context, id string) error
}

var _ LibraryRepo = (*Store)(nil)

func (s *Store) CreateLibrary(ctx context.Context, name string, mediaType MediaType, paths []string) (Library, error) {
	if name == "" {
		return Library{}, fmt.Errorf("library name is required")
	}
	if !ValidMediaType(mediaType) {
		return Library{}, fmt.Errorf("invalid media type %q", mediaType)
	}
	if len(paths) == 0 {
		return Library{}, fmt.Errorf("a local library needs at least one path")
	}

	id := uuid.NewString()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Library{}, fmt.Errorf("create library: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO libraries (id, name, media_type) VALUES (?, ?, ?)`,
		id, name, string(mediaType)); err != nil {
		return Library{}, fmt.Errorf("create library %q: %w", name, err)
	}
	for _, p := range paths {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO library_paths (library_id, path) VALUES (?, ?)`, id, p); err != nil {
			return Library{}, fmt.Errorf("add path %q: %w", p, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Library{}, fmt.Errorf("create library %q: %w", name, err)
	}
	return s.Library(ctx, id)
}

func (s *Store) Libraries(ctx context.Context) ([]Library, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, media_type, source, created_at, updated_at FROM libraries ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	defer rows.Close()

	libs := []Library{}
	for rows.Next() {
		var l Library
		if err := rows.Scan(&l.ID, &l.Name, &l.MediaType, &l.Source, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan library: %w", err)
		}
		libs = append(libs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	for i := range libs {
		if libs[i].Paths, err = s.libraryPaths(ctx, libs[i].ID); err != nil {
			return nil, err
		}
	}
	return libs, nil
}

func (s *Store) Library(ctx context.Context, id string) (Library, error) {
	var l Library
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, media_type, source, created_at, updated_at FROM libraries WHERE id = ?`, id).
		Scan(&l.ID, &l.Name, &l.MediaType, &l.Source, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return Library{}, fmt.Errorf("load library %s: %w", id, err)
	}
	if l.Paths, err = s.libraryPaths(ctx, id); err != nil {
		return Library{}, err
	}
	return l, nil
}

func (s *Store) DeleteLibrary(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete library %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("library %s not found", id)
	}
	return nil
}

func (s *Store) libraryPaths(ctx context.Context, libraryID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT path FROM library_paths WHERE library_id = ? ORDER BY path`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("paths for library %s: %w", libraryID, err)
	}
	defer rows.Close()

	paths := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan path: %w", err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// UpdateLibrary changes a library's name and/or its paths.
//
// The media type is deliberately not changeable: a library is created with
// one and the scanners, item kinds and per-user state all key off it, so
// changing it would orphan everything already scanned. Delete and recreate
// is the honest way to do that.
//
// Paths are replaced wholesale, which is how the caller sends them. Removing
// a path does not delete the items under it here — the next scan prunes
// anything it no longer finds, which keeps one rule for "the files went
// away" rather than two.
func (s *Store) UpdateLibrary(ctx context.Context, id, name string, paths []string) (Library, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Library{}, fmt.Errorf("library name is required")
	}
	clean := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return Library{}, fmt.Errorf("a local library needs at least one path")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Library{}, fmt.Errorf("update library: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE libraries SET name = ?, updated_at = datetime('now') WHERE id = ?`, name, id)
	if err != nil {
		return Library{}, fmt.Errorf("update library: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Library{}, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_paths WHERE library_id = ?`, id); err != nil {
		return Library{}, fmt.Errorf("clear library paths: %w", err)
	}
	for _, p := range clean {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO library_paths (library_id, path) VALUES (?, ?)`, id, p); err != nil {
			return Library{}, fmt.Errorf("add path %q: %w", p, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Library{}, fmt.Errorf("update library: %w", err)
	}
	return s.Library(ctx, id)
}
