package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"
)

// Folder picker support for the admin UI's add-library form: a browser
// cannot reveal real filesystem paths, so the server lists directories
// (directories only — never file contents) one level at a time.
//
// TODO(milestone 4): admin-only once auth lands.

type dirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type dirListing struct {
	Path   string     `json:"path"`
	Parent string     `json:"parent,omitempty"`
	Dirs   []dirEntry `json:"dirs"`
}

// GET /fs/dirs?path=/media[&hidden=1] — subdirectories of an absolute path.
func (s *server) listDirs(c echo.Context) error {
	p := c.QueryParam("path")
	if p == "" {
		p = "/"
	}
	if !filepath.IsAbs(p) {
		return echo.NewHTTPError(http.StatusBadRequest, "path must be absolute")
	}
	p = filepath.Clean(p)
	entries, err := os.ReadDir(p)
	if err != nil {
		if os.IsNotExist(err) {
			return echo.NewHTTPError(http.StatusNotFound, "directory not found")
		}
		if os.IsPermission(err) {
			return echo.NewHTTPError(http.StatusForbidden, "permission denied")
		}
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	showHidden := c.QueryParam("hidden") == "1"
	out := dirListing{Path: p, Dirs: []dirEntry{}}
	if parent := filepath.Dir(p); parent != p {
		out.Parent = parent
	}
	for _, e := range entries {
		if !showHidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		isDir := e.IsDir()
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			// Follow symlinks so linked media mounts are browsable.
			if st, err := os.Stat(filepath.Join(p, e.Name())); err == nil && st.IsDir() {
				isDir = true
			}
		}
		if isDir {
			out.Dirs = append(out.Dirs, dirEntry{Name: e.Name(), Path: filepath.Join(p, e.Name())})
		}
	}
	sort.Slice(out.Dirs, func(i, j int) bool {
		return strings.ToLower(out.Dirs[i].Name) < strings.ToLower(out.Dirs[j].Name)
	})
	return c.JSON(http.StatusOK, out)
}
