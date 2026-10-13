package store

import (
	"context"
	"fmt"
	"strings"
)

// ManageFilter narrows the admin's Manage list. The zero value matches the
// whole library.
type ManageFilter struct {
	// Query is a case-insensitive substring of the title or the path.
	Query string
	// Unmatched keeps only items with no TMDB id.
	Unmatched bool
}

// ManagePage is one page of the Manage list plus the counts around it.
type ManagePage struct {
	Items []Item
	// Total is how many items match the filter, across every page.
	Total int
	// Matched and All describe the whole library regardless of the filter,
	// for the "X of N matched" line.
	Matched int
	All     int
}

// manageWhere is the filter shared by the page query, its count and the id
// listing a filtered refresh uses, so all three always agree on what
// "matching" means. The same rows as ItemsByLibrary: every kind, primaries
// only.
//
// LIKE is case-insensitive for ASCII in SQLite, which covers titles and
// paths well enough; the query's own % and _ are escaped so a path such as
// "the_thing" matches literally rather than as a pattern.
func manageWhere(libraryID string, f ManageFilter) (string, []any) {
	where := `library_id = ? AND version_of = ''`
	args := []any{libraryID}
	if q := strings.TrimSpace(f.Query); q != "" {
		where += ` AND (title LIKE ? ESCAPE '\' OR path LIKE ? ESCAPE '\')`
		pat := "%" + likeEscaper.Replace(q) + "%"
		args = append(args, pat, pat)
	}
	if f.Unmatched {
		where += ` AND tmdb_id = 0`
	}
	return where, args
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// ManageItems returns one page of a library's items for the admin's Manage
// list, in the same order as ItemsByLibrary, with the counts the screen
// shows. Filtering and paging happen here rather than in the browser so a
// library of tens of thousands of items costs one page per request.
func (s *Store) ManageItems(ctx context.Context, libraryID string, f ManageFilter, offset, limit int) (ManagePage, error) {
	var page ManagePage
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(tmdb_id != 0), 0) FROM items
		 WHERE library_id = ? AND version_of = ''`, libraryID).Scan(&page.All, &page.Matched)
	if err != nil {
		return ManagePage{}, fmt.Errorf("count library items: %w", err)
	}
	where, args := manageWhere(libraryID, f)
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE `+where, args...).Scan(&page.Total); err != nil {
		return ManagePage{}, fmt.Errorf("count matching items: %w", err)
	}
	// id breaks ties, so two items sharing a title cannot swap between
	// pages and appear twice or not at all.
	page.Items, err = s.queryItems(ctx,
		`SELECT `+itemCols+` FROM items WHERE `+where+`
		 ORDER BY kind, title, season_num, episode_num, id
		 LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return ManagePage{}, err
	}
	return page, nil
}

// ManageItemIDs lists the ids of every item matching the filter, for a
// metadata refresh of "everything the filter matches" across all pages.
func (s *Store) ManageItemIDs(ctx context.Context, libraryID string, f ManageFilter) ([]string, error) {
	where, args := manageWhere(libraryID, f)
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM items WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list matching items: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan item id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list matching items: %w", err)
	}
	return ids, nil
}
