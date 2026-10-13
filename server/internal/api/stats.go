package api

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/stats"
)

// GET /stats/system?range=1h|24h — CPU/RAM history for the admin dashboard.
func (s *server) systemStats(c echo.Context) error {
	day := c.QueryParam("range") == "24h"
	hist := s.sampler.History(day)
	if hist == nil {
		hist = []stats.Sample{}
	}
	return c.JSON(http.StatusOK, hist)
}

// GET /stats/plays?days=30 — plays per day by media kind.
func (s *server) playStats(c echo.Context) error {
	days, _ := strconv.Atoi(c.QueryParam("days"))
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	out, err := s.store.PlaysByDay(c.Request().Context(), days)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// GET /stats/plays/totals?days=30 — how much was actually watched.
//
// Separate from /stats/plays rather than folded into it: that endpoint
// returns a bare array and the contract is that a response shape does not
// change within a version.
func (s *server) playTotals(c echo.Context) error {
	days, _ := strconv.Atoi(c.QueryParam("days"))
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	out, err := s.store.PlayTotals(c.Request().Context(), days)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}
