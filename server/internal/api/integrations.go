package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/arr"
	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/store"
)

var integrationsLog = logging.For("integrations")

// webhookUser is the username Cineva's webhooks authenticate with; the
// password is each connection's own secret.
const webhookUser = "cineva"

type arrRequest struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	APIKey      string `json:"apiKey"`
	CallbackURL string `json:"callbackUrl"`
}

// GET /integrations — connected Sonarr and Radarr instances.
func (s *server) listIntegrations(c echo.Context) error {
	conns, err := s.store.ArrConnections(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, conns)
}

// POST /integrations — connect an instance and register Cineva's webhook in
// it. Nothing is kept unless the instance accepts the webhook, which it
// tests by calling Cineva back.
func (s *server) createIntegration(c echo.Context) error {
	var req arrRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if !slices.Contains(arr.Kinds, req.Kind) {
		return echo.NewHTTPError(http.StatusBadRequest, "kind must be sonarr, radarr or lidarr")
	}
	conn := store.ArrConnection{Kind: req.Kind}
	if err := applyArrRequest(c, &conn, req); err != nil {
		return err
	}
	ctx := c.Request().Context()
	if err := s.uniqueIntegrationName(ctx, conn); err != nil {
		return err
	}
	client := arr.New(conn.Kind, conn.URL, conn.APIKey)
	st, err := client.Status(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	conn.Version = st.Version

	// The row goes in first: the instance tests the webhook before saving
	// it, and that test has to find the connection and its secret.
	secret, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	conn.SecretHash = hash
	conn, err = s.store.CreateArrConnection(ctx, conn)
	if err != nil {
		return err
	}
	id, err := client.RegisterWebhook(ctx, 0, s.webhookFor(conn, secret))
	if err != nil {
		if derr := s.store.DeleteArrConnection(context.WithoutCancel(ctx), conn.ID); derr != nil {
			integrationsLog.Error("undoing connection after a failed webhook registration failed", "connection", conn.Name, "err", derr)
		}
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	conn.NotificationID = id
	if conn, err = s.store.UpdateArrConnection(ctx, conn); err != nil {
		return err
	}
	s.audit(c, "integration.create", "integration", conn.ID, conn.Name, conn.URL)
	return c.JSON(http.StatusCreated, conn)
}

// PATCH /integrations/:id — change a connection and register the webhook
// again, which also repairs one deleted on the instance's side. A blank
// apiKey keeps the current one.
func (s *server) updateIntegration(c echo.Context) error {
	ctx := c.Request().Context()
	old, err := s.store.ArrConnection(ctx, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "integration not found")
	} else if err != nil {
		return err
	}
	var req arrRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	conn := old
	if err := applyArrRequest(c, &conn, req); err != nil {
		return err
	}
	if err := s.uniqueIntegrationName(ctx, conn); err != nil {
		return err
	}
	client := arr.New(conn.Kind, conn.URL, conn.APIKey)
	st, err := client.Status(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	conn.Version = st.Version

	// The secret is only ever held as a hash, so registering again means a
	// new one. It is saved before the instance tests it, and put back if
	// the instance refuses, so the working webhook keeps working.
	secret, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	conn.SecretHash = hash
	moved := strings.TrimRight(conn.URL, "/") != strings.TrimRight(old.URL, "/")
	if moved {
		conn.NotificationID = 0 // the old id belongs to the old instance
	}
	if conn, err = s.store.UpdateArrConnection(ctx, conn); err != nil {
		return err
	}
	id, err := client.RegisterWebhook(ctx, conn.NotificationID, s.webhookFor(conn, secret))
	if err != nil {
		if _, rerr := s.store.UpdateArrConnection(context.WithoutCancel(ctx), old); rerr != nil {
			integrationsLog.Error("restoring connection after a failed webhook registration failed", "connection", old.Name, "err", rerr)
		}
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	conn.NotificationID = id
	if conn, err = s.store.UpdateArrConnection(ctx, conn); err != nil {
		return err
	}
	if moved {
		// Best effort: the old instance may be gone for good, which is often
		// why the URL changed. Its webhook fails our check from now on.
		if err := arr.New(old.Kind, old.URL, old.APIKey).DeleteWebhook(ctx, old.NotificationID); err != nil {
			integrationsLog.Warn("removing webhook from the old instance failed", "url", old.URL, "err", err)
		}
	}
	s.audit(c, "integration.update", "integration", conn.ID, conn.Name, conn.URL)
	return c.JSON(http.StatusOK, conn)
}

// DELETE /integrations/:id — disconnect, removing the webhook from the
// instance. The connection goes even when the instance cannot be reached;
// the response then says so, since its webhook is left behind (where it
// fails Cineva's check and does nothing).
func (s *server) deleteIntegration(c echo.Context) error {
	ctx := c.Request().Context()
	conn, err := s.store.ArrConnection(ctx, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "integration not found")
	} else if err != nil {
		return err
	}
	cleanup := arr.New(conn.Kind, conn.URL, conn.APIKey).DeleteWebhook(ctx, conn.NotificationID)
	if err := s.store.DeleteArrConnection(ctx, conn.ID); err != nil {
		return err
	}
	s.audit(c, "integration.delete", "integration", conn.ID, conn.Name, conn.URL)
	if cleanup != nil {
		return c.JSON(http.StatusOK, map[string]string{
			"warning": "Disconnected, but the webhook could not be removed from " + conn.Name + " (" + cleanup.Error() + "). Delete the \"Cineva\" connection there by hand.",
		})
	}
	return c.NoContent(http.StatusNoContent)
}

// POST /integrations/:id/webhook — where Sonarr and Radarr deliver events.
// Public, because the caller is the app rather than a user; it signs in
// with HTTP basic auth using the connection's secret.
func (s *server) integrationWebhook(c echo.Context) error {
	ctx := c.Request().Context()
	conn, err := s.store.ArrConnection(ctx, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "integration not found")
	} else if err != nil {
		return err
	}
	user, pass, ok := c.Request().BasicAuth()
	if !ok || user != webhookUser || conn.SecretHash == "" ||
		subtle.ConstantTimeCompare([]byte(auth.HashToken(pass)), []byte(conn.SecretHash)) != 1 {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid webhook credentials")
	}
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, 1<<20))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "unreadable body")
	}
	ev, err := arr.ParseEvent(body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	// Every delivery is noted, Test included, for the connection's status
	// line: proof the webhook reaches us, and what came last.
	if err := s.store.RecordArrEvent(ctx, conn.ID, ev.Type, ev.Describe()); err != nil {
		integrationsLog.Warn("recording webhook event failed", "connection", conn.Name, "err", err)
	} else {
		s.events.Publish(events.Event{Type: "integrations"})
	}
	if ev.Grab() && s.requests != nil {
		if err := s.requests.Grabbed(ctx, conn, ev); err != nil {
			integrationsLog.Warn("updating requests for a grab failed", "connection", conn.Name, "err", err)
		}
	}
	if !ev.ChangesFiles() {
		// Test, Grab, Health and the rest: acknowledged, nothing to do.
		return c.JSON(http.StatusOK, map[string]any{"event": ev.Type, "rescanning": []string{}})
	}
	libs, err := s.store.Libraries(ctx)
	if err != nil {
		return err
	}
	rescanning := []string{}
	for _, lib := range arr.Libraries(libs, conn.Kind, arr.MapPaths(conn.PathMaps, ev.Paths)) {
		if err := s.manager.RequestScan(ctx, lib); err != nil {
			integrationsLog.Error("rescan request failed", "connection", conn.Name, "event", ev.Type, "library", lib.Name, "err", err)
			continue
		}
		rescanning = append(rescanning, lib.Name)
	}
	return c.JSON(http.StatusAccepted, map[string]any{"event": ev.Type, "rescanning": rescanning})
}

// applyArrRequest copies a create or update body onto a connection,
// validating as it goes. Blank fields keep what the connection has, and a
// callback URL defaults to the address this request came in on.
func applyArrRequest(c echo.Context, conn *store.ArrConnection, req arrRequest) error {
	if name := strings.TrimSpace(req.Name); name != "" {
		conn.Name = name
	} else if conn.Name == "" {
		conn.Name = arr.New(conn.Kind, "", "").Label()
	}
	if u := strings.TrimSpace(req.URL); u != "" {
		conn.URL = strings.TrimRight(u, "/")
	}
	if k := strings.TrimSpace(req.APIKey); k != "" {
		conn.APIKey = k
	}
	if u := strings.TrimSpace(req.CallbackURL); u != "" {
		conn.CallbackURL = strings.TrimRight(u, "/")
	} else if conn.CallbackURL == "" {
		conn.CallbackURL = c.Scheme() + "://" + c.Request().Host
	}
	for field, v := range map[string]string{"url": conn.URL, "callbackUrl": conn.CallbackURL} {
		if !httpURL(v) {
			return echo.NewHTTPError(http.StatusBadRequest, field+" must be an http(s) URL")
		}
	}
	if conn.APIKey == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "apiKey is required")
	}
	return nil
}

// uniqueIntegrationName refuses a name another connection already has,
// ignoring case: the name is how an admin tells two instances of the same
// kind apart, on the Integrations page and when approving a request.
func (s *server) uniqueIntegrationName(ctx context.Context, conn store.ArrConnection) error {
	conns, err := s.store.ArrConnections(ctx)
	if err != nil {
		return err
	}
	for _, o := range conns {
		if o.ID != conn.ID && strings.EqualFold(o.Name, conn.Name) {
			return echo.NewHTTPError(http.StatusConflict, "an integration called "+o.Name+" already exists")
		}
	}
	return nil
}

func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (s *server) webhookFor(conn store.ArrConnection, secret string) arr.Webhook {
	return arr.Webhook{
		Name:     "Cineva",
		URL:      conn.CallbackURL + "/api/v1/integrations/" + conn.ID + "/webhook",
		Username: webhookUser,
		Password: secret,
	}
}

// integrationStatus is a connection checked live: whether it answers, its
// health warnings, and the profiles and folders requests can be made with.
type integrationStatus struct {
	Reachable       bool                 `json:"reachable"`
	Error           string               `json:"error,omitempty"`
	Version         string               `json:"version,omitempty"`
	Health          []arr.HealthCheck    `json:"health"`
	QualityProfiles []arr.QualityProfile `json:"qualityProfiles"`
	RootFolders     []arr.RootFolder     `json:"rootFolders"`
	// SuggestedPathMaps map the instance's root folders onto Cineva's
	// library folders, where the two see the media at different paths.
	SuggestedPathMaps []store.PathMap `json:"suggestedPathMaps"`
}

// GET /integrations/:id/status (admin) — the Test button.
func (s *server) integrationStatus(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 15*time.Second)
	defer cancel()
	conn, err := s.store.ArrConnection(ctx, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "integration not found")
	} else if err != nil {
		return err
	}
	out := integrationStatus{
		Health: []arr.HealthCheck{}, QualityProfiles: []arr.QualityProfile{},
		RootFolders: []arr.RootFolder{}, SuggestedPathMaps: []store.PathMap{},
	}
	client := arr.New(conn.Kind, conn.URL, conn.APIKey)
	st, err := client.Status(ctx)
	if err != nil {
		out.Error = err.Error()
		return c.JSON(http.StatusOK, out)
	}
	out.Reachable, out.Version = true, st.Version
	// The rest is best effort: an old instance missing one endpoint should
	// still show what it has.
	var problems []string
	if out.Health, err = client.Health(ctx); err != nil {
		problems = append(problems, "health: "+err.Error())
	}
	if out.QualityProfiles, err = client.QualityProfiles(ctx); err != nil {
		problems = append(problems, "quality profiles: "+err.Error())
	}
	if out.RootFolders, err = client.RootFolders(ctx); err != nil {
		problems = append(problems, "root folders: "+err.Error())
	}
	out.Error = strings.Join(problems, "; ")
	libs, err := s.store.Libraries(ctx)
	if err != nil {
		return err
	}
	roots := make([]string, 0, len(out.RootFolders))
	for _, r := range out.RootFolders {
		roots = append(roots, r.Path)
	}
	out.SuggestedPathMaps = arr.SuggestPathMaps(conn.Kind, roots, libs)
	// Keep the stored version current, since this is the newest word on it.
	if conn.Version != st.Version {
		conn.Version = st.Version
		if _, err := s.store.UpdateArrConnection(ctx, conn); err != nil {
			integrationsLog.Warn("saving the instance's version failed", "connection", conn.Name, "err", err)
		}
	}
	return c.JSON(http.StatusOK, out)
}

// PUT /integrations/:id/options (admin) — what requests are made with,
// and how the instance's paths map onto Cineva's. Nothing is sent to the
// instance, so the webhook is left alone.
func (s *server) setIntegrationOptions(c echo.Context) error {
	ctx := c.Request().Context()
	conn, err := s.store.ArrConnection(ctx, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "integration not found")
	} else if err != nil {
		return err
	}
	var req struct {
		QualityProfileID int             `json:"qualityProfileId"`
		RootFolder       string          `json:"rootFolder"`
		PathMaps         []store.PathMap `json:"pathMaps"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if req.QualityProfileID < 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "qualityProfileId must not be negative")
	}
	maps := []store.PathMap{}
	for _, m := range req.PathMaps {
		m.From, m.To = strings.TrimSpace(m.From), strings.TrimSpace(m.To)
		if m.From == "" && m.To == "" {
			continue // an empty row left in the form
		}
		if m.From == "" || m.To == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "a path mapping needs both the instance's path and Cineva's")
		}
		maps = append(maps, m)
	}
	if err := s.store.SetArrOptions(ctx, conn.ID, req.QualityProfileID, strings.TrimSpace(req.RootFolder), maps); err != nil {
		return err
	}
	saved, err := s.store.ArrConnection(ctx, conn.ID)
	if err != nil {
		return err
	}
	s.audit(c, "integration.options", "integration", conn.ID, conn.Name, "")
	s.events.Publish(events.Event{Type: "integrations"})
	return c.JSON(http.StatusOK, saved)
}
