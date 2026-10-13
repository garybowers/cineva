package logging

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Card prints the startup card: a title line, then detail lines, in a
// rounded frame sized to the longest.
//
//	╭──────────────────────────────────────────────╮
//	│  cineva v0.2.0                               │
//	│  http://localhost:4747 · VAAPI · 2 libraries │
//	╰──────────────────────────────────────────────╯
func Card(w io.Writer, colour bool, title string, lines ...string) {
	width := utf8.RuneCountInString(title)
	for _, l := range lines {
		width = max(width, utf8.RuneCountInString(l))
	}
	paint := func(style, s string) string {
		if !colour {
			return s
		}
		return style + s + reset
	}
	row := func(text, style string) string {
		pad := strings.Repeat(" ", width-utf8.RuneCountInString(text))
		return paint(dim, "│") + "  " + paint(style, text) + pad + "  " + paint(dim, "│")
	}
	rule := strings.Repeat("─", width+4)
	fmt.Fprintln(w, paint(dim, "╭"+rule+"╮"))
	fmt.Fprintln(w, row(title, bold+cyan))
	for _, l := range lines {
		fmt.Fprintln(w, row(l, ""))
	}
	fmt.Fprintln(w, paint(dim, "╰"+rule+"╯"))
}
