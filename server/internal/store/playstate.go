package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LocalUser is the placeholder user id until auth lands in milestone 4.
const LocalUser = "local"

// watchedFraction: crossing this fraction of runtime marks an item watched
// and advances next-up (see CLAUDE.md: player).
const watchedFraction = 0.9

type PlayState struct {
	ItemID       string `json:"itemId"`
	PositionSecs int    `json:"positionSecs"`
	Watched      bool   `json:"watched"`
	UpdatedAt    string `json:"updatedAt"`
}

// ResumeItem is an item with the user's position, for continue-watching rows.
type ResumeItem struct {
	Item
	PositionSecs int `json:"positionSecs"`
}

type PlayStateRepo interface {
	SetProgress(ctx context.Context, userID, itemID string, positionSecs, runtimeSecs int) (PlayState, error)
	SetWatched(ctx context.Context, userID, itemID string, watched bool) (PlayState, error)
	PlayStateFor(ctx context.Context, userID, itemID string) (PlayState, error)
	ContinueWatching(ctx context.Context, userID string, limit int) ([]ResumeItem, error)
	NextEpisode(ctx context.Context, episodeID string) (Item, bool, error)
	PrevEpisode(ctx context.Context, episodeID string) (Item, bool, error)
}

var _ PlayStateRepo = (*Store)(nil)

func (s *Store) SetProgress(ctx context.Context, userID, itemID string, positionSecs, runtimeSecs int) (PlayState, error) {
	// Recorded against the primary so every version of a film shares one
	// resume position (see primaryID).
	itemID = s.primaryID(ctx, itemID)
	watched := 0
	if runtimeSecs > 0 && float64(positionSecs) >= watchedFraction*float64(runtimeSecs) {
		watched = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO play_state (user_id, item_id, position_secs, watched)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, item_id) DO UPDATE SET
			position_secs = excluded.position_secs,
			watched = MAX(watched, excluded.watched),
			updated_at = datetime('now')`,
		userID, itemID, positionSecs, watched)
	if err != nil {
		return PlayState{}, fmt.Errorf("set progress: %w", err)
	}
	return s.PlayStateFor(ctx, userID, itemID)
}

func (s *Store) PlayStateFor(ctx context.Context, userID, itemID string) (PlayState, error) {
	itemID = s.primaryID(ctx, itemID)
	var ps PlayState
	var watched int
	err := s.db.QueryRowContext(ctx,
		`SELECT item_id, position_secs, watched, updated_at FROM play_state
		 WHERE user_id = ? AND item_id = ?`, userID, itemID).
		Scan(&ps.ItemID, &ps.PositionSecs, &watched, &ps.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PlayState{ItemID: itemID}, nil
	}
	if err != nil {
		return PlayState{}, fmt.Errorf("play state: %w", err)
	}
	ps.Watched = watched == 1
	return ps, nil
}

func (s *Store) ContinueWatching(ctx context.Context, userID string, limit int) ([]ResumeItem, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+itemColumns("i")+`, p.position_secs FROM play_state p
		 JOIN items i ON i.id = p.item_id
		 WHERE p.user_id = ? AND p.watched = 0 AND p.position_secs > 10
		 ORDER BY p.updated_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("continue watching: %w", err)
	}
	defer rows.Close()
	out := []ResumeItem{}
	for rows.Next() {
		// The item's own columns, then the trailing position. Sharing
		// itemScanTargets means a new item column cannot desynchronise this.
		var r ResumeItem
		targets := append(itemScanTargets(&r.Item), &r.PositionSecs)
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("scan resume item: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// NextEpisode returns the episode after the given one within its series,
// crossing season boundaries.
func (s *Store) NextEpisode(ctx context.Context, episodeID string) (Item, bool, error) {
	return s.adjacentEpisode(ctx, episodeID, 1)
}

// PrevEpisode is NextEpisode's mirror, for the player's back button.
func (s *Store) PrevEpisode(ctx context.Context, episodeID string) (Item, bool, error) {
	return s.adjacentEpisode(ctx, episodeID, -1)
}

func (s *Store) adjacentEpisode(ctx context.Context, episodeID string, step int) (Item, bool, error) {
	ep, err := s.Item(ctx, episodeID)
	if err != nil || ep.Kind != KindEpisode || ep.ParentID == "" {
		return Item{}, false, err
	}
	season, err := s.Item(ctx, ep.ParentID)
	if err != nil || season.ParentID == "" {
		return Item{}, false, err
	}
	episodes, err := s.queryItems(ctx,
		`SELECT `+itemColumns("e")+` FROM items e
		 JOIN items season ON e.parent_id = season.id
		 WHERE season.parent_id = ? AND e.kind = 'episode'
		 ORDER BY e.season_num, e.episode_num`, season.ParentID)
	if err != nil {
		return Item{}, false, err
	}
	for i, e := range episodes {
		if e.ID == episodeID {
			if j := i + step; j >= 0 && j < len(episodes) {
				return episodes[j], true, nil
			}
			break
		}
	}
	return Item{}, false, nil
}

// SetWatched marks an item watched or unwatched explicitly, rather than
// letting the ~90% threshold decide.
//
// Both directions clear the resume position, which is what the viewer means
// either way: marking watched should stop it offering to resume, and marking
// unwatched should start it from the beginning. Note the MAX() in
// SetProgress, which only ever raises the flag — without this, nothing could
// ever lower it.
func (s *Store) SetWatched(ctx context.Context, userID, itemID string, watched bool) (PlayState, error) {
	itemID = s.primaryID(ctx, itemID)
	w := 0
	if watched {
		w = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO play_state (user_id, item_id, position_secs, watched)
		VALUES (?, ?, 0, ?)
		ON CONFLICT (user_id, item_id) DO UPDATE SET
			position_secs = 0,
			watched = excluded.watched,
			updated_at = datetime('now')`,
		userID, itemID, w)
	if err != nil {
		return PlayState{}, fmt.Errorf("set watched: %w", err)
	}
	return s.PlayStateFor(ctx, userID, itemID)
}

// primaryID maps a version to the film it is a version of, so that watch
// state is shared across the versions of one title: starting a 4K rip and
// resuming on the 1080p one continues where it left off rather than from
// zero. A row that is not a version, or an id that no longer exists, maps
// to itself.
func (s *Store) primaryID(ctx context.Context, itemID string) string {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT CASE WHEN version_of <> '' THEN version_of ELSE id END FROM items WHERE id = ?`,
		itemID).Scan(&id)
	if err != nil || id == "" {
		return itemID
	}
	return id
}

// PlayStatesByLibrary returns every play-state row this user has for items in
// one library, keyed by item id. The grid needs watched/resume for thousands
// of tiles at once, and a per-item query would be a request per tile.
//
// Keyed by the *primary* id, matching primaryID, so a grid tile (always a
// primary) finds the state recorded while playing any of its versions.
func (s *Store) PlayStatesByLibrary(ctx context.Context, userID, libraryID string) (map[string]PlayState, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT CASE WHEN i.version_of <> '' THEN i.version_of ELSE i.id END,
		        p.position_secs, p.watched, p.updated_at
		 FROM play_state p JOIN items i ON i.id = p.item_id
		 WHERE p.user_id = ? AND i.library_id = ?`, userID, libraryID)
	if err != nil {
		return nil, fmt.Errorf("play states by library: %w", err)
	}
	defer rows.Close()
	out := map[string]PlayState{}
	for rows.Next() {
		var ps PlayState
		var watched int
		if err := rows.Scan(&ps.ItemID, &ps.PositionSecs, &watched, &ps.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan play state: %w", err)
		}
		ps.Watched = watched == 1
		// Two versions of one film can each carry a row; keep the most
		// recently touched, which is the one the viewer last watched.
		if prev, ok := out[ps.ItemID]; ok && prev.UpdatedAt > ps.UpdatedAt {
			continue
		}
		out[ps.ItemID] = ps
	}
	return out, rows.Err()
}
