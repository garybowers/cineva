package api

import (
	"errors"
	"net/http"
	"strings"
	"sync"
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

// PATCH /auth/sessions/current {name} — name this device.
func (s *server) nameDevice(c echo.Context) error {
	hash, err := currentSessionHash(c)
	if err != nil {
		return err
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	name := strings.TrimSpace(req.Name)
	if utf8.RuneCountInString(name) > 40 {
		return echo.NewHTTPError(http.StatusBadRequest, "a device name is at most 40 characters")
	}
	if err := s.store.SetSessionName(c.Request().Context(), hash, name); err != nil {
		return err
	}
	s.events.Publish(events.Event{Type: "devices", User: userID(c)})
	return c.NoContent(http.StatusNoContent)
}

// DELETE /auth/sessions/:id — sign one of the caller's other devices out.
func (s *server) revokeDevice(c echo.Context) error {
	if current, _ := c.Get(ctxSessionID).(string); c.Param("id") == current {
		return echo.NewHTTPError(http.StatusBadRequest, "use sign out for this device")
	}
	return s.revokeSession(c, currentUser(c), c.Param("id"))
}

// revokeSession signs a device out and closes its event stream, so it
// finds out now rather than at its next request.
func (s *server) revokeSession(c echo.Context, owner store.User, id string) error {
	ctx := c.Request().Context()
	sess, err := s.store.UserSession(ctx, owner.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	} else if err != nil {
		return err
	}
	if err := s.store.DeleteUserSession(ctx, owner.ID, id); err != nil {
		return err
	}
	s.events.Publish(events.Event{Type: "revoked", User: owner.ID, Session: id})
	s.events.Publish(events.Event{Type: "devices", User: owner.ID})
	s.audit(c, "session.revoke", "user", owner.ID, owner.Username, deviceLabel(sess))
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
