package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/store"
)

// PUT /items/:id/rating — the viewer's star rating of an item, 1 to 5, or 0
// to clear it. Their own, like a resume position.
func (s *server) putRating(c echo.Context) error {
	item, err := s.playableItem(c)
	if err != nil {
		return err
	}
	var req struct {
		Stars *int `json:"stars"`
	}
	if err := c.Bind(&req); err != nil || req.Stars == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "stars must be a whole number from 0 to 5")
	}
	uid := userID(c)
	if err := s.store.SetRating(c.Request().Context(), uid, item.ID, *req.Stars); err != nil {
		if errors.Is(err, store.ErrInvalidRating) {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return err
	}
	// The viewer's other screens show the same stars.
	s.events.Publish(events.Event{Type: "rating", User: uid, Data: map[string]any{"itemId": item.ID, "stars": *req.Stars}})
	return c.NoContent(http.StatusNoContent)
}

// withRatings stamps the viewer's ratings onto items from anywhere: a
// queue, a row of most played tracks. Lists from one library go through
// withPlayState, which does this too. Undecorated on failure, like it.
func (s *server) withRatings(c echo.Context, items []store.Item) []store.Item {
	uid := userID(c)
	if uid == "" || len(items) == 0 {
		return items
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	ratings, err := s.store.RatingsFor(c.Request().Context(), uid, ids)
	if err != nil {
		return items
	}
	for i := range items {
		items[i].UserRating = ratings[items[i].ID]
	}
	return items
}
