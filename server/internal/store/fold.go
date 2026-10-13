package store

import (
	"database/sql/driver"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"modernc.org/sqlite"
)

// SQLite's LIKE folds case for ASCII letters only, so "AMÉLIE" did not find
// "Amélie", nor "ΣΤΑΘΗΣ" "Σταθης". Text searches compare unifold(column)
// with a pattern folded the same way in Go instead (see likeContains), which
// makes them case-insensitive across Unicode while LIKE keeps doing the
// wildcard matching and its escapes.
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
// case compare equal under LIKE: Unicode simple case folding, after
// composing accents onto their letters (NFC), since a filename copied from a
// Mac spells "é" as "e" plus a combining accent where a typed query has one
// character.
//
// Each letter maps to the smallest member of its case-folding orbit — "É"
// for "é", "Σ" for "σ" and the final "ς" alike, "K" for "k" and the Kelvin
// sign — so the result is only for comparing, never for display. Text that
// is all ASCII is returned as it is, which is most titles and paths: LIKE
// already folds ASCII letters itself, and skipping the work for them keeps a
// search over a large library quick.
func Fold(s string) string {
	if isASCII(s) {
		return s
	}
	return strings.Map(foldRune, norm.NFC.String(s))
}

func foldRune(r rune) rune {
	least := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		least = min(least, f)
	}
	return least
}

// foldedCol is the column expression to compare a query's LIKE pattern
// against: unifold(col) when the query has anything beyond ASCII, and the
// column itself when it does not. An ASCII query can only match the ASCII
// letters in a title, and LIKE folds those already, so the plain column
// gives the same answer at a fraction of the cost — calling back into Go
// for every row made a search of a twenty-thousand-item library several
// times slower. (The exceptions are the two non-ASCII letters that fold to
// ASCII ones, the Kelvin sign and the long s, which no title uses.)
func foldedCol(col, q string) string {
	if isASCII(q) {
		return col
	}
	return "unifold(" + col + ")"
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
// comparing against unifold(column) with ESCAPE '\': folded the same way as
// the column, and with the query's own % and _ escaped so "the_thing"
// matches literally rather than as a pattern.
func likeContains(q string) string { return "%" + likeEscaper.Replace(Fold(q)) + "%" }

func likePrefix(q string) string { return likeEscaper.Replace(Fold(q)) + "%" }
