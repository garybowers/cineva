// Package homeassistant connects Cineva to the household's Home Assistant
// (see docs/homeassistant.md): one websocket for live state and control,
// the controls users may work, cameras proxied to the browser, modes, and
// playback events fired into Home Assistant.
package homeassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// conn is an authenticated websocket to Home Assistant. Commands carry an
// id the result answers with; subscriptions deliver events under the id
// that made them. One goroutine reads, any number may call.
type conn struct {
	ws      *websocket.Conn
	version string

	mu      sync.Mutex
	nextID  int
	pending map[int]chan message
	subs    map[int]func(json.RawMessage)
	err     error // why the read loop stopped; set once
	done    chan struct{}
}

// message is anything Home Assistant sends on the websocket.
type message struct {
	ID      int             `json:"id"`
	Type    string          `json:"type"`
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Event   json.RawMessage `json:"event"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Version string `json:"ha_version"`
	Message string `json:"message"` // auth_invalid's reason
}

// ErrAuth is a token Home Assistant refused.
var ErrAuth = errors.New("home assistant refused the access token")

// websocketURL turns an instance's base URL into its websocket endpoint.
func websocketURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not a URL", base)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("%q must start with http:// or https://", base)
	}
	u.Path += "/api/websocket"
	return u.String(), nil
}

// dial opens the websocket and authenticates with the token. The returned
// conn reads until ctx ends or the connection drops; done closes then.
func dial(ctx context.Context, base, token string) (*conn, error) {
	wsURL, err := websocketURL(base)
	if err != nil {
		return nil, err
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", base, err)
	}
	// get_states and the registries of a large house run to megabytes.
	ws.SetReadLimit(64 << 20)
	read := func() (message, error) {
		var m message
		_, data, err := ws.Read(dctx)
		if err != nil {
			return m, err
		}
		return m, json.Unmarshal(data, &m)
	}
	fail := func(err error) (*conn, error) {
		ws.Close(websocket.StatusNormalClosure, "")
		return nil, err
	}
	m, err := read()
	if err != nil {
		return fail(fmt.Errorf("home assistant handshake: %w", err))
	}
	if m.Type != "auth_required" {
		return fail(fmt.Errorf("home assistant handshake: unexpected %q", m.Type))
	}
	auth, _ := json.Marshal(map[string]string{"type": "auth", "access_token": token})
	if err := ws.Write(dctx, websocket.MessageText, auth); err != nil {
		return fail(fmt.Errorf("home assistant handshake: %w", err))
	}
	if m, err = read(); err != nil {
		return fail(fmt.Errorf("home assistant handshake: %w", err))
	}
	switch m.Type {
	case "auth_ok":
	case "auth_invalid":
		if m.Message != "" {
			return fail(fmt.Errorf("%w: %s", ErrAuth, m.Message))
		}
		return fail(ErrAuth)
	default:
		return fail(fmt.Errorf("home assistant handshake: unexpected %q", m.Type))
	}
	c := &conn{
		ws: ws, version: m.Version, nextID: 1,
		pending: map[int]chan message{}, subs: map[int]func(json.RawMessage){},
		done: make(chan struct{}),
	}
	go c.readLoop(ctx)
	return c, nil
}

func (c *conn) readLoop(ctx context.Context) {
	var err error
	for {
		var data []byte
		if _, data, err = c.ws.Read(ctx); err != nil {
			break
		}
		var m message
		if json.Unmarshal(data, &m) != nil {
			continue // not ours to understand; Home Assistant adds types
		}
		c.mu.Lock()
		switch m.Type {
		case "result":
			if ch, ok := c.pending[m.ID]; ok {
				delete(c.pending, m.ID)
				ch <- m
			}
		case "event":
			if fn, ok := c.subs[m.ID]; ok {
				c.mu.Unlock()
				fn(m.Event)
				continue
			}
		}
		c.mu.Unlock()
	}
	c.mu.Lock()
	c.err = err
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
	c.mu.Unlock()
	close(c.done)
	c.ws.Close(websocket.StatusNormalClosure, "")
}

// call sends a command and waits for its result.
func (c *conn) call(ctx context.Context, cmd map[string]any) (json.RawMessage, error) {
	m, err := c.send(ctx, cmd, nil)
	if err != nil {
		return nil, err
	}
	return m.Result, nil
}

// subscribe sends a subscription command; fn then receives each event's
// body, on the read goroutine, so it must not block for long.
func (c *conn) subscribe(ctx context.Context, cmd map[string]any, fn func(json.RawMessage)) error {
	_, err := c.send(ctx, cmd, fn)
	return err
}

func (c *conn) send(ctx context.Context, cmd map[string]any, sub func(json.RawMessage)) (message, error) {
	ch := make(chan message, 1)
	c.mu.Lock()
	if c.err != nil || isClosed(c.done) {
		c.mu.Unlock()
		return message{}, errors.New("home assistant connection closed")
	}
	id := c.nextID
	c.nextID++
	c.pending[id] = ch
	if sub != nil {
		c.subs[id] = sub
	}
	c.mu.Unlock()
	cmd["id"] = id
	data, err := json.Marshal(cmd)
	if err != nil {
		return message{}, fmt.Errorf("encode %v: %w", cmd["type"], err)
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := c.ws.Write(cctx, websocket.MessageText, data); err != nil {
		return message{}, fmt.Errorf("send %v: %w", cmd["type"], err)
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return message{}, errors.New("home assistant connection closed")
		}
		if !m.Success {
			if m.Error != nil {
				return m, fmt.Errorf("home assistant: %s", m.Error.Message)
			}
			return m, errors.New("home assistant refused the command")
		}
		return m, nil
	case <-cctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		delete(c.subs, id)
		c.mu.Unlock()
		return message{}, fmt.Errorf("home assistant did not answer %v: %w", cmd["type"], cctx.Err())
	}
}

func (c *conn) close() {
	c.ws.Close(websocket.StatusNormalClosure, "")
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
