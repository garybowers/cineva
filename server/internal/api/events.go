package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
)

// adminEvents carry server-wide information only admins may see.
var adminEvents = map[string]bool{"stats": true, "sessions": true, "logs": true, "users": true, "scan": true}

// eventStream is the /api/v1/events endpoint: a Server-Sent Events stream of
// state changes (scan progress, library changes) so UIs update live instead
// of polling. Events are advisory — the payload is a hint and clients refetch
// what they display — so a dropped or missed event only delays an update.
func (s *server) eventStream(c echo.Context) error {
	res := c.Response()
	res.Header().Set(echo.HeaderContentType, "text/event-stream")
	res.Header().Set(echo.HeaderCacheControl, "no-store")
	res.Header().Set("X-Accel-Buffering", "no") // reverse proxies must not buffer
	res.WriteHeader(http.StatusOK)
	res.Flush()

	ctx := c.Request().Context()
	me := currentUser(c)
	admin := me.IsAdmin()
	// A browser's stream is how its session counts as online: a device can
	// be handed a queue only while it is listening for it.
	sessionID, _ := c.Get(ctxSessionID).(string)
	if sessionID != "" {
		s.presence.connect(sessionID)
		authLog.Debug("device connected", "user", me.Username, "client", clientLabel(c), "ip", c.RealIP())
		defer authLog.Debug("device disconnected", "user", me.Username, "client", clientLabel(c), "ip", c.RealIP())
		s.events.Publish(events.Event{Type: "devices", User: me.ID})
		// Deferred in this order so the device is offline before the others
		// are told to look.
		defer s.events.Publish(events.Event{Type: "devices", User: me.ID})
		defer s.presence.disconnect(sessionID)
	}
	sub := s.events.Subscribe(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-sub:
			if !ok {
				return nil
			}
			if !admin && adminEvents[ev.Type] {
				continue
			}
			if (ev.User != "" && ev.User != me.ID) || (ev.Session != "" && ev.Session != sessionID) {
				continue
			}
			// A revoked device's stream ends here rather than staying open
			// on the credential it no longer has.
			if ev.Type == "revoked" && ev.Session == sessionID {
				if _, err := fmt.Fprint(res, "event: revoked\ndata: null\n\n"); err == nil {
					res.Flush()
				}
				return nil
			}
			data, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(res, "event: %s\ndata: %s\n\n", ev.Type, data); err != nil {
				return nil
			}
			res.Flush()
		}
	}
}
