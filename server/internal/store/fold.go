package store

import (
	"database/sql/driver"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"modernc.org/sqlite"
)

// SQLite's LIKE folds case for ASCII letters only, so "AMÉLIE" did not find
// "Amélie", nor "ΣΤΑΘΗΣ" "Σταθης", and nothing it does ignores accents, so
// "Amelie" did not find "Amélie" either. Text searches compare a folded
// column (see foldedCol) with a pattern folded the same way in Go (see
// likeContains), which makes them insensitive to case and accents across
// Unicode while LIKE keeps doing the wildcard matching and its escapes.
//
// Registered with the driver, so every connection the store opens has it.
// Deterministic: the same text always folds the same way.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("unifold", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			switch v := args[0].(type) {
			case string:
				return Fold(v), nil
			case []byte:
				return Fold(string(v)), nil
			default:
				return v, nil // NULL, and numbers, which have no case
			}
		})
}

// Fold reduces text to a form in which any two spellings differing only by
// case or accents compare equal under LIKE, so "Amelie", "AMÉLIE" and
// "amélie" all match, as do "Bjork" and "Björk":
//
//   - compatibility decomposition (NFKD), which splits "é" into "e" and a
//     combining accent whether it was typed as one character or arrived
//     already split from a Mac filename, and turns ligatures such as "ﬁ"
//     and full-width letters into their plain letters;
//   - the combining diacritical marks that accent Latin, Greek and Cyrillic
//     letters are dropped (see isAccent). Marks that are part of the
//     spelling in other scripts, such as the voicing marks on Japanese
//     kana, are kept, so "が" does not match "か";
//   - Unicode simple case folding: each letter maps to the smallest member
//     of its case-folding orbit — "E" for "e", "Σ" for "σ" and the final
//     "ς" alike, "K" for "k" and the Kelvin sign;
//   - the few common Latin letters that are not a letter plus a mark, and
//     so survive decomposition, are spelled out in plain letters (see
//     spelledOut): "ø" as "o", "æ" as "ae", "ß" as "ss", and so on.
//
// The result is only for comparing, never for display. Text that is all
// ASCII is returned as it is, which is most titles and paths: it has no
// accents, and LIKE already folds ASCII letters itself.
func Fold(s string) string {
	if isASCII(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFKD.String(s) {
		if isAccent(r) {
			continue
		}
		r = foldRune(r)
		if plain, ok := spelledOut[r]; ok {
			b.WriteString(plain)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func foldRune(r rune) rune {
	least := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		least = min(least, f)
	}
	return least
}

// isAccent reports whether r is a combining diacritical mark of the kind
// that accents Latin, Greek and Cyrillic letters: acute, grave, circumflex,
// diaeresis, ring, cedilla, ogonek, caron, the Vietnamese horn and the like.
// Only the blocks for those, rather than every nonspacing mark
// (unicode.Mn), since elsewhere such a mark can make a different letter.
func isAccent(r rune) bool {
	return (r >= 0x0300 && r <= 0x036F) || // Combining Diacritical Marks
		(r >= 0x1AB0 && r <= 0x1AFF) || // ... Extended
		(r >= 0x1DC0 && r <= 0x1DFF) || // ... Supplement
		(r >= 0x20D0 && r <= 0x20FF) || // ... for Symbols
		(r >= 0xFE20 && r <= 0xFE2F) // Combining Half Marks
}

// spelledOut holds the common Latin letters that decomposition leaves
// alone because they are letters in their own right rather than a letter
// with a mark, written as someone typing on a plain keyboard would. Keyed
// by the case-folded form (foldRune), so each covers both cases: Ø and ø,
// Æ and æ, ẞ and ß. The dotless ı is here too, since its capital I folds
// with the ordinary i.
var spelledOut = map[rune]string{
	'Ø': "O",  // ø, Danish and Norwegian
	'Æ': "AE", // æ
	'Œ': "OE", // œ, French
	'ß': "SS", // ß and ẞ, German
	'Ł': "L",  // ł, Polish
	'Đ': "D",  // đ, Croatian, Serbian and Vietnamese
	'ı': "I",  // ı, Turkish
}

// foldedCol is the column expression to compare a folded LIKE pattern
// against: unifold(col) for a value with anything beyond ASCII, and the
// column itself for one without. An ASCII value has no accents and LIKE
// folds its letters already, so only the rows that need it call back into
// Go, which is the expensive part. Which rows those are is decided by
// comparing the value's length in bytes with its length in characters,
// which SQLite works out itself without leaving the query: over fifty
// thousand titles the test adds about half to the time a plain LIKE takes,
// where calling unifold on every row doubles it. A folded copy of each column
// kept in the table would be quicker still, at the price of a migration and
// of keeping it in step on every write; not needed yet.
//
// The column is folded whatever the query is: an ASCII "Amelie" must find
// an "Amélie", which only the folded column can match.
func foldedCol(col string) string {
	return "CASE WHEN octet_length(" + col + ") = length(" + col + ") THEN " + col +
		" ELSE unifold(" + col + ") END"
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// likeContains and likePrefix turn a user's query into LIKE patterns for
// comparing against foldedCol(column) with ESCAPE '\': folded the same way
// as the column, and with the query's own % and _ escaped so "the_thing"
// matches literally rather than as a pattern.
func likeContains(q string) string { return "%" + likeEscaper.Replace(Fold(q)) + "%" }

func likePrefix(q string) string { return likeEscaper.Replace(Fold(q)) + "%" }
