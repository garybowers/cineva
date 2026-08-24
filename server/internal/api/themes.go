package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"sort"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/ui"
)

type themeInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Appearance string `json:"appearance"` // "dark" | "light"
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
			_ = json.Unmarshal(raw, &t)
		}
		t.ID = e.Name() // the folder is the identity; manifests can't override it
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return c.JSON(http.StatusOK, out)
}
