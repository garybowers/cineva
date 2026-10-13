package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/store"
)

// Library access: every library is open to every user unless an admin
// restricts it to chosen users and groups. Admins see everything.

// GET /libraries/:id/access
func (s *server) getLibraryAccess(c echo.Context) error {
	acc, err := s.store.LibraryAccess(c.Request().Context(), c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, acc)
}

// PUT /libraries/:id/access
func (s *server) setLibraryAccess(c echo.Context) error {
	ctx := c.Request().Context()
	lib, err := s.store.Library(ctx, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	var acc store.LibraryAccess
	if err := c.Bind(&acc); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := s.store.SetLibraryAccess(ctx, lib.ID, acc); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return err
	}
	detail := "everyone"
	if acc.Restricted {
		detail = fmt.Sprintf("restricted to %d user(s), %d group(s)", len(acc.UserIDs), len(acc.GroupIDs))
	}
	s.audit(c, "library.access", "library", lib.ID, lib.Name, detail)
	// Who can see what changed: every client refetches its library list,
	// and the admin its users (whose visible libraries are computed).
	s.events.Publish(events.Event{Type: "libraries"})
	s.events.Publish(events.Event{Type: "users"})
	updated, err := s.store.LibraryAccess(ctx, lib.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, updated)
}

type groupRequest struct {
	Name      string   `json:"name"`
	MemberIDs []string `json:"memberIds"`
}

// GET /groups
func (s *server) listGroups(c echo.Context) error {
	groups, err := s.store.Groups(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, groups)
}

// POST /groups
func (s *server) createGroup(c echo.Context) error {
	var req groupRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	g, err := s.store.CreateGroup(c.Request().Context(), req.Name, req.MemberIDs)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	s.audit(c, "group.create", "group", g.ID, g.Name, fmt.Sprintf("%d member(s)", len(g.MemberIDs)))
	s.events.Publish(events.Event{Type: "users"})
	return c.JSON(http.StatusCreated, g)
}

// PUT /groups/:id — rename and replace the members.
func (s *server) updateGroup(c echo.Context) error {
	var req groupRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	g, err := s.store.UpdateGroup(c.Request().Context(), c.Param("id"), req.Name, req.MemberIDs)
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "group not found")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	s.audit(c, "group.update", "group", g.ID, g.Name, fmt.Sprintf("%d member(s)", len(g.MemberIDs)))
	// Membership decides who sees restricted libraries.
	s.events.Publish(events.Event{Type: "users"})
	s.events.Publish(events.Event{Type: "libraries"})
	return c.JSON(http.StatusOK, g)
}

// DELETE /groups/:id — also drops the group from every library's access list.
func (s *server) deleteGroup(c echo.Context) error {
	ctx := c.Request().Context()
	if err := s.store.DeleteGroup(ctx, c.Param("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "group not found")
		}
		return err
	}
	s.audit(c, "group.delete", "group", c.Param("id"), "", "")
	s.events.Publish(events.Event{Type: "users"})
	s.events.Publish(events.Event{Type: "libraries"})
	return c.NoContent(http.StatusNoContent)
}
