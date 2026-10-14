package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/garybowers/cineva/server/internal/lang"
)

// AudioTrack is one embedded audio stream of an item.
type AudioTrack struct {
	ID          string `json:"id"`
	ItemID      string `json:"itemId"`
	StreamIndex int    `json:"streamIndex"`
	Codec       string `json:"codec"`
	Language    string `json:"language,omitempty"`
	// LanguageName is Language in English ("French" for "fre"), for a
	// client to show; empty when the tag names no language the server
	// knows. Not stored: it is filled in when the tracks are read.
	LanguageName string `json:"languageName,omitempty"`
	Title        string `json:"title,omitempty"`
	Channels     int    `json:"channels,omitempty"`
	BitRate      int64  `json:"bitRate,omitempty"`
	Default      bool   `json:"default,omitempty"`
}

type AudioTrackRepo interface {
	ReplaceAudioTracks(ctx context.Context, itemID string, tracks []AudioTrack) error
	AudioTracksForItem(ctx context.Context, itemID string) ([]AudioTrack, error)
}

var _ AudioTrackRepo = (*Store)(nil)

func (s *Store) ReplaceAudioTracks(ctx context.Context, itemID string, tracks []AudioTrack) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace audio tracks: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM audio_tracks WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("clear audio tracks: %w", err)
	}
	for _, t := range tracks {
		def := 0
		if t.Default {
			def = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO audio_tracks (id, item_id, stream_index, codec, language, title, channels, bitrate, is_default)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), itemID, t.StreamIndex, t.Codec, t.Language, t.Title, t.Channels, t.BitRate, def); err != nil {
			return fmt.Errorf("insert audio track: %w", err)
		}
	}
	return tx.Commit()
}

const audioTrackCols = `id, item_id, stream_index, codec, language, title, channels, bitrate, is_default`

func (s *Store) AudioTracksForItem(ctx context.Context, itemID string) ([]AudioTrack, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+audioTrackCols+` FROM audio_tracks WHERE item_id = ? ORDER BY stream_index`, itemID)
	if err != nil {
		return nil, fmt.Errorf("audio tracks for %s: %w", itemID, err)
	}
	defer rows.Close()
	out := []AudioTrack{}
	for rows.Next() {
		var t AudioTrack
		var def int
		if err := rows.Scan(&t.ID, &t.ItemID, &t.StreamIndex, &t.Codec, &t.Language,
			&t.Title, &t.Channels, &t.BitRate, &def); err != nil {
			return nil, fmt.Errorf("scan audio track: %w", err)
		}
		t.Default = def == 1
		t.LanguageName = lang.NameOf(t.Language)
		out = append(out, t)
	}
	return out, rows.Err()
}
