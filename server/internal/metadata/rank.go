package metadata

import (
	"strings"
	"unicode"
)

// normaliseTitle reduces a title to a comparable key: lower case, no
// diacritics, no punctuation, no leading article, single spaces. It is what
// lets "WALL·E" match "Wall-E" and "The Thing" match "Thing, The".
func normaliseTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// "Thing, The" -> "the thing", a common library convention.
	if i := strings.LastIndex(s, ", "); i > 0 {
		if tail := s[i+2:]; tail == "the" || tail == "a" || tail == "an" {
			s = tail + " " + s[:i]
		}
	}
	var b strings.Builder
	lastSpace := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if f, ok := fold[r]; ok {
				b.WriteString(f)
			} else {
				b.WriteRune(r)
			}
			lastSpace = false
		default:
			// Any run of punctuation or whitespace collapses to one space,
			// so "spider-man" and "spider man" agree.
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	out := strings.TrimSpace(b.String())
	for _, art := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(out, art) {
			return out[len(art):]
		}
	}
	return out
}

// fold maps the Latin-1 diacritics and ligatures that turn up in film
// titles to their ASCII equivalents. A full Unicode normalisation table
// would be overkill here; this covers what actually appears.
var fold = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u",
	'ý': "y", 'ÿ': "y", 'ñ': "n", 'ç': "c",
	'š': "s", 'ž': "z", 'ß': "ss",
	'œ': "oe", 'æ': "ae",
}

// scoreCandidate rates a search hit against what the filename claimed.
// Higher is better.
//
// This exists because TMDB's `year` parameter only nudges relevance, it does
// not filter: searching a common title returns the most *popular* film of
// that name, which for a library full of scene releases is regularly the
// wrong one. Matching on the normalised title with the year as a tiebreak
// beats trusting result order.
func scoreCandidate(c VideoResult, wantTitle string, wantYear int) int {
	score := 0
	got, want := normaliseTitle(c.Title), normaliseTitle(wantTitle)
	switch {
	case got == want:
		score += 100
	case strings.HasPrefix(got, want) || strings.HasPrefix(want, got):
		score += 50
	case strings.Contains(got, want) || strings.Contains(want, got):
		score += 25
	}
	if wantYear > 0 && c.Year > 0 {
		switch c.Year - wantYear {
		case 0:
			score += 60
		case 1, -1:
			// A film released in December is routinely tagged with the
			// following year by one source and not the other.
			score += 30
		default:
			// A wrong year on an otherwise exact title is the signature of
			// the popular-namesake problem, so it has to cost more than an
			// exact title is worth on its own.
			score -= 40
		}
	}
	return score
}

// bestMatch picks the candidate that best fits the title and year parsed
// from the filename. Results are assumed to be in TMDB's own relevance
// order, which is used as the tiebreak: equal scores keep TMDB's preference.
func bestMatch(results []VideoResult, title string, year int) VideoResult {
	best, bestScore := results[0], scoreCandidate(results[0], title, year)
	for _, c := range results[1:] {
		if s := scoreCandidate(c, title, year); s > bestScore {
			best, bestScore = c, s
		}
	}
	return best
}

// CertificationFor picks the content rating for a region, falling back to
// the US where the title is unclassified in that region — TMDB's coverage
// is best there, and a foreign rating is more use than a blank.
//
// An empty region means "no preference": the US rating, or nothing.
func (v VideoResult) CertificationFor(region string) string {
	if len(v.Certifications) == 0 {
		return ""
	}
	if region != "" {
		if c := v.Certifications[strings.ToUpper(region)]; c != "" {
			return c
		}
	}
	return v.Certifications["US"]
}
