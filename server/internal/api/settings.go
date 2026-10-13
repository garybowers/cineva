package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
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

	// settingDetectionPaused holds intro detection until resumed;
	// settingDetectionRate caps its reads in MB/s, "0" for none. Detection
	// reads a quarter of every episode, which at full speed can fill the
	// link to a NAS and starve anything else using it.
	settingDetectionPaused = "segments.paused"
	settingDetectionRate   = "segments.rate_mb"
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
	// DetectionRateMB caps intro detection's reads, in MB/s; 0 is no cap.
	DetectionRateMB      int   `json:"detectionRateMB"`
	DetectionRateOptions []int `json:"detectionRateOptions"`
	// HWCodecs is what the detected device can encode, h264 first. Shown so
	// an operator can see what their hardware offers; h264 remains the
	// output, being the one codec every browser decodes.
	HWCodecs []string `json:"hwCodecs,omitempty"`
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

// detectionRates are the caps offered for intro detection's reads, in MB/s.
var detectionRates = []int{0, 50, 25, 10}

func (s *server) detectionRateMB(ctx context.Context) int {
	v, err := s.store.Setting(ctx, settingDetectionRate, "0")
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || !slices.Contains(detectionRates, n) {
		return 0
	}
	return n
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
		HWCodecs:      s.sessions.HW.Codecs(),
		CertRegion:    s.certRegion(c),
		CertRegions:   certRegions,

		DetectionRateMB:      s.detectionRateMB(c.Request().Context()),
		DetectionRateOptions: detectionRates,
	})
}

// PUT /settings (admin) — only the fields sent are changed.
func (s *server) putSettings(c echo.Context) error {
	var req struct {
		LoginMode  *string `json:"loginMode"`
		HWAccel    *bool   `json:"hwAccel"`
		CertRegion *string `json:"certRegion"`
		// MB/s, one of detectionRates; 0 removes the cap.
		DetectionRateMB *int `json:"detectionRateMB"`
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
	if req.DetectionRateMB != nil {
		mb := *req.DetectionRateMB
		if !slices.Contains(detectionRates, mb) {
			return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("detectionRateMB must be one of %v", detectionRates))
		}
		if err := s.store.SetSetting(c.Request().Context(), settingDetectionRate, strconv.Itoa(mb)); err != nil {
			return err
		}
		if s.segments != nil {
			s.segments.SetRateLimit(int64(mb) * 1_000_000)
		}
		label := "unlimited"
		if mb > 0 {
			label = strconv.Itoa(mb) + " MB/s"
		}
		s.audit(c, "settings.update", "settings", settingDetectionRate, "Detection read speed", label)
	}
	s.events.Publish(events.Event{Type: "settings"})
	return s.getSettings(c)
}
