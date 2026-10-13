package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/avatar"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/store"
)

// avatarPrefix marks avatar files in the artwork directory, so replacing
// one never deletes anything else.
const avatarPrefix = "/artwork/avatar-"

// POST /auth/avatar (multipart "image") — upload the caller's picture.
func (s *server) uploadOwnAvatar(c echo.Context) error {
	return s.uploadAvatar(c, currentUser(c))
}

// DELETE /auth/avatar — back to the initial.
func (s *server) deleteOwnAvatar(c echo.Context) error {
	return s.clearAvatar(c, currentUser(c))
}

// PUT /auth/avatar/colour {colour} — the colour behind the initial.
func (s *server) setOwnAvatarColour(c echo.Context) error {
	var req struct {
		Colour string `json:"colour"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	me := currentUser(c)
	if err := s.store.SetAvatarColour(c.Request().Context(), me.ID, req.Colour); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return s.avatarChanged(c, me.ID)
}

// POST /users/:id/avatar — an admin sets someone's picture.
func (s *server) uploadUserAvatar(c echo.Context) error {
	u, err := s.store.UserByID(c.Request().Context(), c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	return s.uploadAvatar(c, u)
}

// DELETE /users/:id/avatar — an admin removes someone's picture.
func (s *server) deleteUserAvatar(c echo.Context) error {
	u, err := s.store.UserByID(c.Request().Context(), c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	return s.clearAvatar(c, u)
}

func (s *server) uploadAvatar(c echo.Context, u store.User) error {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, avatar.MaxBytes+1<<20) // room for the form around it
	fh, err := c.FormFile("image")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "send the picture as the multipart field \"image\" (at most 15 MB)")
	}
	f, err := fh.Open()
	if err != nil {
		return fmt.Errorf("open upload: %w", err)
	}
	defer f.Close()
	jpg, err := avatar.Process(f)
	if errors.Is(err, avatar.ErrNotImage) {
		return echo.NewHTTPError(http.StatusBadRequest, "that is not a JPEG, PNG, GIF or WebP picture")
	} else if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// A fresh name each time, so browsers holding the old picture fetch
	// the new one rather than trusting their cache.
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("avatar name: %w", err)
	}
	name := "avatar-" + u.ID + "-" + hex.EncodeToString(suffix) + ".jpg"
	dir := s.manager.ArtworkDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("artwork dir: %w", err)
	}
	tmp := filepath.Join(dir, name+".part")
	if err := os.WriteFile(tmp, jpg, 0o644); err != nil {
		return fmt.Errorf("write avatar: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("save avatar: %w", err)
	}
	if err := s.store.SetAvatar(r.Context(), u.ID, "/artwork/"+name); err != nil {
		return err
	}
	s.removeAvatarFile(u.Avatar)
	s.audit(c, "user.avatar", "user", u.ID, u.Username, "picture set")
	return s.avatarChanged(c, u.ID)
}

func (s *server) clearAvatar(c echo.Context, u store.User) error {
	if err := s.store.SetAvatar(c.Request().Context(), u.ID, ""); err != nil {
		return err
	}
	s.removeAvatarFile(u.Avatar)
	s.audit(c, "user.avatar", "user", u.ID, u.Username, "picture removed")
	return s.avatarChanged(c, u.ID)
}

// removeAvatarFile deletes a replaced picture. Best effort: a file left
// behind costs a few kilobytes, and the user's change has already landed.
func (s *server) removeAvatarFile(path string) {
	if !strings.HasPrefix(path, avatarPrefix) {
		return
	}
	file := filepath.Join(s.manager.ArtworkDir, filepath.Base(path))
	if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("avatar: remove %s: %v", file, err)
	}
}

// avatarChanged answers with the updated user and tells open screens: the
// profile picker and the admin's user list both draw avatars.
func (s *server) avatarChanged(c echo.Context, id string) error {
	u, err := s.store.UserByID(c.Request().Context(), id)
	if err != nil {
		return err
	}
	s.events.Publish(events.Event{Type: "users"})
	return c.JSON(http.StatusOK, u)
}
