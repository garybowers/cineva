package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Collection is a set of films presented as one — "The Matrix Collection".
//
// Two kinds share this type. A *provider* collection is TMDB's own grouping,
// derived on the fly from items.collection_id; a *managed* one is a row in
// the collections table that the operator has edited. Managed collections
// hide the provider collection they override, so a manual change survives
// rescans.
type Collection struct {
	// Key identifies a collection in URLs and API calls: "tmdb:91361" for a
	// provider collection, the row id for a managed one. A single opaque
	// string means callers never have to know which kind they hold.
	Key    string `json:"key"`
	TmdbID int    `json:"tmdbId,omitempty"`
	Name   string `json:"name"`
	Count  int    `json:"count"`
	Poster string `json:"poster,omitempty"`
	// Managed is true once an operator has edited it: membership and order
	// then come from the database rather than the provider.
	Managed bool `json:"managed"`

	// Watched is how many of the held films this viewer has finished, and
	// NextUpID the first one they have not, in the collection's own order —
	// the film to play to carry on through the set. NextUpID is empty when
	// the whole collection has been watched.
	Watched    int    `json:"watched"`
	NextUpID   string `json:"nextUpId,omitempty"`
	NextUpName string `json:"nextUpName,omitempty"`
}

const providerKeyPrefix = "tmdb:"

func providerKey(tmdbID int) string { return providerKeyPrefix + strconv.Itoa(tmdbID) }

// ParseCollectionKey splits a collection key into its kind, for callers
// outside the package that need to tell a managed collection from a
// provider one.
func ParseCollectionKey(key string) (tmdbID int, managedID string) { return parseKey(key) }

// parseKey splits a collection key into its kind.
func parseKey(key string) (tmdbID int, managedID string) {
	if rest, ok := strings.CutPrefix(key, providerKeyPrefix); ok {
		id, _ := strconv.Atoi(rest)
		return id, ""
	}
	return 0, key
}

// LibraryCollections lists the collections represented in a library with this
// viewer's progress through each, managed ones first, then provider ones by
// size.
//
// A provider collection the library holds only one film from is omitted:
// labelling a single film as a set is noise, not grouping. A managed
// collection is always listed, however few films it holds — the operator
// made it deliberately.
//
// userID may be empty, in which case progress reads as nothing watched.
func (s *Store) LibraryCollections(ctx context.Context, userID, libraryID string) ([]Collection, error) {
	managed, overridden, err := s.managedCollections(ctx, userID, libraryID)
	if err != nil {
		return nil, err
	}
	provider, err := s.providerCollections(ctx, userID, libraryID, overridden)
	if err != nil {
		return nil, err
	}
	return append(managed, provider...), nil
}

// managedCollections reads the operator's own collections, and reports which
// provider ids they override so those can be skipped.
func (s *Store) managedCollections(ctx context.Context, userID, libraryID string) ([]Collection, map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.tmdb_id, c.name,
		       COUNT(ci.item_id) AS n,
		       SUM(CASE WHEN ps.watched = 1 THEN 1 ELSE 0 END) AS seen,
		       COALESCE((
		         SELECT i2.poster FROM collection_items ci2
		         JOIN items i2 ON i2.id = ci2.item_id
		         WHERE ci2.collection_id = c.id AND i2.poster <> ''
		         ORDER BY ci2.position LIMIT 1
		       ), '') AS poster,
		       COALESCE((
		         SELECT ci3.item_id FROM collection_items ci3
		         LEFT JOIN play_state ps3 ON ps3.item_id = ci3.item_id AND ps3.user_id = ?
		         WHERE ci3.collection_id = c.id AND COALESCE(ps3.watched, 0) = 0
		         ORDER BY ci3.position LIMIT 1
		       ), '') AS next_id,
		       COALESCE((
		         SELECT i4.title FROM collection_items ci4
		         JOIN items i4 ON i4.id = ci4.item_id
		         LEFT JOIN play_state ps4 ON ps4.item_id = ci4.item_id AND ps4.user_id = ?
		         WHERE ci4.collection_id = c.id AND COALESCE(ps4.watched, 0) = 0
		         ORDER BY ci4.position LIMIT 1
		       ), '') AS next_title
		FROM collections c
		LEFT JOIN collection_items ci ON ci.collection_id = c.id
		LEFT JOIN play_state ps ON ps.item_id = ci.item_id AND ps.user_id = ?
		WHERE c.library_id = ?
		GROUP BY c.id, c.tmdb_id, c.name
		ORDER BY c.name`,
		userID, userID, userID, libraryID)
	if err != nil {
		return nil, nil, fmt.Errorf("managed collections: %w", err)
	}
	defer rows.Close()
	out := []Collection{}
	overridden := map[int]bool{}
	for rows.Next() {
		c := Collection{Managed: true}
		if err := rows.Scan(&c.Key, &c.TmdbID, &c.Name, &c.Count, &c.Watched, &c.Poster, &c.NextUpID, &c.NextUpName); err != nil {
			return nil, nil, fmt.Errorf("scan managed collection: %w", err)
		}
		if c.TmdbID > 0 {
			overridden[c.TmdbID] = true
		}
		out = append(out, c)
	}
	return out, overridden, rows.Err()
}

// providerCollections derives collections from the provider's own grouping,
// skipping any an operator has taken over.
func (s *Store) providerCollections(ctx context.Context, userID, libraryID string, skip map[int]bool) ([]Collection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.collection_id, i.collection_name, COUNT(*) AS n,
		       SUM(CASE WHEN ps.watched = 1 THEN 1 ELSE 0 END) AS seen,
		       COALESCE((
		         SELECT p.poster FROM items p
		         WHERE p.library_id = i.library_id
		           AND p.collection_id = i.collection_id
		           AND p.version_of = '' AND p.poster <> ''
		         ORDER BY p.year, p.title LIMIT 1
		       ), '') AS poster,
		       COALESCE((
		         SELECT nx.id FROM items nx
		         LEFT JOIN play_state nps ON nps.item_id = nx.id AND nps.user_id = ?
		         WHERE nx.library_id = i.library_id
		           AND nx.collection_id = i.collection_id
		           AND nx.version_of = ''
		           AND COALESCE(nps.watched, 0) = 0
		         ORDER BY nx.year, nx.title LIMIT 1
		       ), '') AS next_id,
		       COALESCE((
		         SELECT nx.title FROM items nx
		         LEFT JOIN play_state nps ON nps.item_id = nx.id AND nps.user_id = ?
		         WHERE nx.library_id = i.library_id
		           AND nx.collection_id = i.collection_id
		           AND nx.version_of = ''
		           AND COALESCE(nps.watched, 0) = 0
		         ORDER BY nx.year, nx.title LIMIT 1
		       ), '') AS next_title
		FROM items i
		LEFT JOIN play_state ps ON ps.item_id = i.id AND ps.user_id = ?
		WHERE i.library_id = ? AND i.version_of = '' AND i.collection_id > 0
		GROUP BY i.collection_id, i.collection_name
		HAVING n > 1
		ORDER BY n DESC, i.collection_name`,
		userID, userID, userID, libraryID)
	if err != nil {
		return nil, fmt.Errorf("provider collections: %w", err)
	}
	defer rows.Close()
	out := []Collection{}
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.TmdbID, &c.Name, &c.Count, &c.Watched, &c.Poster, &c.NextUpID, &c.NextUpName); err != nil {
			return nil, fmt.Errorf("scan provider collection: %w", err)
		}
		if skip[c.TmdbID] {
			continue
		}
		c.Key = providerKey(c.TmdbID)
		out = append(out, c)
	}
	return out, rows.Err()
}

// CollectionByKey returns one collection's summary, or false when the library
// has none by that key.
func (s *Store) CollectionByKey(ctx context.Context, userID, libraryID, key string) (Collection, bool, error) {
	cols, err := s.LibraryCollections(ctx, userID, libraryID)
	if err != nil {
		return Collection{}, false, err
	}
	for _, c := range cols {
		if c.Key == key {
			return c, true, nil
		}
	}
	return Collection{}, false, nil
}

// ItemsByCollection returns a collection's films in its own order: the
// operator's for a managed collection, release order for a provider one —
// which is how a series is watched when nobody has said otherwise.
func (s *Store) ItemsByCollection(ctx context.Context, libraryID, key string) ([]Item, error) {
	tmdbID, managedID := parseKey(key)
	if managedID != "" {
		return s.queryItems(ctx,
			`SELECT `+itemColumns("i")+` FROM collection_items ci
			 JOIN items i ON i.id = ci.item_id
			 WHERE ci.collection_id = ? AND i.library_id = ?
			 ORDER BY ci.position`, managedID, libraryID)
	}
	return s.queryItems(ctx,
		`SELECT `+itemCols+` FROM items
		 WHERE library_id = ? AND collection_id = ? AND version_of = ''
		 ORDER BY year, title`, libraryID, tmdbID)
}

// CreateCollection makes a wholly operator-defined collection.
func (s *Store) CreateCollection(ctx context.Context, libraryID, name string) (Collection, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Collection{}, fmt.Errorf("collection name is required")
	}
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO collections (id, library_id, tmdb_id, name) VALUES (?, ?, 0, ?)`,
		id, libraryID, name)
	if err != nil {
		return Collection{}, fmt.Errorf("create collection: %w", err)
	}
	return Collection{Key: id, Name: name, Managed: true}, nil
}

// AdoptCollection takes a provider collection under operator control,
// copying its current membership and release order in so the first edit
// starts from what was already on screen rather than an empty list.
//
// Already-managed keys are returned unchanged, so callers can adopt
// unconditionally before an edit.
func (s *Store) AdoptCollection(ctx context.Context, libraryID, key string) (string, error) {
	tmdbID, managedID := parseKey(key)
	if managedID != "" {
		return managedID, nil // already managed
	}
	if tmdbID == 0 {
		return "", fmt.Errorf("adopt collection: bad key %q", key)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("adopt collection: %w", err)
	}
	defer tx.Rollback()

	// Someone may have adopted it already between list and edit.
	var existing string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM collections WHERE library_id = ? AND tmdb_id = ?`, libraryID, tmdbID).Scan(&existing)
	if err == nil {
		return existing, tx.Commit()
	}

	var name string
	if err := tx.QueryRowContext(ctx,
		`SELECT collection_name FROM items
		 WHERE library_id = ? AND collection_id = ? AND version_of = '' LIMIT 1`,
		libraryID, tmdbID).Scan(&name); err != nil {
		return "", fmt.Errorf("adopt collection %d: %w", tmdbID, err)
	}
	id := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO collections (id, library_id, tmdb_id, name) VALUES (?, ?, ?, ?)`,
		id, libraryID, tmdbID, name); err != nil {
		return "", fmt.Errorf("adopt collection: %w", err)
	}
	// Seeded in release order — the order it was being shown in.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO collection_items (collection_id, item_id, position)
		SELECT ?, id, ROW_NUMBER() OVER (ORDER BY year, title) - 1
		FROM items
		WHERE library_id = ? AND collection_id = ? AND version_of = ''`,
		id, libraryID, tmdbID); err != nil {
		return "", fmt.Errorf("seed collection: %w", err)
	}
	return id, tx.Commit()
}

// RenameCollection renames a managed collection.
func (s *Store) RenameCollection(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("collection name is required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE collections SET name = ?, updated_at = datetime('now') WHERE id = ?`, name, id)
	if err != nil {
		return fmt.Errorf("rename collection: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCollection removes a managed collection. Where it overrode a
// provider one, that provider grouping reappears — deleting an override is
// how an operator reverts to the provider's own answer. The films themselves
// are untouched.
func (s *Store) DeleteCollection(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM collections WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCollectionItems replaces a managed collection's membership and order in
// one go. The slice is the order, so adding, removing and reordering are all
// the same call — the client sends the list it wants and does not have to
// describe how it differs from the list it had.
//
// Ids not in the library are ignored rather than rejected, so a stale client
// list cannot fail the whole edit.
func (s *Store) SetCollectionItems(ctx context.Context, id string, itemIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set collection items: %w", err)
	}
	defer tx.Rollback()

	var libraryID string
	if err := tx.QueryRowContext(ctx, `SELECT library_id FROM collections WHERE id = ?`, id).Scan(&libraryID); err != nil {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM collection_items WHERE collection_id = ?`, id); err != nil {
		return fmt.Errorf("clear collection items: %w", err)
	}
	pos := 0
	seen := map[string]bool{}
	for _, itemID := range itemIDs {
		if seen[itemID] {
			continue
		}
		seen[itemID] = true
		res, err := tx.ExecContext(ctx, `
			INSERT INTO collection_items (collection_id, item_id, position)
			SELECT ?, id, ? FROM items WHERE id = ? AND library_id = ?`,
			id, pos, itemID, libraryID)
		if err != nil {
			return fmt.Errorf("add collection item: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			pos++
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE collections SET updated_at = datetime('now') WHERE id = ?`, id); err != nil {
		return fmt.Errorf("touch collection: %w", err)
	}
	return tx.Commit()
}
