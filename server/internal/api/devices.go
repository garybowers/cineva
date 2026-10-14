package api

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/store"
)

// A signed-in browser session is a device: it can be listed, named,
// signed out remotely, and handed a play queue or have its queue taken.

// presence counts each session's open event streams. A device is online
// while it has one, which is what makes it a target for "Play on…".
type presence struct {
	mu sync.Mutex
	n  map[string]int
}

func newPresence() *presence { return &presence{n: map[string]int{}} }

func (p *presence) connect(sessionID string) {
	p.mu.Lock()
	p.n[sessionID]++
	p.mu.Unlock()
}

func (p *presence) disconnect(sessionID string) {
	p.mu.Lock()
	if p.n[sessionID]--; p.n[sessionID] <= 0 {
		delete(p.n, sessionID)
	}
	p.mu.Unlock()
}

func (p *presence) online(sessionID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n[sessionID] > 0
}

// device is a session as its owner (or an admin) sees it.
type device struct {
	store.Session
	Current bool          `json:"current"`
	Online  bool          `json:"online"`
	Queue   *queueSummary `json:"queue,omitempty"`
	Playing *nowPlaying   `json:"playing,omitempty"`
}

// nowPlaying is what a device is playing, as its last heartbeat put it, for
// another device to offer "Continue here".
type nowPlaying struct {
	Item         store.Item `json:"item"`
	SeriesTitle  string     `json:"seriesTitle,omitempty"`
	PositionSecs int        `json:"positionSecs"`
	DurationSecs int        `json:"durationSecs"`
	Paused       bool       `json:"paused"`
	// ReportedAt is when the position was reported, so a client can move
	// it on while the title plays.
	ReportedAt time.Time `json:"reportedAt"`
}

// playingOn is what a user's device is playing, or nil.
func (s *server) playingOn(c echo.Context, owner store.User, deviceID string) *nowPlaying {
	p, ok := s.playbacks.OnDevice(owner.ID, deviceID)
	if !ok {
		return nil
	}
	it, err := s.store.Item(c.Request().Context(), p.ItemID)
	if err != nil || !canSeeLibrary(owner, it.LibraryID) {
		return nil
	}
	return &nowPlaying{Item: it, SeriesTitle: s.seriesTitleOf(c, it), PositionSecs: p.PositionSecs,
		DurationSecs: p.DurationSecs, Paused: p.Paused, ReportedAt: p.LastSeenAt}
}

// queueSummary says enough about a device's queue to choose it: how long it
// is and what it is on.
type queueSummary struct {
	Count       int         `json:"count"`
	Index       int         `json:"index"`
	Current     *store.Item `json:"current,omitempty"`
	SeriesTitle string      `json:"seriesTitle,omitempty"`
	UpdatedAt   string      `json:"updatedAt"`
}

// deviceLabel names a device in messages: its chosen name, else its browser.
func deviceLabel(sess store.Session) string {
	if sess.Name != "" {
		return sess.Name
	}
	return sess.Client
}

// deviceName is deviceLabel for a device nobody need be signed in on.
func deviceName(d store.Device) string {
	if d.Name != "" {
		return d.Name
	}
	return d.Client
}

func (s *server) devicesFor(c echo.Context, owner store.User) ([]device, error) {
	ctx := c.Request().Context()
	sessions, err := s.store.UserSessions(ctx, owner.ID)
	if err != nil {
		return nil, err
	}
	current, _ := c.Get(ctxSessionID).(string)
	out := make([]device, 0, len(sessions))
	for _, sess := range sessions {
		d := device{Session: sess, Current: sess.ID == current, Online: s.presence.online(sess.ID)}
		q, err := sess.ParsedQueue()
		if err != nil {
			return nil, err
		}
		if len(q.ItemIDs) > 0 {
			sum := &queueSummary{Count: len(q.ItemIDs), Index: q.Index, UpdatedAt: sess.QueueUpdatedAt}
			if it, err := s.store.Item(ctx, q.ItemIDs[q.Index]); err == nil && canSeeLibrary(owner, it.LibraryID) {
				sum.Current = &it
				sum.SeriesTitle = s.seriesTitleOf(c, it)
			}
			d.Queue = sum
		}
		d.Playing = s.playingOn(c, owner, sess.ID)
		out = append(out, d)
	}
	return out, nil
}

// seriesTitleOf names an episode's series ("Episode 3" alone says little),
// ” for anything else.
func (s *server) seriesTitleOf(c echo.Context, it store.Item) string {
	if it.Kind != store.KindEpisode || it.ParentID == "" {
		return ""
	}
	ctx := c.Request().Context()
	season, err := s.store.Item(ctx, it.ParentID)
	if err != nil || season.ParentID == "" {
		return ""
	}
	series, err := s.store.Item(ctx, season.ParentID)
	if err != nil {
		return ""
	}
	return series.Title
}

// GET /auth/sessions — the caller's signed-in devices.
func (s *server) listDevices(c echo.Context) error {
	out, err := s.devicesFor(c, currentUser(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// currentSessionHash is the caller's own session, or a 400 for a request
// made with an API key, which belongs to no device.
func currentSessionHash(c echo.Context) (string, error) {
	hash, ok := c.Get(ctxSession).(string)
	if !ok || hash == "" {
		return "", echo.NewHTTPError(http.StatusBadRequest, "only a signed-in browser has a device session")
	}
	return hash, nil
}

// PATCH /auth/sessions/current {name, areaId} — name this device, or put
// it in a Home Assistant room. Fields left out are kept.
func (s *server) nameDevice(c echo.Context) error {
	hash, err := currentSessionHash(c)
	if err != nil {
		return err
	}
	var req struct {
		Name   *string `json:"name"`
		AreaID *string `json:"areaId"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if utf8.RuneCountInString(name) > 40 {
			return echo.NewHTTPError(http.StatusBadRequest, "a device name is at most 40 characters")
		}
		if err := s.store.SetSessionName(ctx, hash, name); err != nil {
			return err
		}
	}
	if req.AreaID != nil {
		if err := s.validArea(*req.AreaID); err != nil {
			return err
		}
		id, _ := c.Get(ctxSessionID).(string)
		if err := s.store.SetDeviceArea(ctx, id, *req.AreaID); err != nil {
			return err
		}
		s.events.Publish(events.Event{Type: "homeassistant", User: userID(c)})
	}
	s.events.Publish(events.Event{Type: "devices", User: userID(c)})
	return c.NoContent(http.StatusNoContent)
}

// DELETE /auth/sessions/:id — sign one of the caller's other devices out,
// or with ?forget=true forget a device altogether (this one included).
func (s *server) revokeDevice(c echo.Context) error {
	if current, _ := c.Get(ctxSessionID).(string); c.Param("id") == current && c.QueryParam("forget") != "true" {
		return echo.NewHTTPError(http.StatusBadRequest, "use sign out for this device")
	}
	return s.revokeSession(c, currentUser(c), c.Param("id"))
}

// revokeSession signs a device out and closes its event stream, so it
// finds out now rather than at its next request. The device stays known,
// with its name, room and preferences, unless ?forget=true, which removes
// it: owner must be the one signed in on it.
func (s *server) revokeSession(c echo.Context, owner store.User, id string) error {
	ctx := c.Request().Context()
	sess, err := s.store.UserSession(ctx, owner.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	} else if err != nil {
		return err
	}
	forget := c.QueryParam("forget") == "true"
	if forget {
		err = s.store.DeleteDevice(ctx, id)
	} else {
		err = s.store.DeleteUserSession(ctx, owner.ID, id)
	}
	if err != nil {
		return err
	}
	s.events.Publish(events.Event{Type: "revoked", User: owner.ID, Session: id})
	s.events.Publish(events.Event{Type: "devices", User: owner.ID})
	action := "session.revoke"
	if forget {
		action = "device.forget"
		s.events.Publish(events.Event{Type: "homeassistant"})
	}
	s.audit(c, action, "user", owner.ID, owner.Username, deviceLabel(sess))
	return c.NoContent(http.StatusNoContent)
}

// POST /auth/sessions/revoke-others — sign out everywhere but here.
func (s *server) revokeOtherDevices(c echo.Context) error {
	hash, err := currentSessionHash(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	me := currentUser(c)
	sessions, err := s.store.UserSessions(ctx, me.ID)
	if err != nil {
		return err
	}
	n, err := s.store.DeleteOtherSessions(ctx, me.ID, hash)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if sess.TokenHash != hash {
			s.events.Publish(events.Event{Type: "revoked", User: me.ID, Session: sess.ID})
		}
	}
	s.events.Publish(events.Event{Type: "devices", User: me.ID})
	s.audit(c, "session.revoke", "user", me.ID, me.Username, "every other device")
	return c.JSON(http.StatusOK, map[string]int{"revoked": n})
}

// PUT /auth/sessions/current/queue {itemIds, index} — this device's play
// queue, kept so another device can take it over.
func (s *server) saveDeviceQueue(c echo.Context) error {
	hash, err := currentSessionHash(c)
	if err != nil {
		return err
	}
	var q store.SessionQueue
	if err := c.Bind(&q); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if len(q.ItemIDs) > store.MaxQueueItems {
		return echo.NewHTTPError(http.StatusBadRequest, "a queue holds at most 500 items")
	}
	if err := s.store.SetSessionQueue(c.Request().Context(), hash, q); err != nil {
		return err
	}
	s.events.Publish(events.Event{Type: "devices", User: userID(c)})
	return c.NoContent(http.StatusNoContent)
}

// handoffQueue is a queue on its way between devices, with its items
// resolved for the receiving one.
type handoffQueue struct {
	Items []store.Item `json:"items"`
	Index int          `json:"index"`
}

// resolveQueue turns stored ids into the items the viewer may still see,
// carrying their watch state, and keeps the index on the same item when
// some have dropped out.
func (s *server) resolveQueue(c echo.Context, q store.SessionQueue) (handoffQueue, error) {
	out := handoffQueue{Items: []store.Item{}}
	items, err := s.store.ItemsByIDs(c.Request().Context(), q.ItemIDs)
	if err != nil {
		return out, err
	}
	me := currentUser(c)
	byLibrary := map[string][]int{}
	for _, it := range items {
		if !canSeeLibrary(me, it.LibraryID) {
			continue
		}
		if len(q.ItemIDs) > 0 && q.Index < len(q.ItemIDs) && it.ID == q.ItemIDs[q.Index] {
			out.Index = len(out.Items)
		}
		byLibrary[it.LibraryID] = append(byLibrary[it.LibraryID], len(out.Items))
		out.Items = append(out.Items, it)
	}
	for lib, idx := range byLibrary {
		group := make([]store.Item, len(idx))
		for i, j := range idx {
			group[i] = out.Items[j]
		}
		group = s.withPlayState(c, lib, group)
		for i, j := range idx {
			out.Items[j] = group[i]
		}
	}
	return out, nil
}

// GET /auth/sessions/:id/queue — another device's queue, to take over.
func (s *server) deviceQueue(c echo.Context) error {
	sess, err := s.store.UserSession(c.Request().Context(), userID(c), c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	} else if err != nil {
		return err
	}
	q, err := sess.ParsedQueue()
	if err != nil {
		return err
	}
	out, err := s.resolveQueue(c, q)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// takeover is what POST /auth/sessions/:id/takeover answers: what to play
// here, from where the other device left it.
type takeover struct {
	handoffQueue
	// PositionSecs is where the other device had got to in the first item.
	// A film or episode resumes there from its saved state anyway; music
	// keeps no resume state, so the client starts the track here.
	PositionSecs int    `json:"positionSecs"`
	From         string `json:"from"`
}

// How long a takeover waits for the other device to pause and say where it
// stopped, before going with its last heartbeat (up to 15 seconds behind).
const takeoverWait = 3 * time.Second

// POST /auth/sessions/:id/takeover — continue here what another of the
// caller's devices is playing. That device is told to pause (a "takeover"
// event naming this one) and report where it got to; the answer is the
// queue to play from there: the other device's queue when it is on it, else
// the title and, for a track, the rest of its album.
func (s *server) takeOverDevice(c echo.Context) error {
	ctx := c.Request().Context()
	me := currentUser(c)
	source, err := s.store.UserSession(ctx, me.ID, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	} else if err != nil {
		return err
	}
	here, _ := c.Get(ctxSessionID).(string)
	if source.ID == here {
		return echo.NewHTTPError(http.StatusBadRequest, "that is this device")
	}
	p, ok := s.playbacks.OnDevice(me.ID, source.ID)
	if !ok {
		return echo.NewHTTPError(http.StatusConflict, "nothing is playing on "+deviceLabel(source))
	}
	to := "another device"
	if sess, err := s.store.UserSession(ctx, me.ID, here); err == nil {
		to = deviceLabel(sess)
	}
	if s.presence.online(source.ID) {
		asked := time.Now().UTC()
		s.events.Publish(events.Event{Type: "takeover", User: me.ID, Session: source.ID,
			Data: map[string]string{"to": to, "itemId": p.ItemID}})
		// Its pause comes back as a heartbeat; until then the last one is
		// the best there is.
		deadline := time.NewTimer(takeoverWait)
		defer deadline.Stop()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
	wait:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				break wait
			case <-tick.C:
				now, ok := s.playbacks.Get(me.ID, p.ItemID)
				if !ok {
					break wait // it stopped instead
				}
				p = now
				if now.Paused && now.LastSeenAt.After(asked) {
					break wait
				}
			}
		}
	}
	item, err := s.store.Item(ctx, p.ItemID)
	if err != nil || !canSeeLibrary(me, item.LibraryID) {
		return echo.NewHTTPError(http.StatusNotFound, "what "+deviceLabel(source)+" is playing cannot be played here")
	}
	q := store.SessionQueue{ItemIDs: []string{item.ID}}
	if sq, err := source.ParsedQueue(); err == nil && sq.Index >= 0 && sq.Index < len(sq.ItemIDs) && sq.ItemIDs[sq.Index] == item.ID {
		q = sq
	} else if item.Kind == store.KindTrack && item.ParentID != "" {
		if tracks, err := s.store.ItemsByParent(ctx, item.ParentID); err == nil {
			q.ItemIDs = q.ItemIDs[:0]
			for _, t := range tracks {
				if t.Kind == store.KindTrack {
					q.ItemIDs = append(q.ItemIDs, t.ID)
				}
				if t.ID == item.ID {
					q.Index = len(q.ItemIDs) - 1
				}
			}
		}
	}
	resolved, err := s.resolveQueue(c, q)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, takeover{handoffQueue: resolved, PositionSecs: p.PositionSecs, From: deviceLabel(source)})
}

// handoff is the "handoff" event's payload: a queue sent to a device.
type handoff struct {
	From string `json:"from"`
	handoffQueue
}

// POST /auth/sessions/:id/play {itemIds, index} — send a queue to another
// of the caller's devices, which starts playing it. Only an online device
// can receive one; the sender stops its own playback once this succeeds.
func (s *server) playOnDevice(c echo.Context) error {
	ctx := c.Request().Context()
	me := currentUser(c)
	target, err := s.store.UserSession(ctx, me.ID, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	} else if err != nil {
		return err
	}
	if current, _ := c.Get(ctxSessionID).(string); target.ID == current {
		return echo.NewHTTPError(http.StatusBadRequest, "that is this device")
	}
	if !s.presence.online(target.ID) {
		return echo.NewHTTPError(http.StatusConflict, deviceLabel(target)+" is not connected — open Cineva on it first")
	}
	var q store.SessionQueue
	if err := c.Bind(&q); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if len(q.ItemIDs) == 0 || len(q.ItemIDs) > store.MaxQueueItems {
		return echo.NewHTTPError(http.StatusBadRequest, "send between 1 and 500 items")
	}
	resolved, err := s.resolveQueue(c, q)
	if err != nil {
		return err
	}
	if len(resolved.Items) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "none of those items can be played")
	}
	from := "another device"
	if hash, ok := c.Get(ctxSession).(string); ok {
		if sess, _, err := s.store.SessionByHash(ctx, hash); err == nil {
			from = deviceLabel(sess)
		}
	}
	s.events.Publish(events.Event{Type: "handoff", User: me.ID, Session: target.ID,
		Data: handoff{From: from, handoffQueue: resolved}})
	return c.NoContent(http.StatusAccepted)
}

// GET /users/:id/sessions — an admin's view of a user's devices.
func (s *server) userDevices(c echo.Context) error {
	u, err := s.store.UserByID(c.Request().Context(), c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	out, err := s.devicesFor(c, u)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// DELETE /users/:id/sessions/:sid — an admin signs one of a user's devices out.
func (s *server) revokeUserDevice(c echo.Context) error {
	u, err := s.store.UserByID(c.Request().Context(), c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	return s.revokeSession(c, u, c.Param("sid"))
}

// ---- every device (admin) ----

// adminDevice is a device as the admin's Devices page shows it.
type adminDevice struct {
	store.Device
	// UserName is whoever is signed in on it now.
	UserName string `json:"userName"`
	Online   bool   `json:"online"`
}

// GET /devices (admin) — every screen, signed in on or not.
func (s *server) listAllDevices(c echo.Context) error {
	ctx := c.Request().Context()
	devices, err := s.store.Devices(ctx)
	if err != nil {
		return err
	}
	users, err := s.store.Users(ctx)
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, u := range users {
		names[u.ID] = u.DisplayName
		if names[u.ID] == "" {
			names[u.ID] = u.Username
		}
	}
	out := make([]adminDevice, 0, len(devices))
	for _, d := range devices {
		out = append(out, adminDevice{Device: d, UserName: names[d.UserID], Online: s.presence.online(d.ID)})
	}
	return c.JSON(http.StatusOK, out)
}

// PATCH /devices/:id (admin) {name, areaId, autoUserId} — fields left out
// are kept. A device signs in by itself only as a user who is neither an
// admin nor disabled: whoever sits in front of the TV gets that account.
func (s *server) updateDevice(c echo.Context) error {
	ctx := c.Request().Context()
	d, err := s.store.Device(ctx, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "device not found")
	} else if err != nil {
		return err
	}
	var req struct {
		Name       *string `json:"name"`
		AreaID     *string `json:"areaId"`
		AutoUserID *string `json:"autoUserId"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if utf8.RuneCountInString(name) > 40 {
			return echo.NewHTTPError(http.StatusBadRequest, "a device name is at most 40 characters")
		}
		if err := s.store.SetDeviceName(ctx, d.ID, name); err != nil {
			return err
		}
	}
	if req.AreaID != nil {
		if err := s.validArea(*req.AreaID); err != nil {
			return err
		}
		if err := s.store.SetDeviceArea(ctx, d.ID, *req.AreaID); err != nil {
			return err
		}
		s.events.Publish(events.Event{Type: "homeassistant"})
	}
	if req.AutoUserID != nil {
		detail := "shows the sign-in screen"
		if id := *req.AutoUserID; id != "" {
			u, err := s.store.UserByID(ctx, id)
			if err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "no such user")
			}
			if u.IsAdmin() {
				return echo.NewHTTPError(http.StatusBadRequest,
					"a device cannot sign in by itself as an admin: anyone in front of it would have the admin pages")
			}
			if u.Disabled {
				return echo.NewHTTPError(http.StatusBadRequest, u.Username+" is disabled")
			}
			detail = "signs in as " + u.Username
		}
		if err := s.store.SetDeviceAutoUser(ctx, d.ID, *req.AutoUserID); err != nil {
			return err
		}
		s.audit(c, "device.autouser", "device", d.ID, deviceName(d), detail)
	}
	if d.UserID != "" {
		s.events.Publish(events.Event{Type: "devices", User: d.UserID})
	}
	updated, err := s.store.Device(ctx, d.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, updated)
}

// DELETE /devices/:id (admin) — forget a device: its name, room and
// preferences go, and whoever is signed in on it is signed out.
func (s *server) forgetDevice(c echo.Context) error {
	ctx := c.Request().Context()
	d, err := s.store.Device(ctx, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "device not found")
	} else if err != nil {
		return err
	}
	if err := s.store.DeleteDevice(ctx, d.ID); err != nil {
		return err
	}
	if d.UserID != "" {
		s.events.Publish(events.Event{Type: "revoked", User: d.UserID, Session: d.ID})
		s.events.Publish(events.Event{Type: "devices", User: d.UserID})
	}
	s.events.Publish(events.Event{Type: "homeassistant"})
	s.audit(c, "device.forget", "device", d.ID, deviceName(d), "")
	return c.NoContent(http.StatusNoContent)
}
