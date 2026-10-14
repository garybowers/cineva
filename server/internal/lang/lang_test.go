package lang

import (
	"reflect"
	"strings"
	"testing"
)

func TestCode(t *testing.T) {
	for _, tc := range []struct{ tag, want string }{
		{"eng", "en"},
		{"en", "en"},
		{"'eng'", "en"},
		{" English ", "en"},
		{"fre", "fr"},
		{"fra", "fr"},
		{"ger", "de"},
		{"deu", "de"},
		{"chi", "zh"},
		{"zho", "zh"},
		{"cze", "cs"},
		{"ces", "cs"},
		{"dut", "nl"},
		{"nld", "nl"},
		{"gre", "el"},
		{"rum", "ro"},
		{"wel", "cy"},
		{"per", "fa"},
		{"nob", "no"},
		{"nb", "no"},
		{"iw", "he"},
		{"pt-BR", "pt"},
		{"en_GB", "en"},
		{"yue", "yue"},
		{"und", ""},
		{"", ""},
		{"xyz", "xyz"}, // unknown tags pass through, so two of them still match
	} {
		if got := Code(tc.tag); got != tc.want {
			t.Errorf("Code(%q) = %q, want %q", tc.tag, got, tc.want)
		}
	}
}

// No spelling may mean two languages, and every code names itself.
func TestTableIsConsistent(t *testing.T) {
	seen := map[string]string{}
	for _, l := range languages {
		spellings := append([]string{l.code, strings.ToLower(l.name)}, l.other...)
		for _, s := range spellings {
			if prev, ok := seen[s]; ok && prev != l.code {
				t.Errorf("%q means both %s and %s", s, prev, l.code)
			}
			seen[s] = l.code
		}
		if Code(l.code) != l.code {
			t.Errorf("Code(%q) = %q", l.code, Code(l.code))
		}
	}
}

func TestNameOf(t *testing.T) {
	for _, tc := range []struct{ tag, want string }{
		{"fre", "French"},
		{"fr-CA", "French"},
		{"nob", "Norwegian"},
		{"yue", "Cantonese"},
		{"und", ""},
		{"mul", ""},
		{"", ""},
	} {
		if got := NameOf(tc.tag); got != tc.want {
			t.Errorf("NameOf(%q) = %q, want %q", tc.tag, got, tc.want)
		}
	}
}

func TestNamed(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags []string
		want []Language
	}{
		{"nothing", nil, nil},
		{"folded, de-duplicated and sorted by name",
			[]string{"fre", "eng", "fra", "fr", "ger", "", "und", "'eng'"},
			[]Language{{"en", "English"}, {"fr", "French"}, {"de", "German"}}},
		{"unknown and special codes dropped",
			[]string{"mul", "zxx", "xyz", "jpn"},
			[]Language{{"ja", "Japanese"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Named(tc.tags); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Named(%q) = %v, want %v", tc.tags, got, tc.want)
			}
		})
	}
}
