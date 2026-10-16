package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/store"
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
	themes, err := builtinThemes()
	if err != nil {
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

// Custom themes: whole colour sets only, shared only by an admin, other
// people's private themes out of sight, and CSS that carries nothing but
// colours.
func TestCustomThemeAPI(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := st.CreateUser(ctx, "admin", "", "h", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	kid, err := st.CreateUser(ctx, "kid", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{store: st, events: events.NewBus()}
	call := func(u store.User, method, id, body string, h func(echo.Context) error) (*httptest.ResponseRecorder, error) {
		req := httptest.NewRequest(method, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.Set(ctxUser, u)
		if id != "" {
			c.SetParamNames("id")
			c.SetParamValues(id)
		}
		return rec, h(c)
	}
	status := func(err error) int {
		var he *echo.HTTPError
		if errors.As(err, &he) {
			return he.Code
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return 200
	}
	colours := `{"bg":"#F8F4EC","panel":"#ffffff","border":"#e0d8c8","text":"#222222","muted":"#666666","accent":"#aa3355","accent-2":"#225588","on-accent":"#ffffff","ok":"#228844","err":"#cc3333","chart-1":"#7c5cff","chart-2":"#1f8fcc","chart-3":"#ad8312"}`

	for name, body := range map[string]string{
		"no name":          `{"tokens":` + colours + `}`,
		"a colour short":   `{"name":"X","tokens":{"bg":"#000000"}}`,
		"not a colour":     `{"name":"X","tokens":` + strings.Replace(colours, `"#222222"`, `"red; background: url(x)"`, 1) + `}`,
		"sharing as kid":   `{"name":"X","tokens":` + colours + `,"household":true}`,
		"dim out of range": `{"name":"X","tokens":` + colours + `,"dim":99}`,
	} {
		if _, err := call(kid, http.MethodPost, "", body, s.createTheme); status(err) < 400 {
			t.Errorf("%s: accepted", name)
		}
	}

	rec, err := call(kid, http.MethodPost, "", `{"name":"Sunny","tokens":`+colours+`,"effect":"blossom"}`, s.createTheme)
	if err != nil {
		t.Fatal(err)
	}
	var sunny themeInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sunny); err != nil {
		t.Fatal(err)
	}
	if !sunny.Custom || !sunny.Mine || sunny.Appearance != "light" || sunny.Tokens["bg"] != "#f8f4ec" || sunny.Dim != 70 || sunny.Effect != "blossom" {
		t.Fatalf("created = %+v", sunny)
	}

	// The admin sees it (to tidy up) but a kid's theme is not offered to
	// others, and nobody else but an admin may change it.
	other, err := st.CreateUser(ctx, "other", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call(other, http.MethodPatch, sunny.ID, `{"name":"Mine now"}`, s.updateTheme); status(err) != http.StatusNotFound {
		t.Fatalf("another user's edit: %v", err)
	}
	if _, err := call(admin, http.MethodPatch, sunny.ID, `{"household":true}`, s.updateTheme); err != nil {
		t.Fatalf("admin shares it: %v", err)
	}
	if got, _ := st.CustomThemes(ctx, other.ID); len(got) != 1 || got[0].ID != sunny.ID {
		t.Fatalf("shared themes for another user = %v", got)
	}
	if _, err := call(other, http.MethodPatch, sunny.ID, `{"name":"Mine now"}`, s.updateTheme); status(err) != http.StatusForbidden {
		t.Fatalf("another user's edit of a shared theme: %v", err)
	}

	rec, err = call(store.User{}, http.MethodGet, sunny.ID, "", s.customThemeCSS)
	if err != nil {
		t.Fatal(err)
	}
	css := rec.Body.String()
	if !strings.Contains(css, "--bg: #f8f4ec;") || !strings.Contains(css, "--bg-dim: 0.70;") || strings.Contains(css, "url(") {
		t.Fatalf("css =\n%s", css)
	}
	if _, err := call(kid, http.MethodDelete, sunny.ID, "", s.deleteTheme); err != nil {
		t.Fatal(err)
	}
	if _, err := call(store.User{}, http.MethodGet, sunny.ID, "", s.customThemeCSS); status(err) != http.StatusNotFound {
		t.Fatalf("css after delete: %v", err)
	}
}
