package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/prefs"
	"github.com/garybowers/cineva/server/internal/store"
)

// Preferences in layers: the viewer's device and their own choices, over
// their groups' and the server's defaults, over the built-in ones. The
// registry and the resolution live in internal/prefs; these handlers only
// read requests and write responses.

// myPrefs is the resolved view: every preference's value and where it came
// from, with the registry so a client can draw Settings without knowing the
// keys in advance.
type myPrefs struct {
	// Device is the device resolved for (a session's public id), absent
	// when there is none, as for an API key.
	Device   string                    `json:"device,omitempty"`
	Sections []prefs.Section           `json:"sections"`
	Registry []prefs.Pref              `json:"registry"`
	Prefs    map[string]prefs.Resolved `json:"prefs"`
}

// prefDevice is the device to resolve or write for: the one named (which
// must be the caller's own), "current" or "" for the caller's own session.
// "" is returned for a caller with no session and no device named.
func (s *server) prefDevice(c echo.Context, id string) (string, error) {
	current, _ := c.Get(ctxSessionID).(string)
	if id == "" || id == "current" || id == current {
		if id == "current" && current == "" {
			return "", echo.NewHTTPError(http.StatusBadRequest, "only a signed-in browser has a device session")
		}
		return current, nil
	}
	if _, err := s.store.UserSession(c.Request().Context(), userID(c), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		return "", err
	}
	return id, nil
}

// resolvedPrefs is the caller's preference values on the device they are
// using, as playback reads them.
func (s *server) resolvedPrefs(c echo.Context) (map[string]string, error) {
	device, _ := c.Get(ctxSessionID).(string)
	r, err := s.prefs.Resolve(c.Request().Context(), userID(c), device)
	if err != nil {
		return nil, err
	}
	return prefs.Values(r), nil
}

func (s *server) writeMyPrefs(c echo.Context, device string) error {
	r, err := s.prefs.Resolve(c.Request().Context(), userID(c), device)
	if err != nil {
		return err
	}
	reg, err := s.prefs.Registry(c.Request().Context(), prefs.ResolvedKeep(r))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, myPrefs{Device: device, Sections: prefs.Sections, Registry: reg, Prefs: r})
}

// GET /auth/me/prefs?device= — every preference as it applies to you on
// this device (or another of yours).
func (s *server) getMyPrefs(c echo.Context) error {
	device, err := s.prefDevice(c, c.QueryParam("device"))
	if err != nil {
		return err
	}
	return s.writeMyPrefs(c, device)
}

// prefError turns the service's refusals into responses: a lock is a 409
// that names it, anything else it rejected a 400.
func prefError(err error) error {
	var locked *prefs.LockedError
	if errors.As(err, &locked) {
		return echo.NewHTTPError(http.StatusConflict, lockedBody{
			Message: locked.Error(), Key: locked.Key, Source: locked.Source, GroupName: locked.GroupName,
		})
	}
	if errors.Is(err, prefs.ErrInvalid) {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return err
}

type lockedBody struct {
	Message   string       `json:"message"`
	Key       string       `json:"key"`
	Source    prefs.Source `json:"source"`
	GroupName string       `json:"groupName,omitempty"`
}

// PUT /auth/me/prefs {key: value | null} — set your own preferences. A
// value sets a key; null hands it back to the default.
func (s *server) putMyPrefs(c echo.Context) error {
	var changes map[string]*string
	if err := c.Bind(&changes); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := s.prefs.SetUser(c.Request().Context(), userID(c), changes); err != nil {
		return prefError(err)
	}
	// Person preferences follow the person: their other open devices
	// fetch them again.
	s.events.Publish(events.Event{Type: "prefs", User: userID(c)})
	device, _ := c.Get(ctxSessionID).(string)
	return s.writeMyPrefs(c, device)
}

// GET /auth/sessions/:id/prefs — one of your devices' own values ("current"
// for this one), before any resolution.
func (s *server) getDevicePrefs(c echo.Context) error {
	device, err := s.prefDevice(c, c.Param("id"))
	if err != nil {
		return err
	}
	if device == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "only a signed-in browser has a device session")
	}
	values, err := s.store.DevicePrefs(c.Request().Context(), device)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, values)
}

// PUT /auth/sessions/:id/prefs {key: value | null} — set device-scoped
// preferences for one of your devices ("current" for this one). Answers with
// the preferences resolved for that device.
func (s *server) putDevicePrefs(c echo.Context) error {
	device, err := s.prefDevice(c, c.Param("id"))
	if err != nil {
		return err
	}
	if device == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "only a signed-in browser has a device session")
	}
	var changes map[string]*string
	if err := c.Bind(&changes); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := s.prefs.SetDevice(c.Request().Context(), userID(c), device, changes); err != nil {
		return prefError(err)
	}
	s.events.Publish(events.Event{Type: "prefs", User: userID(c), Session: device})
	return s.writeMyPrefs(c, device)
}

// ---- Admin: the server's and the groups' defaults ----

// prefDefaults is the admin's table: the registry and every column.
type prefDefaults struct {
	Sections []prefs.Section `json:"sections"`
	Registry []prefs.Pref    `json:"registry"`
	prefs.Defaults
}

func (s *server) allPrefDefaults(c echo.Context) (prefDefaults, error) {
	groups, err := s.store.Groups(c.Request().Context())
	if err != nil {
		return prefDefaults{}, err
	}
	d, err := s.prefs.AllDefaults(c.Request().Context(), groups)
	if err != nil {
		return prefDefaults{}, err
	}
	reg, err := s.prefs.Registry(c.Request().Context(), prefs.DefaultsKeep(d))
	if err != nil {
		return prefDefaults{}, err
	}
	return prefDefaults{Sections: prefs.Sections, Registry: reg, Defaults: d}, nil
}

// GET /prefs/defaults — the registry, the server's defaults, and every
// group's in priority order.
func (s *server) getPrefDefaults(c echo.Context) error {
	out, err := s.allPrefDefaults(c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// PUT /prefs/defaults {key: {value, locked} | null} — the server's defaults.
func (s *server) putPrefDefaults(c echo.Context) error {
	changes, err := bindDefaults(c)
	if err != nil {
		return err
	}
	if err := s.prefs.SetDefaults(c.Request().Context(), store.PrefOwnerServer, "", changes); err != nil {
		return prefError(err)
	}
	s.audit(c, "prefs.defaults", "server", "", "Server", describeDefaults(changes))
	s.events.Publish(events.Event{Type: "prefs"})
	out, err := s.allPrefDefaults(c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// GET /groups/:id/prefs — one group's defaults.
func (s *server) getGroupPrefs(c echo.Context) error {
	d, err := s.groupDefaults(c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, d.Defaults)
}

// PUT /groups/:id/prefs {key: {value, locked} | null} — a group's defaults.
func (s *server) putGroupPrefs(c echo.Context) error {
	changes, err := bindDefaults(c)
	if err != nil {
		return err
	}
	g, err := s.groupDefaults(c)
	if err != nil {
		return err
	}
	if err := s.prefs.SetDefaults(c.Request().Context(), store.PrefOwnerGroup, g.ID, changes); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "group not found")
		}
		return prefError(err)
	}
	s.audit(c, "prefs.defaults", "group", g.ID, g.Name, describeDefaults(changes))
	s.events.Publish(events.Event{Type: "prefs"})
	g, err = s.groupDefaults(c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, g.Defaults)
}

func (s *server) groupDefaults(c echo.Context) (prefs.GroupDefaults, error) {
	groups, err := s.store.Groups(c.Request().Context())
	if err != nil {
		return prefs.GroupDefaults{}, err
	}
	for _, g := range groups {
		if g.ID == c.Param("id") {
			d, err := s.prefs.AllDefaults(c.Request().Context(), []store.UserGroup{g})
			if err != nil {
				return prefs.GroupDefaults{}, err
			}
			return d.Groups[0], nil
		}
	}
	return prefs.GroupDefaults{}, echo.NewHTTPError(http.StatusNotFound, "group not found")
}

func bindDefaults(c echo.Context) (map[string]*store.PrefSetting, error) {
	var changes map[string]*store.PrefSetting
	if err := c.Bind(&changes); err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	return changes, nil
}

// describeDefaults is the audit trail's detail: "audio-lang = en (locked),
// sub-mode cleared".
func describeDefaults(changes map[string]*store.PrefSetting) string {
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := changes[k]
		switch {
		case v == nil:
			parts = append(parts, k+" cleared")
		case v.Locked:
			parts = append(parts, fmt.Sprintf("%s = %q (locked)", k, v.Value))
		default:
			parts = append(parts, fmt.Sprintf("%s = %q", k, v.Value))
		}
	}
	return strings.Join(parts, ", ")
}

// PUT /groups/order {groupIds} — which group's defaults win where they
// conflict: the first listed.
func (s *server) setGroupOrder(c echo.Context) error {
	var req struct {
		GroupIDs []string `json:"groupIds"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	if err := s.store.SetGroupOrder(ctx, req.GroupIDs); err != nil {
		if errors.Is(err, store.ErrInvalidOrder) {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return err
	}
	groups, err := s.store.Groups(ctx)
	if err != nil {
		return err
	}
	names := make([]string, len(groups))
	for i, g := range groups {
		names[i] = g.Name
	}
	s.audit(c, "group.order", "group", "", "", strings.Join(names, ", "))
	s.events.Publish(events.Event{Type: "users"})
	s.events.Publish(events.Event{Type: "prefs"})
	return c.JSON(http.StatusOK, groups)
}
