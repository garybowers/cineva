package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/arr"
	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/store"
)

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
	if req.Kind != "sonarr" && req.Kind != "radarr" {
		return echo.NewHTTPError(http.StatusBadRequest, "kind must be sonarr or radarr")
	}
	conn := store.ArrConnection{Kind: req.Kind}
	if err := applyArrRequest(c, &conn, req); err != nil {
		return err
	}
	ctx := c.Request().Context()
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
			log.Printf("integrations: undo %s: %v", conn.Name, derr)
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
			log.Printf("integrations: restore %s: %v", old.Name, rerr)
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
			log.Printf("integrations: remove webhook from %s: %v", old.URL, err)
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
	if !ev.ChangesFiles() {
		// Test, Grab, Health and the rest: acknowledged, nothing to do.
		return c.JSON(http.StatusOK, map[string]any{"event": ev.Type, "rescanning": []string{}})
	}
	libs, err := s.store.Libraries(ctx)
	if err != nil {
		return err
	}
	rescanning := []string{}
	for _, lib := range arr.Libraries(libs, conn.Kind, ev.Paths) {
		if err := s.manager.RequestScan(ctx, lib); err != nil {
			log.Printf("integrations: %s %s: rescan %s: %v", conn.Name, ev.Type, lib.Name, err)
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
