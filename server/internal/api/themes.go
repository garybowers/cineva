package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"regexp"
	"sort"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/ui"
)

type themeInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Appearance string `json:"appearance"` // "dark" | "light"
	// Tokens is the theme's design tokens from its theme.css, by name
	// without the leading dashes ("bg", "on-accent"), so a client can
	// draw a preview of a theme without switching to it.
	Tokens map[string]string `json:"tokens"`
}

// listThemes enumerates theme folders (one directory per theme, containing
// theme.css and an optional theme.json manifest).
func listThemes(c echo.Context) error {
	themes := ui.Themes()
	entries, err := fs.ReadDir(themes, ".")
	if err != nil {
		return err
	}
	out := make([]themeInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t := themeInfo{ID: e.Name(), Name: e.Name(), Appearance: "dark"}
		if raw, err := fs.ReadFile(themes, e.Name()+"/theme.json"); err == nil {
			if err := json.Unmarshal(raw, &t); err != nil {
				serverLog.Warn("theme manifest unreadable", "theme", e.Name(), "err", err)
			}
		}
		t.ID = e.Name() // the folder is the identity; manifests can't override it
		t.Tokens = map[string]string{}
		if css, err := fs.ReadFile(themes, e.Name()+"/theme.css"); err == nil {
			t.Tokens = themeTokens(string(css))
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return c.JSON(http.StatusOK, out)
}

var (
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssToken   = regexp.MustCompile(`--([a-z0-9-]+)\s*:\s*([^;{}]+?)\s*(?:;|})`)
	// A token's value as a client may set it in a style attribute: a
	// colour in any of CSS's notations, and nothing that could load or
	// break out of anything (no url(), quotes, braces or semicolons).
	safeTokenValue = regexp.MustCompile(`^[#a-zA-Z0-9(),.%/+\s-]+$`)
	cssURL         = regexp.MustCompile(`(?i)url\(`)
)

// themeTokens reads the custom properties a theme.css defines. A theme is a
// flat list of tokens on :root; the last definition of a name wins, as in
// the browser.
func themeTokens(css string) map[string]string {
	out := map[string]string{}
	for _, m := range cssToken.FindAllStringSubmatch(cssComment.ReplaceAllString(css, ""), -1) {
		if safeTokenValue.MatchString(m[2]) && !cssURL.MatchString(m[2]) {
			out[m[1]] = m[2]
		}
	}
	return out
}
