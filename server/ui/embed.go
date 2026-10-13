// Package ui embeds the built web client, admin UI and API reference into the
// server binary.
// The Svelte apps in client/ and admin/ build (npm run build) into their dist/
// directories, which are embedded here — the binary is fully self-contained.
package ui

import (
	"embed"
	"io/fs"
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

func Client() fs.FS  { return mustSub(clientFS, "client/dist") }
func Admin() fs.FS   { return mustSub(adminFS, "admin/dist") }
func APIDocs() fs.FS { return mustSub(apiDocsFS, "apidocs/dist") }
func Themes() fs.FS  { return mustSub(themesFS, "themes") }

func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
