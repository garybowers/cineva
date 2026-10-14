package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/homeassistant"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/playback"
	"github.com/garybowers/cineva/server/internal/store"
)

// Home Assistant: the connection, exposed entities and who may use them,
// rooms, cameras and modes (see docs/homeassistant.md). The service holds
// the live connection; these handlers check access and translate.

var haLog = logging.For("homeassistant")

// haDefaultName is what a connection is called when the admin names none.
const haDefaultName = "Home Assistant"

// principals caches who a user is for access checks, because an entity's
// state change is checked once per open event stream.
type principals struct {
	mu sync.Mutex
	m  map[string]principalEntry
}

type principalEntry struct {
	p  homeassistant.Principal
	at time.Time
}

const principalTTL = 30 * time.Second

func (s *server) haPrincipal(ctx context.Context, userID string) homeassistant.Principal {
	s.haWho.mu.Lock()
	if e, ok := s.haWho.m[userID]; ok && time.Since(e.at) < principalTTL {
		s.haWho.mu.Unlock()
		return e.p
	}
	s.haWho.mu.Unlock()
	p := homeassistant.Principal{ID: userID}
	if u, err := s.store.UserByID(ctx, userID); err == nil {
		p.Admin = u.IsAdmin()
	}
	if groups, err := s.store.UserGroupIDs(ctx, userID); err == nil {
		p.GroupIDs = groups
	}
	s.haWho.mu.Lock()
	if s.haWho.m == nil {
		s.haWho.m = map[string]principalEntry{}
	}
	s.haWho.m[userID] = principalEntry{p: p, at: time.Now()}
	s.haWho.mu.Unlock()
	return p
}

func (s *server) callerPrincipal(c echo.Context) homeassistant.Principal {
	u := currentUser(c)
	p := s.haPrincipal(c.Request().Context(), u.ID)
	p.Admin = u.IsAdmin() // the request's own word is the freshest
	return p
}

// startHomeAssistant wires the service's callbacks to the event bus and
// opens the connection, if one is saved.
func (s *server) startHomeAssistant(ctx context.Context) {
	s.ha.OnChange = func() { s.events.Publish(events.Event{Type: "homeassistant"}) }
	s.ha.OnStates = func(changed []homeassistant.Entity) {
		for _, e := range changed {
			id := e.EntityID
			s.events.Publish(events.Event{
				Type: "homeassistant",
				Data: map[string]any{"entities": []homeassistant.Entity{e}},
				Allow: func(userID string) bool {
					return s.ha.CanSee(s.haPrincipal(context.Background(), userID), id)
				},
			})
		}
	}
	if err := s.ha.Start(ctx); err != nil {
		haLog.Error("starting home assistant failed", "err", err)
	}
}

// haError turns the service's errors into responses: a rule broken is the
// caller's (400), Home Assistant away or refusing is upstream (502).
func haError(err error) error {
	switch {
	case errors.Is(err, homeassistant.ErrInvalid):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	default:
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
}

// ---- connection (admin) ----

type haConnectionOut struct {
	Configured     bool   `json:"configured"`
	Name           string `json:"name,omitempty"`
	URL            string `json:"url,omitempty"`
	Connected      bool   `json:"connected"`
	Version        string `json:"version,omitempty"`
	IsAdmin        bool   `json:"isAdmin"`
	PlaybackEvents bool   `json:"playbackEvents"`
	Error          string `json:"error,omitempty"`
	Entities       int    `json:"entities"`
	Exposed        int    `json:"exposed"`
	CreatedAt      string `json:"createdAt,omitempty"`
	UpdatedAt      string `json:"updatedAt,omitempty"`
}

func (s *server) haConnectionOut(ctx context.Context) (haConnectionOut, error) {
	cfg, err := s.store.HAConnection(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return haConnectionOut{}, nil
	} else if err != nil {
		return haConnectionOut{}, err
	}
	st := s.ha.Status()
	out := haConnectionOut{
		Configured: true, Name: cfg.Name, URL: cfg.URL, Connected: st.Connected,
		Version: cfg.Version, IsAdmin: st.IsAdmin, PlaybackEvents: cfg.PlaybackEvents, Error: st.Error,
		Entities: len(s.ha.Entities()), Exposed: len(s.ha.Exposure()),
		CreatedAt: cfg.CreatedAt, UpdatedAt: cfg.UpdatedAt,
	}
	if st.Version != "" {
		out.Version = st.Version
	}
	return out, nil
}

// GET /homeassistant
func (s *server) getHomeAssistant(c echo.Context) error {
	out, err := s.haConnectionOut(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// PUT /homeassistant — connect, or change the connection. Tried live
// before anything is saved.
func (s *server) putHomeAssistant(c echo.Context) error {
	ctx := c.Request().Context()
	var req struct {
		Name           string `json:"name"`
		URL            string `json:"url"`
		Token          string `json:"token"`
		PlaybackEvents *bool  `json:"playbackEvents"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	cfg, err := s.store.HAConnection(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		cfg.Name = name
	} else if cfg.Name == "" {
		cfg.Name = haDefaultName
	}
	if u := strings.TrimSpace(req.URL); u != "" {
		cfg.URL = strings.TrimRight(u, "/")
	}
	if t := strings.TrimSpace(req.Token); t != "" {
		cfg.Token = t
	}
	if req.PlaybackEvents != nil {
		cfg.PlaybackEvents = *req.PlaybackEvents
	}
	if !httpURL(cfg.URL) {
		return echo.NewHTTPError(http.StatusBadRequest, "url must be an http(s) URL")
	}
	if cfg.Token == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "token is required")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	check, err := homeassistant.Check(checkCtx, cfg.URL, cfg.Token)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	if cfg.PlaybackEvents && !check.IsAdmin {
		return echo.NewHTTPError(http.StatusBadRequest,
			"playback events need the token's user to be an administrator in Home Assistant, which firing events requires")
	}
	cfg.Version = check.Version
	saved, err := s.store.SaveHAConnection(ctx, cfg)
	if err != nil {
		return err
	}
	s.ha.Apply(&saved)
	s.audit(c, "homeassistant.connect", "homeassistant", "", saved.Name, saved.URL)
	// The check just succeeded, so the service's own connect normally
	// follows within a moment; answering after it saves the page showing
	// "unreachable" for a second.
	for i := 0; i < 20 && !s.ha.Status().Connected; i++ {
		time.Sleep(250 * time.Millisecond)
	}
	out, err := s.haConnectionOut(ctx)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// DELETE /homeassistant — disconnect; exposure, rooms and modes are kept.
func (s *server) deleteHomeAssistant(c echo.Context) error {
	ctx := c.Request().Context()
	cfg, err := s.store.HAConnection(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return c.NoContent(http.StatusNoContent)
	} else if err != nil {
		return err
	}
	if err := s.store.DeleteHAConnection(ctx); err != nil {
		return err
	}
	s.ha.Apply(nil)
	s.audit(c, "homeassistant.disconnect", "homeassistant", "", cfg.Name, cfg.URL)
	return c.NoContent(http.StatusNoContent)
}

// ---- entities ----

type haEntityAdmin struct {
	homeassistant.Entity
	Exposed bool                `json:"exposed"`
	Access  store.LibraryAccess `json:"access"`
}

func noAccess() store.LibraryAccess {
	return store.LibraryAccess{UserIDs: []string{}, GroupIDs: []string{}}
}

// missingEntity stands in for an exposed entity Home Assistant no longer
// has, so the admin can still hide it.
func missingEntity(id string) homeassistant.Entity {
	d, _, _ := strings.Cut(id, ".")
	return homeassistant.Entity{EntityID: id, Domain: d, Name: id, State: "missing"}
}

// GET /homeassistant/entities (admin)
func (s *server) listHAEntities(c echo.Context) error {
	exposure := s.ha.Exposure()
	names := map[string]string{}
	for _, a := range s.ha.Areas() {
		names[a.ID] = a.Name
	}
	out := []haEntityAdmin{}
	seen := map[string]bool{}
	for _, e := range s.ha.Entities() {
		acc, ok := exposure[e.EntityID]
		if !ok {
			acc = noAccess()
		}
		out = append(out, haEntityAdmin{Entity: e, Exposed: ok, Access: acc})
		seen[e.EntityID] = true
	}
	for id, acc := range exposure {
		if !seen[id] {
			out = append(out, haEntityAdmin{Entity: missingEntity(id), Exposed: true, Access: acc})
		}
	}
	// By area, with no area last, then by name.
	slices.SortFunc(out, func(a, b haEntityAdmin) int {
		an, bn := names[a.AreaID], names[b.AreaID]
		if (an == "") != (bn == "") {
			if an == "" {
				return 1
			}
			return -1
		}
		if c := strings.Compare(strings.ToLower(an), strings.ToLower(bn)); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return c.JSON(http.StatusOK, out)
}

// PUT /homeassistant/entities/:entityId (admin)
func (s *server) putHAEntity(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("entityId")
	if !homeassistant.Supported(id) {
		return echo.NewHTTPError(http.StatusBadRequest, id+" is not a kind of entity Cineva works with")
	}
	var req struct {
		Exposed bool                 `json:"exposed"`
		Access  *store.LibraryAccess `json:"access"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	current, wasExposed := s.ha.Exposure()[id]
	detail := "hidden"
	if req.Exposed {
		acc := current
		switch {
		case req.Access != nil:
			acc = *req.Access
		case !wasExposed:
			acc = noAccess()
			// A camera is admins' only until someone is granted it.
			acc.Restricted = strings.HasPrefix(id, "camera.")
		}
		if err := s.store.ExposeHAEntity(ctx, id, acc); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return echo.NewHTTPError(http.StatusBadRequest, err.Error())
			}
			return err
		}
		detail = "everyone"
		if acc.Restricted {
			detail = fmt.Sprintf("restricted to %d user(s), %d group(s)", len(acc.UserIDs), len(acc.GroupIDs))
		}
	} else if err := s.store.HideHAEntity(ctx, id); err != nil {
		return err
	}
	if err := s.ha.ReloadExposure(ctx); err != nil {
		return err
	}
	s.audit(c, "homeassistant.entity", "homeassistant", id, id, detail)
	s.events.Publish(events.Event{Type: "homeassistant"})
	e, ok := s.ha.Entity(id)
	if !ok {
		e = missingEntity(id)
	}
	acc, exposed := s.ha.Exposure()[id]
	if !exposed {
		acc = noAccess()
	}
	return c.JSON(http.StatusOK, haEntityAdmin{Entity: e, Exposed: exposed, Access: acc})
}

// POST /homeassistant/entities/:entityId/action — work a control.
func (s *server) haEntityAction(c echo.Context) error {
	id := c.Param("entityId")
	if !s.ha.CanSee(s.callerPrincipal(c), id) {
		return echo.NewHTTPError(http.StatusNotFound, "no such control")
	}
	var a homeassistant.Action
	if err := c.Bind(&a); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := s.ha.Do(c.Request().Context(), id, a); err != nil {
		return haError(err)
	}
	haLog.Debug("control worked", "user", currentUser(c).Username, "entity", id, "service", a.Service)
	return c.NoContent(http.StatusNoContent)
}

// ---- rooms ----

type haArea struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Entities []homeassistant.Entity `json:"entities"`
}

// elsewhere names the group of visible entities that have no area.
const elsewhere = "Elsewhere"

// GET /homeassistant/rooms — what the caller may see, by area.
func (s *server) haRooms(c echo.Context) error {
	ctx := c.Request().Context()
	st := s.ha.Status()
	out := struct {
		Configured    bool                 `json:"configured"`
		Connected     bool                 `json:"connected"`
		Areas         []haArea             `json:"areas"`
		AllAreas      []homeassistant.Area `json:"allAreas"`
		CurrentAreaID string               `json:"currentAreaId"`
		// CurrentDeviceID is this device, so a client can tell whether a
		// mode played here or sent the title elsewhere.
		CurrentDeviceID string `json:"currentDeviceId"`
	}{Configured: st.Configured, Connected: st.Connected, Areas: []haArea{}, AllAreas: s.ha.Areas()}
	if id, _ := c.Get(ctxSessionID).(string); id != "" {
		out.CurrentDeviceID = id
		if sess, err := s.store.SessionByID(ctx, id); err == nil {
			out.CurrentAreaID = sess.AreaID
		}
	}
	if !st.Configured {
		return c.JSON(http.StatusOK, out)
	}
	p := s.callerPrincipal(c)
	byArea := map[string]*haArea{}
	for _, a := range out.AllAreas {
		byArea[a.ID] = &haArea{ID: a.ID, Name: a.Name, Entities: []homeassistant.Entity{}}
	}
	for _, e := range s.ha.Entities() {
		if !s.ha.CanSee(p, e.EntityID) {
			continue
		}
		key := e.AreaID
		if _, ok := byArea[key]; !ok || key == "" {
			// No area, or one Home Assistant no longer lists.
			key = ""
			if _, ok := byArea[""]; !ok {
				byArea[""] = &haArea{ID: "", Name: elsewhere, Entities: []homeassistant.Entity{}}
			}
		}
		byArea[key].Entities = append(byArea[key].Entities, e)
	}
	for _, a := range out.AllAreas {
		if area := byArea[a.ID]; len(area.Entities) > 0 {
			out.Areas = append(out.Areas, *area)
		}
	}
	if area, ok := byArea[""]; ok && len(area.Entities) > 0 {
		out.Areas = append(out.Areas, *area)
	}
	for i := range out.Areas {
		slices.SortFunc(out.Areas[i].Entities, func(a, b homeassistant.Entity) int {
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		})
	}
	return c.JSON(http.StatusOK, out)
}

// validArea checks an area id against Home Assistant's, when it can: a
// room can be set while Home Assistant is away, and checked then by no one.
func (s *server) validArea(areaID string) error {
	if areaID == "" || !s.ha.Status().Connected {
		return nil
	}
	for _, a := range s.ha.Areas() {
		if a.ID == areaID {
			return nil
		}
	}
	return echo.NewHTTPError(http.StatusBadRequest, "Home Assistant has no area "+areaID)
}

type haDevice struct {
	ID       string `json:"id"`
	UserID   string `json:"userId"`
	UserName string `json:"userName"`
	Name     string `json:"name"`
	Online   bool   `json:"online"`
	AreaID   string `json:"areaId"`
}

// GET /homeassistant/devices (admin) — every user's devices and rooms.
func (s *server) listHADevices(c echo.Context) error {
	ctx := c.Request().Context()
	users, err := s.store.Users(ctx)
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, u := range users {
		names[u.ID] = u.Username
		if u.DisplayName != "" {
			names[u.ID] = u.DisplayName
		}
	}
	devices, err := s.store.Devices(ctx)
	if err != nil {
		return err
	}
	out := make([]haDevice, 0, len(devices))
	for _, d := range devices {
		out = append(out, haDevice{
			ID: d.ID, UserID: d.UserID, UserName: names[d.UserID], Name: deviceName(d),
			Online: s.presence.online(d.ID), AreaID: d.AreaID,
		})
	}
	slices.SortStableFunc(out, func(a, b haDevice) int {
		if c := strings.Compare(strings.ToLower(a.UserName), strings.ToLower(b.UserName)); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return c.JSON(http.StatusOK, out)
}

// PUT /homeassistant/devices/:id/area (admin)
func (s *server) putHADeviceArea(c echo.Context) error {
	ctx := c.Request().Context()
	var req struct {
		AreaID string `json:"areaId"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := s.validArea(req.AreaID); err != nil {
		return err
	}
	d, err := s.store.Device(ctx, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "device not found")
	} else if err != nil {
		return err
	}
	if err := s.store.SetDeviceArea(ctx, d.ID, req.AreaID); err != nil {
		return err
	}
	s.audit(c, "homeassistant.room", "device", d.ID, deviceName(d), "room "+req.AreaID)
	if d.UserID != "" {
		s.events.Publish(events.Event{Type: "devices", User: d.UserID})
	}
	s.events.Publish(events.Event{Type: "homeassistant"})
	return c.NoContent(http.StatusNoContent)
}

// ---- cameras ----

// cameraFor checks the caller may see a camera, for the camera handlers.
func (s *server) cameraFor(c echo.Context) (string, error) {
	id := c.Param("entityId")
	if !strings.HasPrefix(id, "camera.") || !s.ha.CanSee(s.callerPrincipal(c), id) {
		return "", echo.NewHTTPError(http.StatusNotFound, "no such camera")
	}
	return id, nil
}

// proxy answers with Home Assistant's response: its body and type, never
// cached, since a camera's picture is only good for now.
func proxy(c echo.Context, res *http.Response) error {
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound:
		return echo.NewHTTPError(http.StatusNotFound, "Home Assistant has no such stream file")
	case res.StatusCode != http.StatusOK:
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("Home Assistant answered %s", res.Status))
	}
	h := c.Response().Header()
	h.Set(echo.HeaderContentType, res.Header.Get(echo.HeaderContentType))
	h.Set(echo.HeaderCacheControl, "no-store")
	c.Response().WriteHeader(http.StatusOK)
	_, err := io.Copy(c.Response(), res.Body)
	return err
}

// GET /homeassistant/cameras/:entityId/snapshot
func (s *server) haCameraSnapshot(c echo.Context) error {
	id, err := s.cameraFor(c)
	if err != nil {
		return err
	}
	res, err := s.ha.Snapshot(c.Request().Context(), id)
	if err != nil {
		return haError(err)
	}
	return proxy(c, res)
}

// GET /homeassistant/cameras/:entityId/hls/:file and .../hls/:dir/:file
func (s *server) haCameraHLS(c echo.Context) error {
	id, err := s.cameraFor(c)
	if err != nil {
		return err
	}
	file := c.Param("file")
	if dir := c.Param("dir"); dir != "" {
		file = dir + "/" + file
	}
	res, err := s.ha.HLS(c.Request().Context(), id, file)
	if errors.Is(err, homeassistant.ErrInvalid) {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	} else if err != nil {
		return haError(err)
	}
	return proxy(c, res)
}

// ---- modes ----

type haModeOut struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Steps      []store.HAModeStep   `json:"steps"`
	NeedsTitle bool                 `json:"needsTitle"`
	AreaID     string               `json:"areaId"`
	Access     *store.LibraryAccess `json:"access,omitempty"`
	CreatedAt  string               `json:"createdAt"`
	UpdatedAt  string               `json:"updatedAt"`
}

func (s *server) haModeOut(ctx context.Context, m store.HAMode, admin bool) haModeOut {
	out := haModeOut{
		ID: m.ID, Name: m.Name, Steps: m.Steps, NeedsTitle: homeassistant.NeedsTitle(m),
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
	if admin {
		acc := m.Access
		out.Access = &acc
	}
	if st, ok := homeassistant.PlayStep(m); ok {
		if d, err := s.store.Device(ctx, st.DeviceID); err == nil {
			out.AreaID = d.AreaID
		}
	}
	return out
}

// GET /homeassistant/modes — what the caller may run; an admin gets all.
func (s *server) listHAModes(c echo.Context) error {
	ctx := c.Request().Context()
	modes, err := s.store.HAModes(ctx)
	if err != nil {
		return err
	}
	p := s.callerPrincipal(c)
	out := []haModeOut{}
	for _, m := range modes {
		if homeassistant.Allowed(m.Access, p) {
			out = append(out, s.haModeOut(ctx, m, p.Admin))
		}
	}
	return c.JSON(http.StatusOK, out)
}

type haModeRequest struct {
	Name   *string              `json:"name"`
	Steps  *[]store.HAModeStep  `json:"steps"`
	Access *store.LibraryAccess `json:"access"`
}

// applyModeRequest copies a create or update body onto a mode and checks
// the result: a name, steps by the rules, and a play step whose device and
// fixed title exist.
func (s *server) applyModeRequest(ctx context.Context, m *store.HAMode, req haModeRequest) error {
	if req.Name != nil {
		m.Name = strings.TrimSpace(*req.Name)
	}
	if req.Steps != nil {
		m.Steps = *req.Steps
	}
	if req.Access != nil {
		m.Access = *req.Access
	}
	if m.Name == "" || len([]rune(m.Name)) > 60 {
		return echo.NewHTTPError(http.StatusBadRequest, "a mode needs a name of at most 60 characters")
	}
	if err := homeassistant.ValidateSteps(m.Steps, s.ha.Entity); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if st, ok := homeassistant.PlayStep(*m); ok {
		if _, err := s.store.Device(ctx, st.DeviceID); errors.Is(err, store.ErrNotFound) {
			return echo.NewHTTPError(http.StatusBadRequest, "the play step's device has been forgotten; choose another")
		} else if err != nil {
			return err
		}
		if st.ItemID != "" {
			if _, err := s.store.Item(ctx, st.ItemID); err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "the play step's title is not in the library")
			}
		}
	}
	return nil
}

func modeSaveError(err error) error {
	switch {
	case errors.Is(err, store.ErrHAModeNameUsed):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNotFound):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return err
}

// POST /homeassistant/modes (admin)
func (s *server) createHAMode(c echo.Context) error {
	ctx := c.Request().Context()
	var req haModeRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	m := store.HAMode{Access: noAccess()}
	if err := s.applyModeRequest(ctx, &m, req); err != nil {
		return err
	}
	saved, err := s.store.CreateHAMode(ctx, m)
	if err != nil {
		return modeSaveError(err)
	}
	s.audit(c, "homeassistant.mode.create", "mode", saved.ID, saved.Name, fmt.Sprintf("%d step(s)", len(saved.Steps)))
	s.events.Publish(events.Event{Type: "homeassistant"})
	return c.JSON(http.StatusCreated, s.haModeOut(ctx, saved, true))
}

// PATCH /homeassistant/modes/:id (admin)
func (s *server) updateHAMode(c echo.Context) error {
	ctx := c.Request().Context()
	m, err := s.store.HAMode(ctx, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "mode not found")
	} else if err != nil {
		return err
	}
	var req haModeRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := s.applyModeRequest(ctx, &m, req); err != nil {
		return err
	}
	saved, err := s.store.UpdateHAMode(ctx, m)
	if err != nil {
		return modeSaveError(err)
	}
	s.audit(c, "homeassistant.mode.update", "mode", saved.ID, saved.Name, fmt.Sprintf("%d step(s)", len(saved.Steps)))
	s.events.Publish(events.Event{Type: "homeassistant"})
	return c.JSON(http.StatusOK, s.haModeOut(ctx, saved, true))
}

// DELETE /homeassistant/modes/:id (admin)
func (s *server) deleteHAMode(c echo.Context) error {
	ctx := c.Request().Context()
	m, err := s.store.HAMode(ctx, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "mode not found")
	} else if err != nil {
		return err
	}
	if err := s.store.DeleteHAMode(ctx, m.ID); err != nil {
		return err
	}
	s.audit(c, "homeassistant.mode.delete", "mode", m.ID, m.Name, "")
	s.events.Publish(events.Event{Type: "homeassistant"})
	return c.NoContent(http.StatusNoContent)
}

// modeRunTimeout bounds a run: its waits are capped at a minute, so this
// is room for Home Assistant being slow, not for the mode.
const modeRunTimeout = 3 * time.Minute

// POST /homeassistant/modes/:id/run — check everything that can be
// checked, then run the steps in the background and report by event.
func (s *server) runHAMode(c echo.Context) error {
	ctx := c.Request().Context()
	me := currentUser(c)
	m, err := s.store.HAMode(ctx, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && !homeassistant.Allowed(m.Access, s.callerPrincipal(c))) {
		return echo.NewHTTPError(http.StatusNotFound, "mode not found")
	} else if err != nil {
		return err
	}
	var req struct {
		ItemID string `json:"itemId"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if homeassistant.NeedsTitle(m) && req.ItemID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, m.Name+" plays the title it is started from: choose one")
	}
	if err := s.ha.Preflight(m); errors.Is(err, homeassistant.ErrUnreachable) {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	} else if err != nil {
		return haError(err)
	}
	play := s.modePlayer(me, m.Name)
	if st, ok := homeassistant.PlayStep(m); ok {
		id := st.ItemID
		if id == "" {
			id = req.ItemID
		}
		// Checked now as well as when the step comes, so a run that cannot
		// play does not first dim the lights.
		if _, _, _, err := s.modePlayTarget(ctx, me, st, id); err != nil {
			return err
		}
	}
	runID := uuid.NewString()
	report := func(p homeassistant.Progress) {
		s.events.Publish(events.Event{Type: "mode-run", User: me.ID, Data: p})
	}
	go func() {
		rctx, cancel := context.WithTimeout(context.Background(), modeRunTimeout)
		defer cancel()
		s.ha.Run(rctx, runID, m, req.ItemID, play, report)
	}()
	s.audit(c, "homeassistant.mode.run", "mode", m.ID, m.Name, req.ItemID)
	return c.JSON(http.StatusAccepted, map[string]string{"runId": runID})
}

// modePlayTarget resolves a play step: the device, signed in and with
// Cineva open, its user, and the title, which both the runner and that
// user must be able to see. Errors are HTTP errors, meant to be shown.
func (s *server) modePlayTarget(ctx context.Context, runner store.User, st store.HAModeStep, itemID string) (store.Session, store.User, store.Item, error) {
	d, err := s.store.Device(ctx, st.DeviceID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Session{}, store.User{}, store.Item{}, echo.NewHTTPError(http.StatusConflict, "the device this mode plays on has been forgotten; an admin needs to choose another")
	} else if err != nil {
		return store.Session{}, store.User{}, store.Item{}, err
	}
	target, err := s.store.SessionByID(ctx, st.DeviceID)
	if errors.Is(err, store.ErrNotFound) {
		return target, store.User{}, store.Item{}, echo.NewHTTPError(http.StatusConflict, "nobody is signed in on "+deviceName(d)+" — sign in there first")
	} else if err != nil {
		return target, store.User{}, store.Item{}, err
	}
	if !s.presence.online(target.ID) {
		return target, store.User{}, store.Item{}, echo.NewHTTPError(http.StatusConflict, deviceLabel(target)+" is not connected — open Cineva on it first")
	}
	owner, err := s.store.UserByID(ctx, target.UserID)
	if err != nil {
		return target, owner, store.Item{}, err
	}
	item, err := s.store.Item(ctx, itemID)
	if err != nil || !canSeeLibrary(runner, item.LibraryID) {
		return target, owner, item, echo.NewHTTPError(http.StatusBadRequest, "that title is not in the library")
	}
	if !canSeeLibrary(owner, item.LibraryID) {
		return target, owner, item, echo.NewHTTPError(http.StatusBadRequest, "that title cannot be played on "+deviceLabel(target))
	}
	return target, owner, item, nil
}

// modePlayer plays a mode's title on its device, as "Play on" does, with
// the device's user's own resume position. A device already playing that
// title is left to carry on: a mode started from the player sets the room
// around the film rather than starting it again.
func (s *server) modePlayer(runner store.User, modeName string) homeassistant.Player {
	return func(ctx context.Context, st store.HAModeStep, itemID string) (string, string, error) {
		target, owner, item, err := s.modePlayTarget(ctx, runner, st, itemID)
		if err != nil {
			var he *echo.HTTPError
			if errors.As(err, &he) {
				return "", "", errors.New(fmt.Sprint(he.Message))
			}
			return "", "", err
		}
		for _, p := range s.playbacks.List() {
			if p.DeviceID == target.ID && p.ItemID == item.ID {
				return "", "", nil
			}
		}
		if states, err := s.store.PlayStatesByLibrary(ctx, owner.ID, item.LibraryID); err == nil {
			if ps, ok := states[item.ID]; ok {
				item.Watched, item.PositionSecs, item.WatchedAt = ps.Watched, ps.PositionSecs, ps.UpdatedAt
			}
		}
		s.events.Publish(events.Event{Type: "handoff", User: owner.ID, Session: target.ID,
			Data: handoff{From: modeName, handoffQueue: handoffQueue{Items: []store.Item{item}}}})
		return target.ID, deviceLabel(target), nil
	}
}

// ---- playback events ----

// firePlayback tells Home Assistant about a playback, when it is listening.
func (s *server) firePlayback(event string, p playback.Playback) {
	if !s.ha.PlaybackEventsOn() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		data := map[string]any{
			"user": p.UserID, "device": "", "device_id": p.DeviceID, "area_id": "",
			"item_id": p.ItemID, "title": p.Title, "subtitle": p.Subtitle, "kind": p.Kind,
			"position_secs": p.PositionSecs, "duration_secs": p.DurationSecs,
		}
		if u, err := s.store.UserByID(ctx, p.UserID); err == nil {
			data["user"] = u.Username
		}
		if p.DeviceID != "" {
			// The device, not its session: a stopped event can come after
			// its session ends.
			if d, err := s.store.Device(ctx, p.DeviceID); err == nil {
				data["device"], data["area_id"] = deviceName(d), d.AreaID
			}
		}
		s.ha.FirePlayback(event, data)
	}()
}

func (s *server) onPlaybackPause(p playback.Playback) {
	// Devices list what each plays and whether it is paused.
	s.events.Publish(events.Event{Type: "devices", User: p.UserID})
	if p.Paused {
		s.firePlayback(homeassistant.PlaybackPaused, p)
	} else {
		s.firePlayback(homeassistant.PlaybackResumed, p)
	}
}
