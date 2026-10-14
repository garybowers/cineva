package api

import (
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/prefs"
	"github.com/garybowers/cineva/server/internal/store"
)

// Track preferences, resolved with the viewer's other preferences
// (internal/prefs, so an admin's default applies) and applied when playback
// starts, so every client gets the same tracks without choosing them itself.
const (
	prefAudioLang = prefs.AudioLang // ISO 639-1; "" = the file's default
	prefSubMode   = prefs.SubMode   // see the subMode* values
	prefSubLang   = prefs.SubLang   // ISO 639-1; "" = the audio preference, else any
)

// Subtitle modes, after Plex's: when a subtitle is switched on by itself.
const (
	subModeOff     = "off"     // never (the default)
	subModeForced  = "forced"  // only a forced track: signs and foreign dialogue
	subModeForeign = "foreign" // a full track when the audio is in another language
	subModeAlways  = "always"  // a full track whenever there is one
)

// chooseAudio is the stream to play: the first track in the preferred
// language (the file's default among them if it marks one), else the file's
// default. -1 when there is no audio at all.
func chooseAudio(tracks []store.AudioTrack, lang string) int {
	if lang != "" {
		match := -1
		for _, t := range tracks {
			if !media.SameLanguage(t.Language, lang) {
				continue
			}
			if t.Default {
				return t.StreamIndex
			}
			if match < 0 {
				match = t.StreamIndex
			}
		}
		if match >= 0 {
			return match
		}
	}
	return defaultAudioTrack(tracks)
}

// chooseSubtitle is the subtitle to switch on for these preferences, given
// the audio track being played; ok is false for none. A text track wins over
// an image one in the same role: text is overlaid by the player, while an
// image track has to be burned in, which forces a transcode.
func chooseSubtitle(subs []store.SubtitleTrack, audioLang string, prefs map[string]string) (store.SubtitleTrack, bool) {
	mode := prefs[prefSubMode]
	if mode == "" || mode == subModeOff {
		return store.SubtitleTrack{}, false
	}
	lang := prefs[prefSubLang]
	if lang == "" {
		lang = prefs[prefAudioLang]
	}
	full := mode == subModeAlways || (mode == subModeForeign && !media.SameLanguage(audioLang, lang))
	if lang == "" && mode == subModeForeign {
		full = false // no language of the viewer's to compare the audio with
	}

	pick := func(forced bool) (store.SubtitleTrack, bool) {
		var image *store.SubtitleTrack
		for i, s := range subs {
			if s.Forced != forced || (lang != "" && !media.SameLanguage(s.Language, lang)) {
				continue
			}
			if s.TextCodec() {
				return s, true
			}
			if image == nil {
				image = &subs[i]
			}
		}
		if image != nil {
			return *image, true
		}
		return store.SubtitleTrack{}, false
	}
	if full {
		if s, ok := pick(false); ok {
			return s, true
		}
	}
	// A forced track carries only what the audio does not: signs, and lines
	// in another language. Worth showing in every mode but off.
	return pick(true)
}
