package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

// newTestContext is the minimum an echo.Context needs for the path and
// header checks below.
func newTestContext(path, rangeHeader string) echo.Context {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	return echo.New().NewContext(req, httptest.NewRecorder())
}

// The SPA fallback has to tell a route from a static file. It used to ask
// whether the last path segment contained a dot, which sent every film with
// a dot in its title to a JSON 404 on reload or a shared link.
func TestLooksLikeAsset(t *testing.T) {
	tests := []struct {
		path  string
		asset bool
		why   string
	}{
		{"/assets/index-DMohx9tU.js", true, "hashed bundle"},
		{"/assets/index-BfthYR-2.css", true, "stylesheet"},
		{"/favicon.svg", true, "icon"},
		{"/manifest.webmanifest", true, "pwa manifest"},
		{"/sw.js", true, "service worker"},
		{"/icon-192.png", true, "png"},
		{"/assets/inter-latin-wght-normal-Dx4kXJAl.woff2", true, "font"},
		{"/assets/INDEX-ABC.JS", true, "extension match is case-insensitive"},

		{"/libraries/Movies/Dr. No", false, "a title ending in a short word"},
		{"/libraries/Movies/E.T. the Extra-Terrestrial", false, "dots mid-title"},
		{"/libraries/Movies/Guardians of the Galaxy Vol. 2", false, "numeric suffix"},
		{"/libraries/Movies/Fantastic Mr. Fox", false, "honorific"},
		{"/libraries/Movies/Poltergeist", false, "no dot at all"},
		{"/people/0001f278-ef43-4734-8b41-627e08c9b9b2", false, "uuid route"},
		{"/", false, "root"},
		{"/admin/libraries", false, "admin route"},
	}
	for _, tc := range tests {
		if got := looksLikeAsset(tc.path); got != tc.asset {
			t.Errorf("looksLikeAsset(%q) = %v, want %v (%s)", tc.path, got, tc.asset, tc.why)
		}
	}
}

// Gzip must not touch a range request: the range addresses the original
// bytes, so compressing the body afterwards makes Content-Range a lie and
// breaks seeking and resumable downloads.
func TestIncompressible(t *testing.T) {
	tests := []struct {
		path   string
		rangeH string
		skip   bool
		why    string
	}{
		{"/api/v1/libraries/x/items", "", false, "json is the whole point"},
		{"/assets/index-abc.js", "", false, "bundles compress well"},
		{"/index.html", "", false, "html compresses well"},

		{"/artwork/tmdb-687163.jpg", "", true, "already compressed"},
		{"/assets/inter-latin.woff2", "", true, "woff2 is compressed"},
		{"/api/v1/play/sid/seg00001.m4s", "", true, "media segment"},
		{"/api/v1/items/x/download", "bytes=0-1023", true, "range request"},
		{"/api/v1/libraries/x/items", "bytes=0-99", true, "range beats content type"},
	}
	for _, tc := range tests {
		c := newTestContext(tc.path, tc.rangeH)
		if got := incompressible(c); got != tc.skip {
			t.Errorf("incompressible(%q, Range:%q) = %v, want %v (%s)", tc.path, tc.rangeH, got, tc.skip, tc.why)
		}
	}
}
