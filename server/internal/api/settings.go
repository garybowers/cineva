package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/segments"
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
	// settingDetectionMode says when detection may read: "window" (only
	// between the times in settingDetectionWindow, "HH:MM-HH:MM"), "idle"
	// (whenever nothing is playing or scanning) or "manual" (only when an
	// admin starts it). The overnight window is the default: playback comes
	// first, and analysis is never urgent.
	settingDetectionMode   = "segments.mode"
	settingDetectionWindow = "segments.window"
)

// defaultDetectionRateMB caps detection's reads unless an admin chooses
// otherwise: fast enough to get through a backlog overnight, slow enough
// that nothing else on the share notices.
const defaultDetectionRateMB = 10

const defaultDetectionWindow = "02:00-06:00"

var detectionModes = []string{segments.ModeWindow, segments.ModeIdle, segments.ModeManual}

// parseWindow reads "HH:MM-HH:MM" into minutes after midnight.
func parseWindow(v string) (start, end int, err error) {
	from, to, ok := strings.Cut(v, "-")
	if !ok {
		return 0, 0, fmt.Errorf("window %q: want HH:MM-HH:MM", v)
	}
	if start, err = parseClock(from); err != nil {
		return 0, 0, err
	}
	if end, err = parseClock(to); err != nil {
		return 0, 0, err
	}
	return start, end, nil
}

func parseClock(v string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("time %q: want HH:MM", v)
	}
	return t.Hour()*60 + t.Minute(), nil
}

func formatClock(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// detectionSchedule reads when detection may run, falling back to the
// overnight window for anything missing or malformed.
func (s *server) detectionSchedule(ctx context.Context) segments.Schedule {
	mode, err := s.store.Setting(ctx, settingDetectionMode, segments.ModeWindow)
	if err != nil || !slices.Contains(detectionModes, mode) {
		mode = segments.ModeWindow
	}
	window, err := s.store.Setting(ctx, settingDetectionWindow, defaultDetectionWindow)
	if err != nil {
		window = defaultDetectionWindow
	}
	start, end, err := parseWindow(window)
	if err != nil {
		start, end, _ = parseWindow(defaultDetectionWindow)
	}
	return segments.Schedule{Mode: mode, Start: start, End: end}
}

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
	// DetectionMode is when detection may read — "window", "idle" or
	// "manual" — and the window, local time, applies to "window".
	DetectionMode        string `json:"detectionMode"`
	DetectionWindowStart string `json:"detectionWindowStart"`
	DetectionWindowEnd   string `json:"detectionWindowEnd"`
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
	v, err := s.store.Setting(ctx, settingDetectionRate, strconv.Itoa(defaultDetectionRateMB))
	if err != nil {
		return defaultDetectionRateMB
	}
	n, err := strconv.Atoi(v)
	if err != nil || !slices.Contains(detectionRates, n) {
		return defaultDetectionRateMB
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
	sched := s.detectionSchedule(c.Request().Context())
	return c.JSON(http.StatusOK, settingsResponse{
		LoginMode:     s.loginMode(c),
		HWAccel:       s.hwAccelEnabled(c),
		HWAccelStatus: s.sessions.HW.Name(),
		HWCodecs:      s.sessions.HW.Codecs(),
		CertRegion:    s.certRegion(c),
		CertRegions:   certRegions,

		DetectionRateMB:      s.detectionRateMB(c.Request().Context()),
		DetectionRateOptions: detectionRates,
		DetectionMode:        sched.Mode,
		DetectionWindowStart: formatClock(sched.Start),
		DetectionWindowEnd:   formatClock(sched.End),
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
		// "window", "idle" or "manual", and the window as "HH:MM".
		DetectionMode        *string `json:"detectionMode"`
		DetectionWindowStart *string `json:"detectionWindowStart"`
		DetectionWindowEnd   *string `json:"detectionWindowEnd"`
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
	if req.DetectionMode != nil || req.DetectionWindowStart != nil || req.DetectionWindowEnd != nil {
		ctx := c.Request().Context()
		sched := s.detectionSchedule(ctx)
		if req.DetectionMode != nil {
			if !slices.Contains(detectionModes, *req.DetectionMode) {
				return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("detectionMode must be one of %v", detectionModes))
			}
			sched.Mode = *req.DetectionMode
		}
		for _, f := range []struct {
			v   *string
			dst *int
		}{{req.DetectionWindowStart, &sched.Start}, {req.DetectionWindowEnd, &sched.End}} {
			if f.v == nil {
				continue
			}
			m, err := parseClock(*f.v)
			if err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, err.Error())
			}
			*f.dst = m
		}
		window := formatClock(sched.Start) + "-" + formatClock(sched.End)
		if err := s.store.SetSetting(ctx, settingDetectionMode, sched.Mode); err != nil {
			return err
		}
		if err := s.store.SetSetting(ctx, settingDetectionWindow, window); err != nil {
			return err
		}
		if s.segments != nil {
			s.segments.SetSchedule(sched)
		}
		s.audit(c, "settings.update", "settings", settingDetectionMode, "Detection schedule", sched.Mode+" "+window)
	}
	s.events.Publish(events.Event{Type: "settings"})
	return s.getSettings(c)
}
