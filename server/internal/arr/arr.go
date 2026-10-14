// Package arr talks to Sonarr, Radarr and Lidarr: enough of their API to
// check a connection and register a webhook in it, the shape of the events
// that webhook delivers, and the calls a request needs — adding or
// monitoring a film or series and searching for it.
//
// The apps share one API surface for this — system status, health and the
// notification ("Connect") providers — so one client serves all three.
// Sonarr and Radarr speak v3 of it, Lidarr v1.
package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is one Sonarr or Radarr instance.
type Client struct {
	Kind   string // "sonarr" | "radarr" | "lidarr"
	URL    string // base URL, e.g. http://sonarr.lan:8989
	APIKey string
	HTTP   *http.Client
}

func New(kind, url, apiKey string) *Client {
	return &Client{
		Kind:   kind,
		URL:    strings.TrimRight(url, "/"),
		APIKey: apiKey,
		// Registering a webhook makes Sonarr test it by calling us back
		// before it answers, so allow for a round trip each way.
		HTTP: &http.Client{Timeout: 20 * time.Second},
	}
}

// Kinds are the apps Cineva connects to.
var Kinds = []string{"sonarr", "radarr", "lidarr"}

// Label is the app's name for messages: "Sonarr", "Radarr" or "Lidarr".
func (c *Client) Label() string {
	switch c.Kind {
	case "radarr":
		return "Radarr"
	case "lidarr":
		return "Lidarr"
	}
	return "Sonarr"
}

// api is the path of one of the app's endpoints: Lidarr's API is v1, the
// others' v3, with the same shapes where Cineva uses them.
func (c *Client) api(path string) string {
	if c.Kind == "lidarr" {
		return "/api/v1" + path
	}
	return "/api/v3" + path
}

// Status is the part of /system/status worth keeping.
type Status struct {
	AppName string `json:"appName"`
	Version string `json:"version"`
}

// Status checks the URL and key, and that the app at the other end is the
// kind it was added as: a Radarr URL entered as Sonarr would otherwise
// accept a webhook whose events Cineva then reads as the wrong media type.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var st Status
	if err := c.do(ctx, http.MethodGet, c.api("/system/status"), nil, &st); err != nil {
		return Status{}, err
	}
	if st.AppName != "" && !strings.EqualFold(st.AppName, c.Label()) {
		return Status{}, fmt.Errorf("%s is %s, not %s", c.URL, st.AppName, c.Label())
	}
	return st, nil
}

// Webhook is what Cineva asks the app to call.
type Webhook struct {
	Name     string
	URL      string
	Username string
	Password string
}

// triggers are the events that change what is on disk, plus grabs, which
// tell a request it is downloading. Health and "added to the app" events
// matter to neither, so they are not subscribed to.
var triggers = map[string]bool{
	"onGrab":                        true,
	"onDownload":                    true, // includes upgrades in older versions
	"onUpgrade":                     true,
	"onImportComplete":              true,
	"onRename":                      true,
	"onSeriesDelete":                true,
	"onEpisodeFileDelete":           true,
	"onEpisodeFileDeleteForUpgrade": true,
	"onMovieDelete":                 true,
	"onMovieFileDelete":             true,
	"onMovieFileDeleteForUpgrade":   true,
	// Lidarr's.
	"onReleaseImport": true,
	"onArtistDelete":  true,
	"onAlbumDelete":   true,
	"onTrackRetag":    true,
}

// RegisterWebhook creates Cineva's webhook in the app, or updates the one
// with the given id, and returns its id. An id the app no longer has (the
// webhook was deleted there) is created afresh. The app tests the webhook
// before saving it, so Cineva must already accept the credentials.
func (c *Client) RegisterWebhook(ctx context.Context, id int, hook Webhook) (int, error) {
	var def map[string]any
	if id > 0 {
		err := c.do(ctx, http.MethodGet, c.api(fmt.Sprintf("/notification/%d", id)), nil, &def)
		var se *StatusError
		if errors.As(err, &se) && se.Code == http.StatusNotFound {
			id, def = 0, nil
		} else if err != nil {
			return 0, err
		}
	}
	if def == nil {
		// Start from the app's own template rather than a hand-written body,
		// so fields and triggers this version has (or lacks) come out right.
		var schema []map[string]any
		if err := c.do(ctx, http.MethodGet, c.api("/notification/schema"), nil, &schema); err != nil {
			return 0, err
		}
		for _, s := range schema {
			if s["implementation"] == "Webhook" {
				def = s
				break
			}
		}
		if def == nil {
			return 0, fmt.Errorf("%s offers no webhook connection", c.Label())
		}
	}
	applyWebhook(def, hook)

	var saved struct {
		ID int `json:"id"`
	}
	var err error
	if id > 0 {
		err = c.do(ctx, http.MethodPut, c.api(fmt.Sprintf("/notification/%d", id)), def, &saved)
	} else {
		err = c.do(ctx, http.MethodPost, c.api("/notification"), def, &saved)
	}
	if err != nil {
		return 0, err
	}
	if saved.ID == 0 {
		saved.ID = id
	}
	return saved.ID, nil
}

// applyWebhook fills a notification definition in place: name, the
// subscribed triggers (only those the app says it supports), and the
// webhook's URL, method and credentials.
func applyWebhook(def map[string]any, hook Webhook) {
	def["name"] = hook.Name
	for key := range def {
		if !strings.HasPrefix(key, "on") {
			continue
		}
		supported, _ := def["supportsO"+key[1:]].(bool)
		def[key] = supported && triggers[key]
	}
	fields, _ := def["fields"].([]any)
	for _, f := range fields {
		field, ok := f.(map[string]any)
		if !ok {
			continue
		}
		switch field["name"] {
		case "url":
			field["value"] = hook.URL
		case "method":
			field["value"] = 1 // POST
		case "username":
			field["value"] = hook.Username
		case "password":
			field["value"] = hook.Password
		}
	}
}

// DeleteWebhook removes Cineva's webhook from the app. One already gone is
// not an error.
func (c *Client) DeleteWebhook(ctx context.Context, id int) error {
	if id <= 0 {
		return nil
	}
	err := c.do(ctx, http.MethodDelete, c.api(fmt.Sprintf("/notification/%d", id)), nil, nil)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return nil
	}
	return err
}

// StatusError is a non-2xx answer, with the app's own explanation when it
// gave one.
type StatusError struct {
	App     string
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	switch {
	case e.Code == http.StatusUnauthorized:
		return e.App + " rejected the API key"
	case e.Message != "":
		return e.App + ": " + e.Message
	default:
		return fmt.Sprintf("%s answered %d", e.App, e.Code)
	}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, rd)
	if err != nil {
		return fmt.Errorf("%s request: %w", c.Label(), err)
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s at %s: %w", c.Label(), c.URL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("%s response: %w", c.Label(), err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &StatusError{App: c.Label(), Code: resp.StatusCode, Message: problem(raw)}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s response: %w", c.Label(), err)
	}
	return nil
}

// problem pulls a readable message out of an error body: a list of
// validation failures (what a failed webhook test returns), or an object
// with a message.
func problem(raw []byte) string {
	var failures []struct {
		ErrorMessage string `json:"errorMessage"`
	}
	if json.Unmarshal(raw, &failures) == nil {
		msgs := []string{}
		for _, f := range failures {
			if f.ErrorMessage != "" {
				msgs = append(msgs, f.ErrorMessage)
			}
		}
		if len(msgs) > 0 {
			return strings.Join(msgs, "; ")
		}
	}
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Message != "" {
		return obj.Message
	}
	return ""
}
