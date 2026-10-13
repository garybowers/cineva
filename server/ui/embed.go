// Package ui embeds the built web client, admin UI and API reference into the
// server binary.
// The Svelte apps in client/ and admin/ build (npm run build) into their dist/
// directories, which are embedded here — the binary is fully self-contained.
//
// The built files are not committed: each dist/ holds only a .gitkeep, which
// every build puts back, so go:embed always has something to match. A binary
// built without `make ui` serves a page saying so instead of a blank one.
package ui

import (
	"embed"
	"io/fs"
	"testing/fstest"
)

//go:embed all:client/dist
var clientFS embed.FS

//go:embed all:admin/dist
var adminFS embed.FS

// The API reference page: Swagger UI over docs/openapi.yaml, at /api/docs.
//
//go:embed all:apidocs/dist
var apiDocsFS embed.FS

// themes/<name>/theme.css + theme.json — one folder per theme, shared by both
// UIs (they are built against the same design-token contract).
//
//go:embed all:themes
var themesFS embed.FS

func Client() fs.FS  { return built(clientFS, "client/dist") }
func Admin() fs.FS   { return built(adminFS, "admin/dist") }
func APIDocs() fs.FS { return built(apiDocsFS, "apidocs/dist") }
func Themes() fs.FS  { return mustSub(themesFS, "themes") }

// Missing names the UIs this binary was built without.
func Missing() []string {
	var out []string
	for _, u := range []struct {
		name string
		fs   embed.FS
		dir  string
	}{{"web client", clientFS, "client/dist"}, {"admin UI", adminFS, "admin/dist"}, {"API reference", apiDocsFS, "apidocs/dist"}} {
		if _, err := fs.Stat(u.fs, u.dir+"/index.html"); err != nil {
			out = append(out, u.name)
		}
	}
	return out
}

const notBuilt = `<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Cineva</title>
<p>This Cineva binary was built without its web interface. Run <code>make ui</code>
(after <code>make setup</code>) and build it again, or use a release binary.</p>
`

// built is the UI in dir, or the not-built page in its place.
func built(f fs.FS, dir string) fs.FS {
	sub := mustSub(f, dir)
	if _, err := fs.Stat(sub, "index.html"); err == nil {
		return sub
	}
	return fstest.MapFS{"index.html": {Data: []byte(notBuilt)}}
}

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
