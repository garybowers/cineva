package store

import (
	"context"
	"fmt"
)

// TrackLanguages is every language tag the libraries' tracks carry, as the
// files spell them ("eng", "fre", "fr"), each spelling once. Folding them to
// one code per language is the caller's (internal/lang).
type TrackLanguages struct {
	Audio     []string
	Subtitles []string
}

// TrackLanguageRepo reads which languages the libraries hold, for the
// language preferences' choices.
type TrackLanguageRepo interface {
	TrackLanguages(ctx context.Context) (TrackLanguages, error)
}

var _ TrackLanguageRepo = (*Store)(nil)

// TrackLanguages lists the distinct audio and subtitle language tags across
// every library. Untagged tracks are left out.
func (s *Store) TrackLanguages(ctx context.Context) (TrackLanguages, error) {
	var out TrackLanguages
	for _, q := range []struct {
		table string
		dst   *[]string
	}{
		{"audio_tracks", &out.Audio},
		{"subtitle_tracks", &out.Subtitles},
	} {
		rows, err := s.db.QueryContext(ctx,
			`SELECT DISTINCT language FROM `+q.table+` WHERE language <> '' ORDER BY language`)
		if err != nil {
			return TrackLanguages{}, fmt.Errorf("%s languages: %w", q.table, err)
		}
		list := []string{}
		for rows.Next() {
			var l string
			if err := rows.Scan(&l); err != nil {
				rows.Close()
				return TrackLanguages{}, fmt.Errorf("scan %s language: %w", q.table, err)
			}
			list = append(list, l)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return TrackLanguages{}, fmt.Errorf("%s languages: %w", q.table, err)
		}
		*q.dst = list
	}
	return out, nil
}
