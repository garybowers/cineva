package store

import (
	"context"
	"fmt"
)

// TranscodeSession is the stored description of a transcode session: enough
// to re-register it after a restart, with the segments already on disk.
//
// The transcoder's own state is not here and does not need to be. A restored
// session is idle until a segment is asked for, and the restart-at-offset
// path then starts it wherever the viewer actually is.
type TranscodeSession struct {
	ID            string
	ItemID        string
	Title         string
	InputPath     string
	Dir           string
	SegmentSecs   int
	NumSegments   int
	RuntimeSecs   int
	QualityLabel  string
	QualityHeight int
	QualityKbps   int
	AudioTrack    int
	HDR           string
	SegSecsReal   float64
}

func (s *Store) SaveTranscodeSession(ctx context.Context, t TranscodeSession) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO transcode_sessions
			(id, item_id, title, input_path, dir, segment_secs, num_segments,
			 runtime_secs, quality_label, quality_height, quality_bitrate,
			 audio_track, hdr, seg_secs_real)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO NOTHING`,
		t.ID, t.ItemID, t.Title, t.InputPath, t.Dir, t.SegmentSecs, t.NumSegments,
		t.RuntimeSecs, t.QualityLabel, t.QualityHeight, t.QualityKbps,
		t.AudioTrack, t.HDR, t.SegSecsReal)
	if err != nil {
		return fmt.Errorf("save transcode session: %w", err)
	}
	return nil
}

func (s *Store) DeleteTranscodeSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM transcode_sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete transcode session: %w", err)
	}
	return nil
}

// TranscodeSessions lists every stored session, for restoring at startup.
func (s *Store) TranscodeSessions(ctx context.Context) ([]TranscodeSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, item_id, title, input_path, dir, segment_secs, num_segments,
		       runtime_secs, quality_label, quality_height, quality_bitrate,
		       audio_track, hdr, seg_secs_real
		FROM transcode_sessions ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("transcode sessions: %w", err)
	}
	defer rows.Close()
	out := []TranscodeSession{}
	for rows.Next() {
		var t TranscodeSession
		if err := rows.Scan(&t.ID, &t.ItemID, &t.Title, &t.InputPath, &t.Dir,
			&t.SegmentSecs, &t.NumSegments, &t.RuntimeSecs, &t.QualityLabel,
			&t.QualityHeight, &t.QualityKbps, &t.AudioTrack, &t.HDR, &t.SegSecsReal); err != nil {
			return nil, fmt.Errorf("scan transcode session: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
