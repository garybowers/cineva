package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestThemeTokens(t *testing.T) {
	for _, tc := range []struct {
		name, css string
		want      map[string]string
	}{
		{"flat root", ":root {\n  --bg: #0e1116;\n  --on-accent: #ffffff; /* text on accent */\n  --chart-1: #7c5cff;\n}",
			map[string]string{"bg": "#0e1116", "on-accent": "#ffffff", "chart-1": "#7c5cff"}},
		{"commented out, and the last definition wins", ":root { /* --bg: #000; */ --bg: #111; --bg: #222 }",
			map[string]string{"bg": "#222"}},
		{"colour functions", ":root { --panel: rgb(10 20 30 / 0.5); --border: color-mix(in srgb, #fff 10%, #000); }",
			map[string]string{"panel": "rgb(10 20 30 / 0.5)", "border": "color-mix(in srgb, #fff 10%, #000)"}},
		{"nothing that could load or escape", `:root { --bg: url(https://example.com/x.png); --panel: "a"; --text: #eee; }`,
			map[string]string{"text": "#eee"}},
		{"no tokens", "body { color: red; }", map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := themeTokens(tc.css); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("themeTokens = %v, want %v", got, tc.want)
			}
		})
	}
}

// Every theme folder defines the whole token set both UIs are built
// against, and the list hands it out for previews.
func TestListThemesTokens(t *testing.T) {
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/v1/themes", nil), rec)
	if err := listThemes(c); err != nil {
		t.Fatal(err)
	}
	var themes []themeInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &themes); err != nil {
		t.Fatal(err)
	}
	if len(themes) == 0 {
		t.Fatal("no themes")
	}
	contract := []string{"bg", "panel", "border", "text", "muted", "accent", "accent-2", "on-accent", "ok", "err", "chart-1", "chart-2", "chart-3"}
	for _, th := range themes {
		for _, k := range contract {
			if th.Tokens[k] == "" {
				t.Errorf("theme %s: no --%s", th.ID, k)
			}
		}
	}
}
