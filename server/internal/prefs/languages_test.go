package prefs

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/lang"
	"github.com/garybowers/cineva/server/internal/store"
)

func TestLanguageOptions(t *testing.T) {
	first := []Option{{Value: "", Label: "The file's default"}}
	held := []lang.Language{{Code: "en", Name: "English"}, {Code: "fr", Name: "French"}}
	for _, tc := range []struct {
		name string
		keep []string
		want []string
	}{
		{"what is held", nil, []string{"", "en", "fr"}},
		{"a kept value no library holds, in name order", []string{"de"}, []string{"", "en", "fr", "de"}},
		{"a kept value already held, or empty, adds nothing", []string{"fr", "", "fr"}, []string{"", "en", "fr"}},
		{"a kept value the table does not know shows as itself", []string{"zz"}, []string{"", "en", "fr", "zz"}},
		{"sorted by name, not code", []string{"cy", "ar"}, []string{"", "ar", "en", "fr", "cy"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, o := range languageOptions(first, held, tc.keep) {
				got = append(got, o.Value)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("values %q, want %q", got, tc.want)
			}
		})
	}
}

// The language preferences offer what the libraries hold, folded and named,
// plus what the viewer has; the rest of the registry is untouched, and the
// list is read again once it is a minute old.
func TestServiceRegistryLanguages(t *testing.T) {
	ctx := context.Background()
	svc, st := newService(t)
	now := time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	lib, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{"/f"})
	if err != nil {
		t.Fatal(err)
	}
	film, err := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindMovie, Title: "Amélie", Path: "/f/a.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceAudioTracks(ctx, film.ID, []store.AudioTrack{
		{StreamIndex: 1, Language: "fre"}, {StreamIndex: 2, Language: "eng"}, {StreamIndex: 3, Language: "und"}, {StreamIndex: 4, Language: "fra"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceSubtitles(ctx, film.ID, []store.SubtitleTrack{
		{Source: "embedded", StreamIndex: 5, Codec: "subrip", Language: "ger"}, {Source: "embedded", StreamIndex: 6, Codec: "subrip", Language: "en"},
	}); err != nil {
		t.Fatal(err)
	}

	options := func(keep map[string][]string) map[string][]Option {
		t.Helper()
		reg, err := svc.Registry(ctx, keep)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][]Option{}
		for _, p := range reg {
			out[p.Key] = p.Options
		}
		return out
	}

	got := options(map[string][]string{AudioLang: {"ja"}})
	wantAudio := []Option{{Value: "", Label: "The file's default"}, {Value: "en", Label: "English"}, {Value: "fr", Label: "French"}, {Value: "ja", Label: "Japanese"}}
	if !reflect.DeepEqual(got[AudioLang], wantAudio) {
		t.Errorf("audio options %+v, want %+v", got[AudioLang], wantAudio)
	}
	wantSubs := []Option{{Value: "", Label: "Same as the audio language"}, {Value: "en", Label: "English"}, {Value: "de", Label: "German"}}
	if !reflect.DeepEqual(got[SubLang], wantSubs) {
		t.Errorf("subtitle options %+v, want %+v", got[SubLang], wantSubs)
	}
	if !reflect.DeepEqual(got["quality"], Lookup("quality").Options) {
		t.Errorf("quality options changed: %+v", got["quality"])
	}
	if len(Lookup(AudioLang).Options) != 1 {
		t.Errorf("serving the registry changed the shared entry: %+v", Lookup(AudioLang).Options)
	}

	// A new language within the minute waits; after it, it is offered.
	if err := st.ReplaceAudioTracks(ctx, film.ID, []store.AudioTrack{{StreamIndex: 1, Language: "spa"}}); err != nil {
		t.Fatal(err)
	}
	if n := len(options(nil)[AudioLang]); n != 3 {
		t.Errorf("within the minute, %d audio options, want the cached 3", n)
	}
	now = now.Add(languagesTTL)
	if got := options(nil)[AudioLang]; len(got) != 2 || got[1].Value != "es" {
		t.Errorf("after the minute, audio options %+v, want the file's default and Spanish", got)
	}
}

func TestKeep(t *testing.T) {
	r := map[string]Resolved{
		AudioLang:  {Origin: Origin{Value: "fr"}, Default: &Origin{Value: "en"}},
		SubLang:    {Origin: Origin{Value: ""}},
		"autoplay": {Origin: Origin{Value: "on"}},
	}
	if got, want := ResolvedKeep(r), map[string][]string{AudioLang: {"fr", "en"}, SubLang: {""}}; !reflect.DeepEqual(got, want) {
		t.Errorf("ResolvedKeep = %v, want %v", got, want)
	}
	d := Defaults{
		Server: map[string]store.PrefSetting{AudioLang: {Value: "en"}},
		Groups: []GroupDefaults{{Defaults: map[string]store.PrefSetting{AudioLang: {Value: "de"}, SubLang: {Value: "nl"}}}},
	}
	if got, want := DefaultsKeep(d), map[string][]string{AudioLang: {"en", "de"}, SubLang: {"nl"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("DefaultsKeep = %v, want %v", got, want)
	}
}
