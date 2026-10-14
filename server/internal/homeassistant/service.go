package homeassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/store"
)

var logger = logging.For("homeassistant")

// Store is what the service needs from the database.
type Store interface {
	HAConnection(ctx context.Context) (store.HAConnection, error)
	SetHAVersion(ctx context.Context, version string) error
	HAExposures(ctx context.Context) (map[string]store.LibraryAccess, error)
}

// Principal is who is asking, for access checks.
type Principal struct {
	ID       string
	Admin    bool
	GroupIDs []string
}

// Allowed applies the library access model to an entity or mode: open to
// everyone unless restricted, then to the users and groups listed, and
// always to admins.
func Allowed(acc store.LibraryAccess, p Principal) bool {
	if p.Admin || !acc.Restricted {
		return true
	}
	if slices.Contains(acc.UserIDs, p.ID) {
		return true
	}
	for _, g := range p.GroupIDs {
		if slices.Contains(acc.GroupIDs, g) {
			return true
		}
	}
	return false
}

// Area is a Home Assistant area.
type Area struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Status is the connection's live state.
type Status struct {
	Configured bool
	Connected  bool
	Version    string
	IsAdmin    bool
	Error      string
}

// Service holds the connection to Home Assistant and everything known
// through it. Its state lives in memory; the database holds only the
// connection, exposure, access and modes.
type Service struct {
	store Store
	http  *http.Client
	// OnStates is called with exposed entities whose state changed;
	// OnChange when anything else clients show changed (the connection
	// came or went, areas, exposure) and they should refetch. Both are
	// called without the service's lock held.
	OnStates func([]Entity)
	OnChange func()
	// OnConnection, when set, hears that a working connection was lost
	// (up false, with the reason) and, after that, that it came back.
	OnConnection func(up bool, detail string)

	mu         sync.RWMutex
	cfg        *store.HAConnection
	c          *conn
	connected  bool
	lastErr    string
	isAdmin    bool
	states     map[string]rawState
	areaNames  map[string]string
	entityArea map[string]string // the entity's own area, from the entity registry
	entityDev  map[string]string // its device, whose area it takes otherwise
	deviceArea map[string]string
	exposure   map[string]store.LibraryAccess
	streams    map[string]string // camera entity id -> its HLS stream's base URL path

	root      context.Context
	runCancel context.CancelFunc
	runDone   chan struct{}
	// lostTold is that OnConnection was told the connection was lost, so
	// the next connect is news.
	lostTold bool
}

func New(st Store) *Service {
	return &Service{
		store: st,
		http: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: 20 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		}},
		exposure: map[string]store.LibraryAccess{},
		streams:  map[string]string{},
	}
}

// Start loads exposure and, when a connection is saved, keeps the
// websocket open until ctx ends.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	s.root = ctx
	s.mu.Unlock()
	if err := s.ReloadExposure(ctx); err != nil {
		return err
	}
	cfg, err := s.store.HAConnection(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	s.Apply(&cfg)
	return nil
}

// Apply switches to a new connection, or disconnects with nil: the old
// websocket closes and a new one opens in the background.
func (s *Service) Apply(cfg *store.HAConnection) {
	s.mu.Lock()
	cancel, done, root := s.runCancel, s.runDone, s.root
	s.runCancel, s.runDone = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	s.mu.Lock()
	s.cfg = cfg
	s.c, s.connected, s.lastErr, s.isAdmin = nil, false, "", false
	s.states, s.streams = nil, map[string]string{}
	if cfg != nil && root != nil {
		ctx, cancel := context.WithCancel(root)
		s.runCancel, s.runDone = cancel, make(chan struct{})
		go s.run(ctx, *cfg, s.runDone)
	}
	s.mu.Unlock()
	s.changed()
}

// run keeps one websocket open, reconnecting with backoff: 1 s doubling to
// a minute, back to 1 s after a connection that lasted.
func (s *Service) run(ctx context.Context, cfg store.HAConnection, done chan struct{}) {
	defer close(done)
	backoff := time.Second
	for {
		began := time.Now()
		err := s.session(ctx, cfg)
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		wasUp := s.connected
		s.c, s.connected, s.streams = nil, false, map[string]string{}
		s.lastErr = err.Error()
		s.mu.Unlock()
		if wasUp {
			logger.Warn("home assistant connection lost", "url", cfg.URL, "err", err)
			s.mu.Lock()
			s.lostTold = true
			s.mu.Unlock()
			if s.OnConnection != nil {
				s.OnConnection(false, err.Error())
			}
		} else {
			logger.Debug("home assistant connect failed", "url", cfg.URL, "err", err)
		}
		s.changed()
		if time.Since(began) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// session is one connection: authenticate, subscribe, load, then wait for
// it to drop.
func (s *Service) session(ctx context.Context, cfg store.HAConnection) error {
	c, err := dial(ctx, cfg.URL, cfg.Token)
	if err != nil {
		return err
	}
	defer c.close()
	admin, err := currentUserIsAdmin(ctx, c)
	if err != nil {
		return err
	}
	// Subscribed before the states are loaded, so no change falls between.
	if err := c.subscribe(ctx, map[string]any{"type": "subscribe_events", "event_type": "state_changed"}, s.onStateChanged); err != nil {
		return err
	}
	for _, ev := range []string{"area_registry_updated", "device_registry_updated", "entity_registry_updated"} {
		// On the read goroutine: the reload is a call, whose answer that
		// same goroutine has to read, so it runs on its own.
		reload := func(json.RawMessage) {
			go func() {
				if err := s.loadRegistries(ctx, c); err != nil {
					logger.Warn("reloading home assistant registries failed", "err", err)
				}
				s.changed()
			}()
		}
		if err := c.subscribe(ctx, map[string]any{"type": "subscribe_events", "event_type": ev}, reload); err != nil {
			return err
		}
	}
	raw, err := c.call(ctx, map[string]any{"type": "get_states"})
	if err != nil {
		return err
	}
	var list []rawState
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("home assistant states: %w", err)
	}
	states := make(map[string]rawState, len(list))
	for _, st := range list {
		if Supported(st.EntityID) {
			states[st.EntityID] = st
		}
	}
	s.mu.Lock()
	s.states = states
	s.mu.Unlock()
	if err := s.loadRegistries(ctx, c); err != nil {
		return err
	}
	s.mu.Lock()
	s.c, s.connected, s.lastErr, s.isAdmin = c, true, "", admin
	back := s.lostTold
	s.lostTold = false
	s.mu.Unlock()
	if back && s.OnConnection != nil {
		s.OnConnection(true, "")
	}
	logger.Info("connected to home assistant", "url", cfg.URL, "version", c.version, "entities", len(states))
	if c.version != "" && c.version != cfg.Version {
		if err := s.store.SetHAVersion(ctx, c.version); err != nil {
			logger.Warn("saving home assistant version failed", "err", err)
		}
	}
	s.changed()
	select {
	case <-c.done:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = errors.New("connection closed")
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func currentUserIsAdmin(ctx context.Context, c *conn) (bool, error) {
	raw, err := c.call(ctx, map[string]any{"type": "auth/current_user"})
	if err != nil {
		return false, err
	}
	var u struct {
		IsAdmin bool `json:"is_admin"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return false, fmt.Errorf("home assistant user: %w", err)
	}
	return u.IsAdmin, nil
}

// loadRegistries reads areas, devices and entities, for which area each
// entity is in.
func (s *Service) loadRegistries(ctx context.Context, c *conn) error {
	var areas []struct {
		AreaID string `json:"area_id"`
		Name   string `json:"name"`
	}
	var devices []struct {
		ID     string `json:"id"`
		AreaID string `json:"area_id"`
	}
	var entities []struct {
		EntityID string `json:"entity_id"`
		AreaID   string `json:"area_id"`
		DeviceID string `json:"device_id"`
	}
	for _, q := range []struct {
		cmd string
		out any
	}{
		{"config/area_registry/list", &areas},
		{"config/device_registry/list", &devices},
		{"config/entity_registry/list", &entities},
	} {
		raw, err := c.call(ctx, map[string]any{"type": q.cmd})
		if err != nil {
			return fmt.Errorf("%s: %w", q.cmd, err)
		}
		if err := json.Unmarshal(raw, q.out); err != nil {
			return fmt.Errorf("%s: %w", q.cmd, err)
		}
	}
	areaNames := make(map[string]string, len(areas))
	for _, a := range areas {
		areaNames[a.AreaID] = a.Name
	}
	deviceArea := make(map[string]string, len(devices))
	for _, d := range devices {
		deviceArea[d.ID] = d.AreaID
	}
	entityArea, entityDev := map[string]string{}, map[string]string{}
	for _, e := range entities {
		if !Supported(e.EntityID) {
			continue
		}
		entityArea[e.EntityID], entityDev[e.EntityID] = e.AreaID, e.DeviceID
	}
	s.mu.Lock()
	s.areaNames, s.deviceArea, s.entityArea, s.entityDev = areaNames, deviceArea, entityArea, entityDev
	s.mu.Unlock()
	return nil
}

func (s *Service) onStateChanged(body json.RawMessage) {
	var ev struct {
		Data struct {
			EntityID string    `json:"entity_id"`
			NewState *rawState `json:"new_state"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &ev) != nil || !Supported(ev.Data.EntityID) {
		return
	}
	id := ev.Data.EntityID
	s.mu.Lock()
	if s.states == nil {
		s.mu.Unlock()
		return
	}
	if ev.Data.NewState == nil {
		delete(s.states, id)
	} else {
		s.states[id] = *ev.Data.NewState
	}
	_, exposed := s.exposure[id]
	var e Entity
	if ev.Data.NewState != nil {
		e = normalise(*ev.Data.NewState, s.areaOf(id))
	}
	s.mu.Unlock()
	if !exposed {
		return
	}
	if ev.Data.NewState == nil {
		s.changed() // gone: clients refetch rather than keep a dead control
		return
	}
	if s.OnStates != nil {
		s.OnStates([]Entity{e})
	}
}

// areaOf resolves an entity's area: its own, else its device's. Called
// with the lock held.
func (s *Service) areaOf(entityID string) string {
	if a := s.entityArea[entityID]; a != "" {
		return a
	}
	return s.deviceArea[s.entityDev[entityID]]
}

func (s *Service) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

// ReloadExposure rereads which entities are exposed and who may use them,
// after an admin changes it.
func (s *Service) ReloadExposure(ctx context.Context) error {
	exp, err := s.store.HAExposures(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.exposure = exp
	s.mu.Unlock()
	return nil
}

// Status is the connection's live state.
func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Status{Configured: s.cfg != nil, Connected: s.connected, IsAdmin: s.isAdmin, Error: s.lastErr}
	if s.c != nil {
		st.Version = s.c.version
	}
	return st
}

// Entities is every entity in a supported domain Home Assistant has, by
// entity id. Empty until the first connect.
func (s *Service) Entities() []Entity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entity, 0, len(s.states))
	for id, st := range s.states {
		out = append(out, normalise(st, s.areaOf(id)))
	}
	slices.SortFunc(out, func(a, b Entity) int { return strings.Compare(a.EntityID, b.EntityID) })
	return out
}

// Entity is one entity's current state.
func (s *Service) Entity(entityID string) (Entity, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.states[entityID]
	if !ok {
		return Entity{}, false
	}
	return normalise(st, s.areaOf(entityID)), true
}

// Areas is every area in Home Assistant, by name.
func (s *Service) Areas() []Area {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Area, 0, len(s.areaNames))
	for id, name := range s.areaNames {
		out = append(out, Area{ID: id, Name: name})
	}
	slices.SortFunc(out, func(a, b Area) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out
}

// Exposure is a copy of every exposed entity's access.
func (s *Service) Exposure() map[string]store.LibraryAccess {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]store.LibraryAccess, len(s.exposure))
	for k, v := range s.exposure {
		out[k] = v
	}
	return out
}

// CanSee reports whether an entity is exposed and p may use it.
func (s *Service) CanSee(p Principal, entityID string) bool {
	s.mu.RLock()
	acc, ok := s.exposure[entityID]
	s.mu.RUnlock()
	return ok && Allowed(acc, p)
}

// ErrUnreachable is Home Assistant not connected right now.
var ErrUnreachable = errors.New("home assistant is not connected")

func (s *Service) live() (*conn, store.HAConnection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return nil, store.HAConnection{}, fmt.Errorf("%w: none is set up", ErrUnreachable)
	}
	if !s.connected || s.c == nil {
		if s.lastErr != "" {
			return nil, *s.cfg, fmt.Errorf("%w: %s", ErrUnreachable, s.lastErr)
		}
		return nil, *s.cfg, ErrUnreachable
	}
	return s.c, *s.cfg, nil
}

// Do works one control: the action is checked against what the entity's
// domain allows, then sent as a service call.
func (s *Service) Do(ctx context.Context, entityID string, a Action) error {
	var sources []string
	if e, ok := s.Entity(entityID); ok {
		sources = e.Sources
	}
	data, err := serviceData(entityID, a, sources)
	if err != nil {
		return err
	}
	c, _, err := s.live()
	if err != nil {
		return err
	}
	_, err = c.call(ctx, map[string]any{
		"type": "call_service", "domain": domainOf(entityID), "service": a.Service,
		"service_data": data, "target": map[string]any{"entity_id": entityID},
	})
	return err
}

// Snapshot fetches a camera's current picture. The caller closes the body.
func (s *Service) Snapshot(ctx context.Context, entityID string) (*http.Response, error) {
	if domainOf(entityID) != "camera" {
		return nil, invalid("%s is not a camera", entityID)
	}
	_, cfg, err := s.live()
	if err != nil {
		return nil, err
	}
	return s.get(ctx, cfg, strings.TrimRight(cfg.URL, "/")+"/api/camera_proxy/"+entityID)
}

// hlsFile is one file of Home Assistant's HLS stream: a playlist or the
// init segment at the top, segments and parts one level down.
var hlsFile = regexp.MustCompile(`^(?:(segment|part)/)?[A-Za-z0-9_.-]+\.(m3u8|mp4|m4s)$`)

// HLS fetches one file of a camera's live stream, asking Home Assistant
// to start the stream first if need be. The caller closes the body.
func (s *Service) HLS(ctx context.Context, entityID, file string) (*http.Response, error) {
	if domainOf(entityID) != "camera" {
		return nil, invalid("%s is not a camera", entityID)
	}
	if !hlsFile.MatchString(file) || strings.Contains(file, "..") {
		return nil, invalid("no such stream file")
	}
	if e, ok := s.Entity(entityID); !ok || e.Stream == nil || !*e.Stream {
		return nil, invalid("%s cannot stream live", entityID)
	}
	c, cfg, err := s.live()
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		s.mu.RLock()
		base := s.streams[entityID]
		s.mu.RUnlock()
		if base == "" {
			raw, err := c.call(ctx, map[string]any{"type": "camera/stream", "entity_id": entityID})
			if err != nil {
				return nil, err
			}
			var r struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(raw, &r); err != nil || r.URL == "" {
				return nil, fmt.Errorf("home assistant gave no stream for %s", entityID)
			}
			base = r.URL[:strings.LastIndex(r.URL, "/")]
			s.mu.Lock()
			s.streams[entityID] = base
			s.mu.Unlock()
		}
		res, err := s.get(ctx, cfg, strings.TrimRight(cfg.URL, "/")+base+"/"+file)
		if err != nil {
			return nil, err
		}
		// Home Assistant drops a stream nobody is watching, and its token
		// with it: start a new one, once, when the playlist is asked for.
		if res.StatusCode == http.StatusNotFound && attempt == 0 && strings.HasSuffix(file, ".m3u8") {
			res.Body.Close()
			s.mu.Lock()
			delete(s.streams, entityID)
			s.mu.Unlock()
			continue
		}
		return res, nil
	}
}

func (s *Service) get(ctx context.Context, cfg store.HAConnection, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	res, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	return res, nil
}

// Playback events fired into Home Assistant.
const (
	PlaybackStarted = "cineva_playback_started"
	PlaybackPaused  = "cineva_playback_paused"
	PlaybackResumed = "cineva_playback_resumed"
	PlaybackStopped = "cineva_playback_stopped"
)

// PlaybackEventsOn reports whether playback events would be sent now,
// so callers can skip gathering their data when not.
func (s *Service) PlaybackEventsOn() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg != nil && s.cfg.PlaybackEvents && s.isAdmin && s.connected
}

// FirePlayback sends a playback event when the connection has them
// switched on and its user may fire them. It returns at once; a failure
// is logged and costs only that event.
func (s *Service) FirePlayback(event string, data map[string]any) {
	s.mu.RLock()
	on := s.cfg != nil && s.cfg.PlaybackEvents && s.isAdmin && s.connected
	c := s.c
	s.mu.RUnlock()
	if !on || c == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.call(ctx, map[string]any{"type": "fire_event", "event_type": event, "event_data": data}); err != nil {
			logger.Warn("firing playback event failed", "event", event, "err", err)
		}
	}()
}

// CheckResult is what a test connection learned.
type CheckResult struct {
	Version string
	IsAdmin bool
}

// Check connects with a URL and token, without keeping the connection:
// how a new or changed connection is tried before it is saved.
func Check(ctx context.Context, url, token string) (CheckResult, error) {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c, err := dial(cctx, url, token)
	if err != nil {
		return CheckResult{}, err
	}
	defer c.close()
	admin, err := currentUserIsAdmin(cctx, c)
	if err != nil {
		return CheckResult{}, err
	}
	return CheckResult{Version: c.version, IsAdmin: admin}, nil
}
