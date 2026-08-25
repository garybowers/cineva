package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
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
	admin := currentUser(c).IsAdmin()
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
