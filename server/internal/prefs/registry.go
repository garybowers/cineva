// Package prefs is the registry of every preference the server knows about,
// and the resolver that decides which layer's value applies: the device's,
// the user's, their groups' defaults, the server's, or the built-in one.
// See docs/personalisation.md, "Preferences".
//
// Values are strings at every layer, as they always were in user_prefs. The
// registry says what a key's string means, so the server can validate a
// value, hold an admin default for it, and describe it to a client that
// draws its Settings page from the registry instead of hardcoding it.
package prefs

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/garybowers/cineva/server/internal/lang"
)

// Type is how a preference's string is read.
type Type string

const (
	TypeBool   Type = "bool"   // "on" or "off"
	TypeEnum   Type = "enum"   // one of Options
	TypeInt    Type = "int"    // a whole number within Min and Max
	TypeString Type = "string" // free text, up to MaxValueLen
	TypeList   Type = "list"   // a JSON array of strings
)

// Bool values. "on" and "off" rather than true and false because autoplay,
// the first boolean preference, was stored that way before there was a
// registry, and existing values must keep their meaning.
const (
	On  = "on"
	Off = "off"
)

// Scope says whom a preference belongs to: the person, everywhere they sign
// in, or the device in front of them.
type Scope string

const (
	ScopePerson Scope = "person"
	ScopeDevice Scope = "device"
)

// Limits on what can be stored, so preferences cannot be used as free
// storage by a buggy or hostile client. The same limits applied to
// /auth/prefs before there was a registry.
const (
	MaxKeys     = 50
	MaxKeyLen   = 64
	MaxValueLen = 512
)

// Option is one choice of an enum.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
	// Description says what choosing it does, where the label alone does
	// not; shown under the control while it is selected.
	Description string `json:"description,omitempty"`
}

// Condition hides a preference while another one has a value: subtitle
// language means nothing while subtitles are off.
type Condition struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Pref describes one preference.
type Pref struct {
	Key     string `json:"key"`
	Type    Type   `json:"type"`
	Default string `json:"default"`
	Scope   Scope  `json:"scope"`
	// Lockable says whether an admin may lock it. False where a lock would
	// only be petty, such as the theme.
	Lockable bool `json:"lockable"`
	// Section is the Settings section it appears in (see Sections), so
	// every client lays out the same page.
	Section string `json:"section"`
	// Hidden is a preference the client keeps for itself rather than a
	// row in Settings or on the admin's Defaults page.
	Hidden      bool       `json:"hidden,omitempty"`
	Label       string     `json:"label"`
	Description string     `json:"description"`
	Options     []Option   `json:"options,omitempty"`
	Min         *int       `json:"min,omitempty"`
	Max         *int       `json:"max,omitempty"`
	HideWhen    *Condition `json:"hideWhen,omitempty"`

	// langs marks a language preference, whose Options beyond the first
	// are the languages the libraries hold.
	langs langSource
}

// Section is a Settings section, in the order a client shows them.
type Section struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Sections that preferences appear in. Settings has other sections too
// (profile, devices, API keys) that hold no preferences.
var Sections = []Section{
	{ID: "playback", Label: "Playback"},
	{ID: "music", Label: "Music"},
	{ID: "appearance", Label: "Appearance"},
}

// Track preferences, read by the server when playback starts.
const (
	AudioLang = "audio-lang" // a lang.Code, ISO 639-1 where there is one; "" = the file's default
	// MusicQuality is the music stream the device asks for: "original",
	// "opus-128" or "opus-64"; resolved by the server when a track plays.
	MusicQuality = "music-quality"
	// MusicBar is where the web client shows the music bar: "top" or
	// "bottom". A person's choice, not a device's.
	MusicBar = "music-bar"
	// MusicAutoplay carries a queue on with a mix when it ends.
	MusicAutoplay = "music-autoplay"
	SubMode       = "sub-mode" // off | forced | foreign | always
	SubLang       = "sub-lang" // a lang.Code; "" = the audio preference, else any
)

// Language preferences offer the languages the libraries hold, not a fixed
// list: their Options are filled in when the registry is served (see
// Service.Registry), from the audio tracks for the audio preference and the
// subtitle tracks for the subtitle one. A stored value need only name a
// language the server knows (internal/lang), so a choice outlives the last
// file in that language.
type langSource int

const (
	notLanguages langSource = iota
	audioLanguages
	subtitleLanguages
)

const convertHint = "Always converts, to save bandwidth or suit a slow connection."

// registry is every preference the server knows, in the order Settings
// shows them within a section. Keys the client used before the registry
// existed keep their names and values.
var registry = []Pref{
	{
		Key: "quality", Type: TypeEnum, Default: "auto", Scope: ScopeDevice, Lockable: true,
		Section: "playback", Label: "Video quality",
		Description: "Changing it in the player changes it here too.",
		Options: []Option{
			{Value: "auto", Label: "Auto (recommended)", Description: "Plays the file as it is when this device can, and converts it only when it must."},
			{Value: "original", Label: "Original", Description: "Always the untouched video. Some files will not play in some browsers."},
			{Value: "1080p", Label: "1080p · 8 Mbps", Description: convertHint},
			{Value: "720p", Label: "720p · 4 Mbps", Description: convertHint},
			{Value: "480p", Label: "480p · 2.5 Mbps", Description: convertHint},
		},
	},
	{
		Key: AudioLang, Type: TypeEnum, Default: "", Scope: ScopePerson, Lockable: true,
		Section: "playback", Label: "Audio language",
		Description: "Played when a file has it; otherwise the file's own choice.",
		Options:     []Option{{Value: "", Label: "The file's default"}},
		langs:       audioLanguages,
	},
	{
		Key: SubMode, Type: TypeEnum, Default: "off", Scope: ScopePerson, Lockable: true,
		Section: "playback", Label: "Subtitles",
		Options: []Option{
			{Value: "off", Label: "Off", Description: "Switched on by hand in the player."},
			{Value: "forced", Label: "Signs and foreign dialogue", Description: "Only the short tracks that translate signs and lines in another language."},
			{Value: "foreign", Label: "When the audio is in another language", Description: "Full subtitles when a film is not in your language; otherwise only signs and foreign dialogue."},
			{Value: "always", Label: "Always", Description: "Full subtitles whenever there are some in your language."},
		},
	},
	{
		Key: SubLang, Type: TypeEnum, Default: "", Scope: ScopePerson, Lockable: true,
		Section: "playback", Label: "Subtitle language",
		Description: "Picture-based subtitles, common on disc copies, are drawn into the video, which means converting it.",
		Options:     []Option{{Value: "", Label: "Same as the audio language"}},
		HideWhen:    &Condition{Key: SubMode, Value: "off"},
		langs:       subtitleLanguages,
	},
	{
		Key: "autoplay", Type: TypeBool, Default: On, Scope: ScopePerson, Lockable: true,
		Section: "playback", Label: "Play the next episode",
		Description: "Starts the next episode when one ends, after a short countdown you can stop.",
	},
	{
		Key: "skip-intro", Type: TypeEnum, Default: "button", Scope: ScopePerson, Lockable: true,
		Section: "playback", Label: "Intros",
		Options: []Option{
			{Value: "auto", Label: "Skip automatically", Description: "Jumps past a detected intro as it starts. Seek back into it to watch it after all."},
			{Value: "button", Label: "Show a Skip intro button", Description: "Offers to skip while a detected intro plays."},
			{Value: "off", Label: "Do nothing", Description: "Intros play like the rest of the episode."},
		},
	},
	{
		Key: "skip-credits", Type: TypeEnum, Default: "card", Scope: ScopePerson, Lockable: true,
		Section: "playback", Label: "Credits",
		Options: []Option{
			{Value: "next", Label: "Go to the next episode", Description: "The next episode starts as soon as detected credits begin, when Play the next episode is on."},
			{Value: "card", Label: "Show what is up next", Description: "A card offers the next episode when the credits begin, and plays it after a short countdown."},
			{Value: "end", Label: "Play to the end", Description: "The credits play out, and the next episode follows when the file ends."},
		},
	},
	{
		Key: "music-quality", Type: TypeEnum, Default: "original", Scope: ScopeDevice, Lockable: true,
		Section: "music", Label: "Music quality",
		Description: "Data saver streams a smaller copy, for phones on mobile data. From the next track.",
		Options: []Option{
			{Value: "original", Label: "Original"},
			{Value: "opus-128", Label: "High (Opus 128k)"},
			{Value: "opus-64", Label: "Data saver (Opus 64k)"},
		},
	},
	{
		Key: "gapless", Type: TypeBool, Default: On, Scope: ScopeDevice, Lockable: false,
		Section: "music", Label: "Gapless playback",
		Description: "Decodes the next track ahead and joins it exactly, so an album plays without a gap. Costs memory; a phone that struggles can turn it off.",
	},
	{
		Key: "music-start", Type: TypeEnum, Default: "bar", Scope: ScopeDevice, Lockable: false,
		Section: "music", Label: "When music starts",
		Options: []Option{
			{Value: "bar", Label: "Show the bar", Description: "The music bar appears and the page stays where it is."},
			{Value: "now-playing", Label: "Open Now playing", Description: "The now-playing screen opens with the cover, the controls and what is up next."},
		},
	},
	{
		Key: MusicAutoplay, Type: TypeBool, Default: On, Scope: ScopePerson, Lockable: false,
		Section: "music", Label: "Keep playing when the queue ends",
		Description: "When the last track ends, a mix from it carries on: more of what belongs beside it, shown in Up next before it plays (server/docs/mix.md).",
	},
	{
		Key: MusicBar, Type: TypeEnum, Default: "top", Scope: ScopePerson, Lockable: false,
		Section: "music", Label: "Player bar",
		Description: "Where the music bar sits while something plays. Follows you to every device.",
		Options: []Option{
			{Value: "top", Label: "Top of the screen", Description: "Under the top bar; the page moves down to make room."},
			{Value: "bottom", Label: "Bottom of the screen", Description: "Above the tab bar on a phone."},
		},
	},
	{
		Key: "theme", Type: TypeString, Default: "midnight", Scope: ScopePerson, Lockable: false,
		Section: "appearance", Label: "Theme",
		Description: "Follows you to every device you sign in on.",
	},
	{
		Key: "holiday-themes", Type: TypeBool, Default: On, Scope: ScopePerson, Lockable: true,
		Section: "appearance", Label: "Holiday themes",
		Description: "Switches to the holiday's own theme for Halloween, Christmas and Easter, then back to yours.",
	},
	{
		// Not a row in Settings: set by choosing a theme while a holiday's
		// is showing, which keeps that choice for the rest of the holiday.
		Key: "holiday-skip", Type: TypeString, Default: "", Scope: ScopePerson, Lockable: false, Hidden: true,
		Section: "appearance", Label: "Holiday theme set aside",
		Description: "The holiday whose theme you chose your own over, this year (\"halloween-2026\").",
	},
	{
		Key: "theme-effects", Type: TypeBool, Default: On, Scope: ScopePerson, Lockable: false,
		Section: "appearance", Label: "Theme effects",
		Description: "The gentle snow, leaves or blossom some themes have. Never while something plays, nor when your system asks for less motion.",
	},
	{
		Key: "interface", Type: TypeEnum, Default: "auto", Scope: ScopeDevice, Lockable: false,
		Section: "appearance", Label: "Interface",
		Description: "How this screen shows Cineva. It stays with the screen whoever signs in on it.",
		Options: []Option{
			{Value: "auto", Label: "Automatic", Description: "TV on a television's own browser (Fire TV, Android TV, LG, Samsung), desktop everywhere else."},
			{Value: "desktop", Label: "Desktop", Description: "For a mouse, keyboard or touch."},
			{Value: "tv", Label: "TV", Description: "Large, and worked with a remote's arrows, OK and Back."},
		},
	},
	{
		Key: "sidebar-size", Type: TypeEnum, Default: "regular", Scope: ScopeDevice, Lockable: false,
		Section: "appearance", Label: "Sidebar size",
		Description: "How big the sidebar's entries and icons are on this screen.",
		Options: []Option{
			{Value: "compact", Label: "Compact", Description: "Small type and icons; the narrowest rail."},
			{Value: "regular", Label: "Regular", Description: "A little larger, easy to read at a desk."},
			{Value: "large", Label: "Large", Description: "For a desktop looked at from further away."},
		},
	},
}

var byKey = func() map[string]*Pref {
	m := make(map[string]*Pref, len(registry))
	for i := range registry {
		m[registry[i].Key] = &registry[i]
	}
	return m
}()

// Registry is every registered preference, in Settings order, with the
// language preferences offering only their first choice (Service.Registry
// adds the languages). The slice is a copy; the entries are shared and must
// not be changed.
func Registry() []Pref { return slices.Clone(registry) }

// Lookup returns a registered preference, or nil for a key the registry
// does not know (which is still stored, as an opaque string).
func Lookup(key string) *Pref { return byKey[key] }

// Validate checks a value against the preference's type.
func (p *Pref) Validate(v string) error {
	if len(v) > MaxValueLen {
		return fmt.Errorf("%s: a value is at most %d characters", p.Key, MaxValueLen)
	}
	switch p.Type {
	case TypeBool:
		if v != On && v != Off {
			return fmt.Errorf("%s: must be %q or %q", p.Key, On, Off)
		}
	case TypeEnum:
		if p.langs != notLanguages && v != "" {
			if _, ok := lang.Name(v); !ok {
				return fmt.Errorf("%s: %q is not a language code the server knows", p.Key, v)
			}
			return nil
		}
		if !slices.ContainsFunc(p.Options, func(o Option) bool { return o.Value == v }) {
			return fmt.Errorf("%s: %q is not one of its choices", p.Key, v)
		}
	case TypeInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%s: must be a whole number", p.Key)
		}
		if (p.Min != nil && n < *p.Min) || (p.Max != nil && n > *p.Max) {
			return fmt.Errorf("%s: %d is out of range", p.Key, n)
		}
	case TypeString:
		if v == "" && p.Default != "" {
			return fmt.Errorf("%s: cannot be empty", p.Key)
		}
	case TypeList:
		var list []string
		if err := json.Unmarshal([]byte(v), &list); err != nil {
			return fmt.Errorf("%s: must be a JSON array of strings", p.Key)
		}
	}
	return nil
}
