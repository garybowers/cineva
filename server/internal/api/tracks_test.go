package api

import (
	"testing"

	"github.com/garybowers/cineva/server/internal/store"
)

func TestChooseAudio(t *testing.T) {
	tracks := []store.AudioTrack{
		{StreamIndex: 1, Language: "eng", Default: true},
		{StreamIndex: 2, Language: "jpn"},
		{StreamIndex: 3, Language: "jpn", Default: true},
		{StreamIndex: 4, Language: "fre"},
	}
	for _, tc := range []struct {
		name string
		lang string
		want int
	}{
		{"no preference keeps the file's default", "", 1},
		{"a preferred language takes its marked default", "ja", 3},
		{"else its first track", "fr", 4},
		{"a language the file lacks falls back", "de", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := chooseAudio(tracks, tc.lang); got != tc.want {
				t.Fatalf("chooseAudio = %d, want %d", got, tc.want)
			}
		})
	}
	if got := chooseAudio(nil, "en"); got != -1 {
		t.Fatalf("no audio = %d, want -1", got)
	}
}

func TestChooseSubtitle(t *testing.T) {
	subs := []store.SubtitleTrack{
		{ID: "pgs-en", Codec: "hdmv_pgs_subtitle", Language: "eng"},
		{ID: "srt-en", Codec: "subrip", Language: "eng"},
		{ID: "forced-en", Codec: "hdmv_pgs_subtitle", Language: "eng", Forced: true},
		{ID: "srt-fr", Codec: "subrip", Language: "fre"},
	}
	imageOnly := []store.SubtitleTrack{{ID: "pgs-en", Codec: "hdmv_pgs_subtitle", Language: "eng"}}
	for _, tc := range []struct {
		name      string
		subs      []store.SubtitleTrack
		audioLang string
		prefs     map[string]string
		want      string // "" for none
	}{
		{"off by default", subs, "jpn", map[string]string{}, ""},
		{"off", subs, "jpn", map[string]string{prefSubMode: subModeOff, prefSubLang: "en"}, ""},
		{"always prefers text over image", subs, "eng", map[string]string{prefSubMode: subModeAlways, prefSubLang: "en"}, "srt-en"},
		{"always in another language", subs, "eng", map[string]string{prefSubMode: subModeAlways, prefSubLang: "fr"}, "srt-fr"},
		{"an image track when that is all there is", imageOnly, "eng", map[string]string{prefSubMode: subModeAlways, prefSubLang: "en"}, "pgs-en"},
		{"forced only", subs, "eng", map[string]string{prefSubMode: subModeForced, prefSubLang: "en"}, "forced-en"},
		{"foreign audio gets a full track", subs, "jpn", map[string]string{prefSubMode: subModeForeign, prefSubLang: "en"}, "srt-en"},
		{"own-language audio gets only the forced one", subs, "eng", map[string]string{prefSubMode: subModeForeign, prefSubLang: "en"}, "forced-en"},
		{"the audio preference stands in for a subtitle language", subs, "jpn", map[string]string{prefSubMode: subModeForeign, prefAudioLang: "en"}, "srt-en"},
		{"no language to compare with", subs, "jpn", map[string]string{prefSubMode: subModeForeign}, "forced-en"},
		{"nothing in the language", subs, "jpn", map[string]string{prefSubMode: subModeAlways, prefSubLang: "de"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := chooseSubtitle(tc.subs, tc.audioLang, tc.prefs)
			if !ok {
				got.ID = ""
			}
			if got.ID != tc.want {
				t.Fatalf("chooseSubtitle = %q, want %q", got.ID, tc.want)
			}
		})
	}
}
