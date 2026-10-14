package store

import (
	"context"
	"reflect"
	"testing"
)

// The languages the libraries hold, as the files spell them: each spelling
// once, untagged tracks left out, and gone once no track carries them.
func TestTrackLanguages(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "Films", MediaMovies, []string{"/f"})
	if err != nil {
		t.Fatal(err)
	}

	empty, err := s.TrackLanguages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Audio) != 0 || len(empty.Subtitles) != 0 {
		t.Fatalf("no items, languages %+v", empty)
	}

	film := func(path string, audio, subs []string) Item {
		t.Helper()
		it, err := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindMovie, Title: path, Path: path})
		if err != nil {
			t.Fatal(err)
		}
		var tracks []AudioTrack
		for i, l := range audio {
			tracks = append(tracks, AudioTrack{StreamIndex: i + 1, Codec: "aac", Language: l})
		}
		if err := s.ReplaceAudioTracks(ctx, it.ID, tracks); err != nil {
			t.Fatal(err)
		}
		var st []SubtitleTrack
		for i, l := range subs {
			st = append(st, SubtitleTrack{Source: "embedded", StreamIndex: i + 10, Codec: "subrip", Language: l})
		}
		if err := s.ReplaceSubtitles(ctx, it.ID, st); err != nil {
			t.Fatal(err)
		}
		return it
	}
	film("/f/a.mkv", []string{"eng", "fre", ""}, []string{"eng", "ger"})
	b := film("/f/b.mkv", []string{"eng", "jpn"}, []string{"spa", "eng", ""})

	for _, step := range []struct {
		name       string
		audio, sub []string
	}{
		{"both films", []string{"eng", "fre", "jpn"}, []string{"eng", "ger", "spa"}},
		{"after one is gone", []string{"eng", "fre"}, []string{"eng", "ger"}},
	} {
		got, err := s.TrackLanguages(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Audio, step.audio) || !reflect.DeepEqual(got.Subtitles, step.sub) {
			t.Errorf("%s: audio %q subtitles %q, want %q and %q", step.name, got.Audio, got.Subtitles, step.audio, step.sub)
		}
		if err := s.ReplaceAudioTracks(ctx, b.ID, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.ReplaceSubtitles(ctx, b.ID, nil); err != nil {
			t.Fatal(err)
		}
	}
}
