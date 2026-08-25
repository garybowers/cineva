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
	PlayStateFor(ctx context.Context, userID, itemID string) (PlayState, error)
	ContinueWatching(ctx context.Context, userID string, limit int) ([]ResumeItem, error)
	NextEpisode(ctx context.Context, episodeID string) (Item, bool, error)
	PrevEpisode(ctx context.Context, episodeID string) (Item, bool, error)
}

var _ PlayStateRepo = (*Store)(nil)

func (s *Store) SetProgress(ctx context.Context, userID, itemID string, positionSecs, runtimeSecs int) (PlayState, error) {
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
		// Mirrors scanItem's column order, plus the trailing position column.
		var r ResumeItem
		if err := rows.Scan(&r.ID, &r.LibraryID, &r.Kind, &r.ParentID, &r.Title, &r.Year,
			&r.SeasonNum, &r.EpisodeNum, &r.DiscNum, &r.TrackNum, &r.Artist,
			&r.Path, &r.SizeBytes, &r.RuntimeSecs,
			&r.Container, &r.VideoCodec, &r.AudioCodec, &r.Width, &r.Height,
			&r.TmdbID, &r.Overview, &r.Poster, &r.AddedAt, &r.UpdatedAt, &r.PositionSecs); err != nil {
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
