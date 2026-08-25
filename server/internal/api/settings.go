package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
)

// Runtime settings (admin UI). Each has a config-file default and a
// database override.
const (
	settingLoginMode = "auth.login_mode"
	LoginModePicker  = "picker" // "Who's watching?" tiles for every user
	LoginModeForm    = "form"   // username + password-or-PIN box
)

type settingsResponse struct {
	LoginMode string `json:"loginMode"`
}

func (s *server) loginMode(c echo.Context) string {
	mode, err := s.store.Setting(c.Request().Context(), settingLoginMode, s.defaultLoginMode)
	if err != nil || (mode != LoginModePicker && mode != LoginModeForm) {
		return LoginModePicker
	}
	return mode
}

// GET /settings (admin)
func (s *server) getSettings(c echo.Context) error {
	return c.JSON(http.StatusOK, settingsResponse{LoginMode: s.loginMode(c)})
}

// PUT /settings (admin) — only the fields sent are changed.
func (s *server) putSettings(c echo.Context) error {
	var req struct {
		LoginMode *string `json:"loginMode"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if req.LoginMode != nil {
		if *req.LoginMode != LoginModePicker && *req.LoginMode != LoginModeForm {
			return echo.NewHTTPError(http.StatusBadRequest, "loginMode must be picker or form")
		}
		if err := s.store.SetSetting(c.Request().Context(), settingLoginMode, *req.LoginMode); err != nil {
			return err
		}
		s.audit(c, "settings.update", "settings", settingLoginMode, "Sign-in screen", *req.LoginMode)
	}
	s.events.Publish(events.Event{Type: "settings"})
	return s.getSettings(c)
}
