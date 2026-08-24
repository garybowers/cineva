package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type SubtitleTrack struct {
	ID          string `json:"id"`
	ItemID      string `json:"itemId"`
	Source      string `json:"source"` // "embedded" | "sidecar"
	StreamIndex int    `json:"streamIndex"`
	Path        string `json:"path,omitempty"` // sidecar file path
	Codec       string `json:"codec"`
	Language    string `json:"language,omitempty"`
	Title       string `json:"title,omitempty"`
	Forced      bool   `json:"forced,omitempty"`
}

// TextCodec reports whether the track can be extracted to WebVTT (as opposed
// to image-based subtitles, which need burn-in).
func (t SubtitleTrack) TextCodec() bool {
	switch t.Codec {
	case "subrip", "srt", "ass", "ssa", "mov_text", "webvtt", "vtt":
		return true
	}
	return false
}

type SubtitleRepo interface {
	ReplaceSubtitles(ctx context.Context, itemID string, tracks []SubtitleTrack) error
	SubtitlesForItem(ctx context.Context, itemID string) ([]SubtitleTrack, error)
	Subtitle(ctx context.Context, id string) (SubtitleTrack, error)
}

var _ SubtitleRepo = (*Store)(nil)

// ReplaceSubtitles swaps an item's subtitle rows for the given set (scans are
// idempotent, so rows are rebuilt rather than diffed).
func (s *Store) ReplaceSubtitles(ctx context.Context, itemID string, tracks []SubtitleTrack) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace subtitles: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM subtitle_tracks WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("clear subtitles: %w", err)
	}
	for _, t := range tracks {
		forced := 0
		if t.Forced {
			forced = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO subtitle_tracks (id, item_id, source, stream_index, path, codec, language, title, forced)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), itemID, t.Source, t.StreamIndex, t.Path, t.Codec, t.Language, t.Title, forced); err != nil {
			return fmt.Errorf("insert subtitle: %w", err)
		}
	}
	return tx.Commit()
}

const subtitleCols = `id, item_id, source, stream_index, path, codec, language, title, forced`

func scanSubtitle(row interface{ Scan(...any) error }) (SubtitleTrack, error) {
	var t SubtitleTrack
	var forced int
	err := row.Scan(&t.ID, &t.ItemID, &t.Source, &t.StreamIndex, &t.Path, &t.Codec, &t.Language, &t.Title, &forced)
	t.Forced = forced == 1
	return t, err
}

func (s *Store) SubtitlesForItem(ctx context.Context, itemID string) ([]SubtitleTrack, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+subtitleCols+` FROM subtitle_tracks WHERE item_id = ? ORDER BY source, stream_index, path`, itemID)
	if err != nil {
		return nil, fmt.Errorf("subtitles for %s: %w", itemID, err)
	}
	defer rows.Close()
	out := []SubtitleTrack{}
	for rows.Next() {
		t, err := scanSubtitle(rows)
		if err != nil {
			return nil, fmt.Errorf("scan subtitle: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Subtitle(ctx context.Context, id string) (SubtitleTrack, error) {
	t, err := scanSubtitle(s.db.QueryRowContext(ctx,
		`SELECT `+subtitleCols+` FROM subtitle_tracks WHERE id = ?`, id))
	if err != nil {
		return SubtitleTrack{}, fmt.Errorf("subtitle %s: %w", id, err)
	}
	return t, nil
}
