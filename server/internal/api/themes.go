package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/avatar"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/occasion"
	"github.com/garybowers/cineva/server/internal/store"
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
	// Background is the URL of the theme's background picture ("" for
	// none) and Dim how strongly the background colour covers it, in per
	// cent. Effect is the animated touch it shows: "snow", "leaves",
	// "blossom" or "". Occasion is the holiday a built-in theme is for.
	Background string `json:"background,omitempty"`
	Dim        int    `json:"dim,omitempty"`
	Effect     string `json:"effect,omitempty"`
	Occasion   string `json:"occasion,omitempty"`
	// Custom themes (made in Settings): who made it, whether it is shared
	// with everyone, whether the caller made it, and the stylesheet to
	// load, which changes with every edit.
	Custom    bool   `json:"custom,omitempty"`
	OwnerID   string `json:"ownerId,omitempty"`
	Household bool   `json:"household,omitempty"`
	Mine      bool   `json:"mine,omitempty"`
	CSS       string `json:"css"`
}

// builtinThemes enumerates the theme folders (one directory per theme,
// with theme.css and a theme.json manifest).
func builtinThemes() ([]themeInfo, error) {
	themes := ui.Themes()
	entries, err := fs.ReadDir(themes, ".")
	if err != nil {
		return nil, fmt.Errorf("theme folders: %w", err)
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
		t.Custom, t.OwnerID, t.Household, t.Mine = false, "", false, false
		// A manifest names its background by file, beside it.
		if t.Background != "" {
			t.Background = "/themes/" + t.ID + "/" + path.Base(t.Background)
		}
		t.CSS = "/themes/" + t.ID + "/theme.css"
		t.Tokens = map[string]string{}
		if css, err := fs.ReadFile(themes, e.Name()+"/theme.css"); err == nil {
			t.Tokens = themeTokens(string(css))
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// GET /themes — the built-in themes, then the custom ones the caller may
// pick: everyone's shared ones, and when signed in their own.
func (s *server) listThemes(c echo.Context) error {
	out, err := builtinThemes()
	if err != nil {
		return err
	}
	uid := ""
	if u := s.optionalUser(c); u != nil {
		uid = u.ID
	}
	custom, err := s.store.CustomThemes(c.Request().Context(), uid)
	if err != nil {
		return err
	}
	for _, t := range custom {
		out = append(out, customThemeInfo(t, uid))
	}
	return c.JSON(http.StatusOK, out)
}

func customThemeInfo(t store.CustomTheme, uid string) themeInfo {
	return themeInfo{
		ID: t.ID, Name: t.Name, Appearance: t.Appearance, Tokens: t.Tokens,
		Background: t.Background, Dim: t.Dim, Effect: t.Effect,
		Custom: true, OwnerID: t.OwnerID, Household: t.Household, Mine: uid != "" && t.OwnerID == uid,
		CSS: "/api/v1/themes/" + t.ID + "/theme.css?v=" + url.QueryEscape(t.UpdatedAt),
	}
}

// GET /themes/occasion — the holiday on now, and its theme; 204 outside
// every holiday's window. Read in the server's own time zone.
func (s *server) currentOccasion(c echo.Context) error {
	o, last, ok := occasion.Active(time.Now())
	if !ok {
		return c.NoContent(http.StatusNoContent)
	}
	themeID := ""
	if builtin, err := builtinThemes(); err == nil {
		for _, t := range builtin {
			if t.Occasion == o.ID {
				themeID = t.ID
			}
		}
	}
	return c.JSON(http.StatusOK, map[string]string{"id": o.ID, "name": o.Name, "until": last.Format(time.DateOnly), "themeId": themeID})
}

// GET /themes/:id/theme.css — a custom theme as a stylesheet, the same
// shape as a built-in theme's file, so both load the same way. Public, like
// the theme folders: the profile picker is drawn in the household's theme
// before anyone signs in, and the id is not guessable.
func (s *server) customThemeCSS(c echo.Context) error {
	t, err := s.store.CustomTheme(c.Request().Context(), c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "theme not found")
	}
	var b strings.Builder
	b.WriteString("/* Cineva theme: " + strings.NewReplacer("*/", "", "\n", " ").Replace(t.Name) + " (custom). */\n:root {\n")
	keys := make([]string, 0, len(t.Tokens))
	for k := range t.Tokens {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		// Checked when saved; checked again, as this is CSS.
		if themeTokenNames[k] && hexColour.MatchString(t.Tokens[k]) {
			fmt.Fprintf(&b, "  --%s: %s;\n", k, t.Tokens[k])
		}
	}
	if strings.HasPrefix(t.Background, "/artwork/") && safeArtworkPath.MatchString(t.Background) {
		fmt.Fprintf(&b, "  --bg-image: url(\"%s\");\n", t.Background)
	}
	fmt.Fprintf(&b, "  --bg-dim: %.2f;\n}\n", float64(t.Dim)/100)
	c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
	return c.Blob(http.StatusOK, "text/css; charset=utf-8", []byte(b.String()))
}

// themeTokenNames is the token contract every theme defines (CLAUDE.md,
// Theming): what a custom theme must give a colour for.
var themeTokenNames = map[string]bool{
	"bg": true, "panel": true, "border": true, "text": true, "muted": true,
	"accent": true, "accent-2": true, "on-accent": true, "ok": true, "err": true,
	"chart-1": true, "chart-2": true, "chart-3": true,
}

var (
	hexColour       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	safeArtworkPath = regexp.MustCompile(`^/artwork/[A-Za-z0-9._-]+$`)
)

var themeEffects = map[string]bool{"": true, "snow": true, "leaves": true, "blossom": true}

// themeRequest is a custom theme as a client sends it. Every field is
// optional on PATCH.
type themeRequest struct {
	Name      *string           `json:"name"`
	Tokens    map[string]string `json:"tokens"`
	Dim       *int              `json:"dim"`
	Effect    *string           `json:"effect"`
	Household *bool             `json:"household"`
}

// apply checks a request against the rules and fills t from it.
func (r themeRequest) apply(t *store.CustomTheme, admin bool) error {
	if r.Name != nil {
		n := strings.TrimSpace(*r.Name)
		if n == "" || utf8.RuneCountInString(n) > 60 {
			return echo.NewHTTPError(http.StatusBadRequest, "a theme needs a name of up to 60 characters")
		}
		t.Name = n
	}
	if r.Tokens != nil {
		tokens := map[string]string{}
		for k := range themeTokenNames {
			v := strings.ToLower(strings.TrimSpace(r.Tokens[k]))
			if !hexColour.MatchString(v) {
				return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("--%s needs a colour such as #1a2b3c", k))
			}
			tokens[k] = v
		}
		t.Tokens = tokens
		t.Appearance = "dark"
		if luminance(tokens["bg"]) > 0.5 {
			t.Appearance = "light"
		}
	}
	if r.Dim != nil {
		if *r.Dim < 0 || *r.Dim > 95 {
			return echo.NewHTTPError(http.StatusBadRequest, "dim is 0 to 95 per cent")
		}
		t.Dim = *r.Dim
	}
	if r.Effect != nil {
		if !themeEffects[*r.Effect] {
			return echo.NewHTTPError(http.StatusBadRequest, "effect is snow, leaves, blossom or none")
		}
		t.Effect = *r.Effect
	}
	if r.Household != nil {
		if *r.Household != t.Household && !admin {
			return echo.NewHTTPError(http.StatusForbidden, "only an administrator can share a theme with everyone")
		}
		t.Household = *r.Household
	}
	return nil
}

// luminance is a #rrggbb colour's relative luminance (WCAG), 0 to 1.
func luminance(hex string) float64 {
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return 0
	}
	lin := func(c int) float64 {
		v := float64(c) / 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// POST /themes — make a theme.
func (s *server) createTheme(c echo.Context) error {
	var req themeRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if req.Name == nil || req.Tokens == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "a theme needs a name and its colours")
	}
	me := currentUser(c)
	t := store.CustomTheme{OwnerID: me.ID, Dim: 70}
	if err := req.apply(&t, me.IsAdmin()); err != nil {
		return err
	}
	saved, err := s.store.SaveCustomTheme(c.Request().Context(), t)
	if err != nil {
		return err
	}
	s.themesChanged(c, "theme.create", saved)
	return c.JSON(http.StatusCreated, customThemeInfo(saved, me.ID))
}

// editableTheme is a custom theme the caller may change: their own, or as
// an admin a shared one or anyone's.
func (s *server) editableTheme(c echo.Context) (store.CustomTheme, error) {
	t, err := s.store.CustomTheme(c.Request().Context(), c.Param("id"))
	me := currentUser(c)
	if err != nil || (t.OwnerID != me.ID && !t.Household && !me.IsAdmin()) {
		return t, echo.NewHTTPError(http.StatusNotFound, "theme not found")
	}
	if t.OwnerID != me.ID && !me.IsAdmin() {
		return t, echo.NewHTTPError(http.StatusForbidden, "only its maker or an administrator can change this theme")
	}
	return t, nil
}

// PATCH /themes/:id — change a theme.
func (s *server) updateTheme(c echo.Context) error {
	t, err := s.editableTheme(c)
	if err != nil {
		return err
	}
	var req themeRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	me := currentUser(c)
	if err := req.apply(&t, me.IsAdmin()); err != nil {
		return err
	}
	saved, err := s.store.SaveCustomTheme(c.Request().Context(), t)
	if err != nil {
		return err
	}
	s.themesChanged(c, "theme.update", saved)
	return c.JSON(http.StatusOK, customThemeInfo(saved, me.ID))
}

// DELETE /themes/:id — remove a theme. Anyone using it goes back to the
// default theme.
func (s *server) deleteTheme(c echo.Context) error {
	t, err := s.editableTheme(c)
	if err != nil {
		return err
	}
	if err := s.store.DeleteCustomTheme(c.Request().Context(), t.ID); err != nil {
		return err
	}
	s.removeArtworkFile(t.Background, themeBackgroundPrefix)
	s.themesChanged(c, "theme.delete", t)
	return c.NoContent(http.StatusNoContent)
}

// themeBackgroundPrefix starts a theme background's /artwork/ path.
const themeBackgroundPrefix = "/artwork/theme-"

// PUT /themes/:id/background — the theme's background picture, multipart
// field "image".
func (s *server) putThemeBackground(c echo.Context) error {
	t, err := s.editableTheme(c)
	if err != nil {
		return err
	}
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, avatar.MaxBytes+1<<20)
	fh, err := c.FormFile("image")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "send the picture as the multipart field \"image\" (at most 15 MB)")
	}
	f, err := fh.Open()
	if err != nil {
		return fmt.Errorf("open upload: %w", err)
	}
	defer f.Close()
	jpg, err := avatar.Backdrop(f)
	if errors.Is(err, avatar.ErrNotImage) {
		return echo.NewHTTPError(http.StatusBadRequest, "that is not a JPEG, PNG, GIF or WebP picture")
	} else if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("background name: %w", err)
	}
	name := strings.TrimPrefix(themeBackgroundPrefix, "/artwork/") + t.ID + "-" + hex.EncodeToString(suffix) + ".jpg"
	dir := s.manager.ArtworkDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("artwork dir: %w", err)
	}
	tmp := filepath.Join(dir, name+".part")
	if err := os.WriteFile(tmp, jpg, 0o644); err != nil {
		return fmt.Errorf("write background: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("save background: %w", err)
	}
	if err := s.store.SetCustomThemeBackground(r.Context(), t.ID, "/artwork/"+name); err != nil {
		return err
	}
	s.removeArtworkFile(t.Background, themeBackgroundPrefix)
	return s.themeSaved(c, t.ID, "theme.background")
}

// DELETE /themes/:id/background — take the picture away.
func (s *server) deleteThemeBackground(c echo.Context) error {
	t, err := s.editableTheme(c)
	if err != nil {
		return err
	}
	if err := s.store.SetCustomThemeBackground(c.Request().Context(), t.ID, ""); err != nil {
		return err
	}
	s.removeArtworkFile(t.Background, themeBackgroundPrefix)
	return s.themeSaved(c, t.ID, "theme.background")
}

func (s *server) themeSaved(c echo.Context, id, action string) error {
	t, err := s.store.CustomTheme(c.Request().Context(), id)
	if err != nil {
		return err
	}
	s.themesChanged(c, action, t)
	return c.JSON(http.StatusOK, customThemeInfo(t, currentUser(c).ID))
}

// themesChanged records a change and tells open screens: a shared theme
// changes for everyone, a private one for its maker.
func (s *server) themesChanged(c echo.Context, action string, t store.CustomTheme) {
	s.audit(c, action, "theme", t.ID, t.Name, "")
	ev := events.Event{Type: "themes"}
	if !t.Household {
		ev.User = t.OwnerID
	}
	s.events.Publish(ev)
}

// removeArtworkFile deletes a file this server wrote to the artwork folder,
// when path is one of the given kind.
func (s *server) removeArtworkFile(path, prefix string) {
	if !strings.HasPrefix(path, prefix) || !safeArtworkPath.MatchString(path) {
		return
	}
	file := filepath.Join(s.manager.ArtworkDir, filepath.Base(path))
	if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		serverLog.Warn("removing old picture failed", "path", file, "err", err)
	}
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
