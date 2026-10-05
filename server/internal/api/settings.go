package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
)

// Runtime settings (admin UI). Each has a config-file default and a
// database override.
const (
	settingLoginMode = "auth.login_mode"
	LoginModePicker  = "picker" // "Who's watching?" tiles for every user
	LoginModeForm    = "form"   // username + password-or-PIN box

	// Hardware transcoding is on by default: when no GPU is usable the
	// detector falls back to software anyway, so the safe default is also
	// the fast one. The switch exists to rule hardware out when a driver
	// misbehaves, without editing config and restarting.
	settingHWAccel = "transcode.hwaccel_enabled"

	// Content ratings are issued per country by different boards, so there
	// is no single "the" certification for a film. This picks whose rating
	// to store and show. Changing it affects titles scanned or refreshed
	// afterwards — existing rows keep the rating they were given, so
	// changing region wants a metadata refresh to take full effect.
	settingCertRegion = "metadata.cert_region"
)

// certRegions are the regions offered in the admin UI. TMDB carries ratings
// for many more; these are the ones with good coverage and a board whose
// scale is widely recognised.
var certRegions = []string{"GB", "US", "IE", "AU", "NZ", "CA", "DE", "FR", "NL", "ES", "IT", "SE", "NO", "DK", "FI"}

// defaultCertRegion is the fallback when neither config nor the database
// says otherwise. GB because this is a British-English project; it is a
// default, not an assumption — the admin setting exists precisely because
// the right answer depends on where the server is.
const defaultCertRegion = "GB"

type settingsResponse struct {
	LoginMode string `json:"loginMode"`
	// HWAccel is the admin switch. HWAccelStatus is read-only: what the
	// detector actually found, so the switch is not just a mystery toggle
	// ("vaapi (/dev/dri/renderD128)", or "none" on a host with no GPU).
	HWAccel       bool   `json:"hwAccel"`
	HWAccelStatus string `json:"hwAccelStatus"`
	// CertRegion is the country whose content rating is stored and shown;
	// CertRegions is the list the UI offers.
	CertRegion  string   `json:"certRegion"`
	CertRegions []string `json:"certRegions"`
}

// certRegion reads the configured region, defaulting to GB.
func (s *server) certRegion(c echo.Context) string {
	v, err := s.store.Setting(c.Request().Context(), settingCertRegion, s.defaultCertRegion)
	if err != nil || v == "" {
		return defaultCertRegion
	}
	return v
}

// hwAccelEnabled reads the switch, defaulting to on.
func (s *server) hwAccelEnabled(c echo.Context) bool {
	v, err := s.store.Setting(c.Request().Context(), settingHWAccel, "true")
	if err != nil {
		return true
	}
	return v != "false"
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
	return c.JSON(http.StatusOK, settingsResponse{
		LoginMode:     s.loginMode(c),
		HWAccel:       s.hwAccelEnabled(c),
		HWAccelStatus: s.sessions.HW.Name(),
		CertRegion:    s.certRegion(c),
		CertRegions:   certRegions,
	})
}

// PUT /settings (admin) — only the fields sent are changed.
func (s *server) putSettings(c echo.Context) error {
	var req struct {
		LoginMode  *string `json:"loginMode"`
		HWAccel    *bool   `json:"hwAccel"`
		CertRegion *string `json:"certRegion"`
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
	if req.HWAccel != nil {
		v := "false"
		if *req.HWAccel {
			v = "true"
		}
		if err := s.store.SetSetting(c.Request().Context(), settingHWAccel, v); err != nil {
			return err
		}
		// Takes effect on the next session rather than at the next restart;
		// sessions already running keep the pipeline they were built with.
		s.sessions.SetHWEnabled(*req.HWAccel)
		s.audit(c, "settings.update", "settings", settingHWAccel, "Hardware acceleration", v)
	}
	if req.CertRegion != nil {
		region := strings.ToUpper(strings.TrimSpace(*req.CertRegion))
		if !slices.Contains(certRegions, region) {
			return echo.NewHTTPError(http.StatusBadRequest, "unsupported certification region "+region)
		}
		if err := s.store.SetSetting(c.Request().Context(), settingCertRegion, region); err != nil {
			return err
		}
		// The scanner reads this when it applies a provider result, so a
		// change takes effect on the next scan or metadata refresh without
		// a restart.
		s.manager.CertRegion = region
		s.audit(c, "settings.update", "settings", settingCertRegion, "Certification region", region)
	}
	s.events.Publish(events.Event{Type: "settings"})
	return s.getSettings(c)
}
