package media

import "github.com/garybowers/cineva/server/internal/lang"

// LanguageCode is the ISO 639-1 code for a stream's language tag, however
// the file spells it (see lang.Code), or the tag itself lower-cased when it
// is one the table does not know. "" for no language at all, including
// "und" (undetermined).
func LanguageCode(tag string) string { return lang.Code(tag) }

// SameLanguage reports whether two tags name the same language. Two empty
// tags do not match: an untagged track is not in anyone's language.
func SameLanguage(a, b string) bool {
	ca := LanguageCode(a)
	return ca != "" && ca == LanguageCode(b)
}
