package media

import "strings"

// Files tag their streams' languages inconsistently: ISO 639-2 in its
// bibliographic form ("fre", "ger", "chi") or its terminological one ("fra",
// "deu", "zho"), ISO 639-1 ("fr"), the occasional English name, and now and
// then with stray quotes from a muxing script. LanguageCode folds all of
// those to one ISO 639-1 code so a preference matches whatever spelling a
// file happens to use.
var languageCodes = map[string]string{}

func init() {
	for code, spellings := range map[string][]string{
		"en": {"eng", "english"},
		"fr": {"fre", "fra", "french"},
		"de": {"ger", "deu", "german"},
		"es": {"spa", "spanish"},
		"it": {"ita", "italian"},
		"pt": {"por", "portuguese"},
		"nl": {"dut", "nld", "dutch"},
		"sv": {"swe", "swedish"},
		"da": {"dan", "danish"},
		"no": {"nor", "nob", "nno", "norwegian"},
		"fi": {"fin", "finnish"},
		"is": {"ice", "isl", "icelandic"},
		"pl": {"pol", "polish"},
		"cs": {"cze", "ces", "czech"},
		"sk": {"slo", "slk", "slovak"},
		"hu": {"hun", "hungarian"},
		"ro": {"rum", "ron", "romanian"},
		"el": {"gre", "ell", "greek"},
		"tr": {"tur", "turkish"},
		"ru": {"rus", "russian"},
		"uk": {"ukr", "ukrainian"},
		"he": {"heb", "hebrew"},
		"ar": {"ara", "arabic"},
		"hi": {"hin", "hindi"},
		"zh": {"chi", "zho", "chinese"},
		"ja": {"jpn", "japanese"},
		"ko": {"kor", "korean"},
		"th": {"tha", "thai"},
		"vi": {"vie", "vietnamese"},
		"id": {"ind", "indonesian"},
		"ms": {"may", "msa", "malay"},
		"cy": {"wel", "cym", "welsh"},
		"ga": {"gle", "irish"},
	} {
		languageCodes[code] = code
		for _, s := range spellings {
			languageCodes[s] = code
		}
	}
}

// LanguageCode is the ISO 639-1 code for a stream's language tag, or the tag
// itself lower-cased when it is one this table does not know. "" for no
// language at all, including "und" (undetermined).
func LanguageCode(tag string) string {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(tag), `"'`))
	if t == "" || t == "und" {
		return ""
	}
	if code, ok := languageCodes[t]; ok {
		return code
	}
	return t
}

// SameLanguage reports whether two tags name the same language. Two empty
// tags do not match: an untagged track is not in anyone's language.
func SameLanguage(a, b string) bool {
	ca := LanguageCode(a)
	return ca != "" && ca == LanguageCode(b)
}
