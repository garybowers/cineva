package media

import "testing"

func TestLanguageCode(t *testing.T) {
	for _, tc := range []struct{ tag, want string }{
		{"eng", "en"},
		{"en", "en"},
		{"'eng'", "en"},
		{" English ", "en"},
		{"fre", "fr"},
		{"fra", "fr"},
		{"chi", "zh"},
		{"und", ""},
		{"", ""},
		{"tlh", "tlh"}, // unknown tags pass through, so two of them still match
	} {
		if got := LanguageCode(tc.tag); got != tc.want {
			t.Errorf("LanguageCode(%q) = %q, want %q", tc.tag, got, tc.want)
		}
	}
	if SameLanguage("", "und") {
		t.Error("two untagged tracks matched")
	}
	if !SameLanguage("ger", "de") {
		t.Error("ger and de did not match")
	}
}
