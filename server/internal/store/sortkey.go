package store

import "strings"

// SortKey is the title as it sorts and files under a letter: folded (see
// Fold), lower-cased and with a leading English article dropped, so "The
// Beatles" sits under B beside "Beach Boys" and "Björk" under B with
// "Blur". Stored in items.sort_title on every save; migration 0045 filled
// it for what was there with the same rule in SQL, so the two must agree.
//
// Only ASCII letters are lower-cased, as SQLite's lower() does: Fold has
// already brought every other letter to one case.
func SortKey(title string) string {
	t := strings.TrimSpace(title)
	lower := strings.ToLower(t)
	for _, article := range []string{"the ", "an ", "a "} {
		if strings.HasPrefix(lower, article) {
			t = strings.TrimSpace(t[len(article):])
			break
		}
	}
	return asciiLower(Fold(t))
}

func asciiLower(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}
