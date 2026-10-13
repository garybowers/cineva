package store

import (
	"context"
	"fmt"
	"strings"
)

// ItemsByIDs loads the given items in the order asked for, skipping any
// that no longer exist — a queue handed between devices can name an item a
// rescan has since removed.
func (s *Store) ItemsByIDs(ctx context.Context, ids []string) ([]Item, error) {
	out := []Item{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	found, err := s.queryItems(ctx, `SELECT `+itemCols+` FROM items WHERE id IN (?`+
		strings.Repeat(", ?", len(ids)-1)+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("items by id: %w", err)
	}
	byID := make(map[string]Item, len(found))
	for _, it := range found {
		byID[it.ID] = it
	}
	for _, id := range ids {
		if it, ok := byID[id]; ok {
			out = append(out, it)
		}
	}
	return out, nil
}
