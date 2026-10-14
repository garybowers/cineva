package prefs

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/garybowers/cineva/server/internal/lang"
	"github.com/garybowers/cineva/server/internal/store"
)

// languagesTTL is how long the libraries' languages are reused before they
// are read again: long enough that opening Settings does not scan every
// track, short enough that a language a scan brings appears soon after.
const languagesTTL = time.Minute

// languageCache holds the libraries' languages, folded and named.
type languageCache struct {
	mu        sync.Mutex
	at        time.Time
	audio     []lang.Language
	subtitles []lang.Language
}

func (s *Service) libraryLanguages(ctx context.Context) (audio, subtitles []lang.Language, err error) {
	c := &s.langs
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && s.now().Sub(c.at) < languagesTTL {
		return c.audio, c.subtitles, nil
	}
	held, err := s.store.TrackLanguages(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("library languages: %w", err)
	}
	c.audio, c.subtitles, c.at = lang.Named(held.Audio), lang.Named(held.Subtitles), s.now()
	return c.audio, c.subtitles, nil
}

// Registry is every registered preference, in Settings order, with each
// language preference offering the languages the libraries hold: audio
// tracks' for the audio language, subtitles' for the subtitle one. keep is
// the values each key holds for whoever is looking (resolved for a viewer,
// every default for an admin), which stay among the choices even when no
// library holds them any more, so a saved choice never shows blank.
//
// The list is server-wide rather than filtered by the viewer's library
// access: a language name says nothing about which titles carry it.
func (s *Service) Registry(ctx context.Context, keep map[string][]string) ([]Pref, error) {
	audio, subtitles, err := s.libraryLanguages(ctx)
	if err != nil {
		return nil, err
	}
	out := Registry()
	for i, p := range out {
		var held []lang.Language
		switch p.langs {
		case audioLanguages:
			held = audio
		case subtitleLanguages:
			held = subtitles
		default:
			continue
		}
		out[i].Options = languageOptions(p.Options, held, keep[p.Key])
	}
	return out, nil
}

// languageOptions is first (the registry's own choices), then the languages
// held and those kept, as one list sorted by name.
func languageOptions(first []Option, held []lang.Language, keep []string) []Option {
	opts := slices.Clone(first)
	langs := slices.Clone(held)
	for _, v := range keep {
		if v == "" || slices.ContainsFunc(langs, func(l lang.Language) bool { return l.Code == v }) {
			continue
		}
		name, ok := lang.Name(v)
		if !ok {
			name = v // stored before the table knew it; still shown as itself
		}
		langs = append(langs, lang.Language{Code: v, Name: name})
	}
	slices.SortFunc(langs, func(a, b lang.Language) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Code, b.Code))
	})
	for _, l := range langs {
		if !slices.ContainsFunc(opts, func(o Option) bool { return o.Value == l.Code }) {
			opts = append(opts, Option{Value: l.Code, Label: l.Name})
		}
	}
	return opts
}

// ResolvedKeep is the values to keep among the language choices for a
// viewer: what they resolve to now, and what Reset would go back to.
func ResolvedKeep(r map[string]Resolved) map[string][]string {
	keep := map[string][]string{}
	for _, k := range []string{AudioLang, SubLang} {
		if v, ok := r[k]; ok {
			keep[k] = append(keep[k], v.Value)
			if v.Default != nil {
				keep[k] = append(keep[k], v.Default.Value)
			}
		}
	}
	return keep
}

// DefaultsKeep is the values to keep among the language choices on the
// admin's table: every default set for them, the server's and each group's.
func DefaultsKeep(d Defaults) map[string][]string {
	keep := map[string][]string{}
	add := func(m map[string]store.PrefSetting) {
		for _, k := range []string{AudioLang, SubLang} {
			if v, ok := m[k]; ok {
				keep[k] = append(keep[k], v.Value)
			}
		}
	}
	add(d.Server)
	for _, g := range d.Groups {
		add(g.Defaults)
	}
	return keep
}
