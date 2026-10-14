// Package playback tracks what is playing right now — every play, direct or
// transcoded, video or music — for the admin Activity page. Entries are
// created on play, kept alive by the client's progress heartbeats, and
// dropped when the client says stop or falls silent.
package playback

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Client describes the device a playback belongs to, from what the HTTP
// request reveals. Device registration (milestone 11) will add a real name.
type Client struct {
	IP        string `json:"ip"`
	Browser   string `json:"browser"`
	OS        string `json:"os"`
	UserAgent string `json:"userAgent"`
}

// Playback is one active play.
type Playback struct {
	ID           string    `json:"id"`
	UserID       string    `json:"userId"`
	ItemID       string    `json:"itemId"`
	Title        string    `json:"title"`
	Subtitle     string    `json:"subtitle,omitempty"` // series/season or artist/album context
	Kind         string    `json:"kind"`
	Mode         string    `json:"mode"` // "direct" | "hls"
	Quality      string    `json:"quality,omitempty"`
	SessionID    string    `json:"sessionId,omitempty"` // HLS transcode session, when Mode is hls
	DeviceID     string    `json:"deviceId,omitempty"`  // the browser session (device) playing it
	Client       Client    `json:"client"`
	PositionSecs int       `json:"positionSecs"`
	DurationSecs int       `json:"durationSecs"`
	Paused       bool      `json:"paused"`
	StartedAt    time.Time `json:"startedAt"`
	LastSeenAt   time.Time `json:"lastSeenAt"`

	// EventID is the play_events row opened for this playback, closed when
	// it ends. Not serialised: it is a server-side bookkeeping handle, not
	// something a client has any use for.
	EventID int64 `json:"-"`
}

// Registry is the in-memory set of active playbacks, keyed by user+item so
// a client that restarts the same item replaces its own entry.
type Registry struct {
	IdleTimeout time.Duration
	OnChange    func() // called after any change (event bus)
	// OnEnd is called once for each playback that finishes, however it
	// finishes: an explicit stop, its transcode session being killed, or
	// going idle long enough to be reaped. A playback that is never
	// explicitly stopped — the usual case, since browsers close tabs
	// without warning — would otherwise leave its history row open for
	// ever.
	OnEnd func(Playback)
	// OnPause is called when a heartbeat says a playback was paused or
	// resumed; Paused carries which.
	OnPause func(Playback)

	mu   sync.Mutex
	byID map[string]*Playback
	// stopped remembers playbacks an administrator ended, because removing
	// one is not enough on its own: the progress heartbeat re-registers a
	// missing playback so that a reap or a server restart does not lose a
	// stream that is still running. Without this marker an admin's stop
	// would be undone a few seconds later by the client's own heartbeat.
	stopped map[string]time.Time
}

// stopGrace is how long a stop is remembered. It has to outlast the idle
// timeout so a client that is mid-heartbeat still learns it was stopped,
// but stay short enough that the viewer can start the item again by hand
// shortly afterwards — an admin stop is not a ban.
const stopGrace = 90 * time.Second

func NewRegistry(idle time.Duration) *Registry {
	if idle <= 0 {
		idle = 45 * time.Second
	}
	return &Registry{
		IdleTimeout: idle,
		byID:        map[string]*Playback{},
		stopped:     map[string]time.Time{},
	}
}

func key(userID, itemID string) string { return userID + "/" + itemID }

// Start records a new playback (replacing any existing one for the same
// user+item) and returns it.
func (r *Registry) Start(p Playback) Playback {
	now := time.Now().UTC()
	p.ID = key(p.UserID, p.ItemID)
	p.StartedAt, p.LastSeenAt = now, now
	r.mu.Lock()
	r.byID[p.ID] = &p
	r.mu.Unlock()
	r.changed()
	return p
}

// Touch is the client heartbeat: position and paused state. Returns false
// when there is no such playback (the client should treat that as "stale").
func (r *Registry) Touch(userID, itemID string, positionSecs int, paused bool) bool {
	r.mu.Lock()
	p, ok := r.byID[key(userID, itemID)]
	var flipped *Playback
	if ok {
		if p.Paused != paused {
			flipped = p
		}
		p.PositionSecs, p.Paused, p.LastSeenAt = positionSecs, paused, time.Now().UTC()
		if flipped != nil {
			cp := *p
			flipped = &cp
		}
	}
	r.mu.Unlock()
	if flipped != nil && r.OnPause != nil {
		r.OnPause(*flipped)
	}
	if ok {
		r.changed()
	}
	return ok
}

// Stop removes a playback; returns its HLS session id (if any) so the
// caller can tear the transcode down too.
func (r *Registry) Stop(userID, itemID string) (sessionID string, ok bool) {
	r.mu.Lock()
	p, ok := r.byID[key(userID, itemID)]
	var ended Playback
	if ok {
		sessionID = p.SessionID
		ended = *p
		delete(r.byID, p.ID)
	}
	r.mu.Unlock()
	if ok {
		r.ended(ended)
		r.changed()
	}
	return sessionID, ok
}

// StopSession removes whichever playback owns the given HLS session.
func (r *Registry) StopSession(sessionID string) {
	r.mu.Lock()
	var ended []Playback
	for id, p := range r.byID {
		if p.SessionID == sessionID {
			ended = append(ended, *p)
			delete(r.byID, id)
		}
	}
	r.mu.Unlock()
	for _, p := range ended {
		r.ended(p)
	}
	if len(ended) > 0 {
		r.changed()
	}
}

// Get returns the playback of an item by a user, if there is one.
func (r *Registry) Get(userID, itemID string) (Playback, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.byID[key(userID, itemID)]
	if !ok {
		return Playback{}, false
	}
	return *p, true
}

// OnDevice returns what a device (a browser session) is playing, if
// anything: the playback it most recently reported on.
func (r *Registry) OnDevice(userID, deviceID string) (Playback, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best *Playback
	for _, p := range r.byID {
		if p.UserID == userID && p.DeviceID == deviceID && (best == nil || p.LastSeenAt.After(best.LastSeenAt)) {
			best = p
		}
	}
	if best == nil {
		return Playback{}, false
	}
	return *best, true
}

// Claim makes a playback the given device's, as when a device that handed
// a title over starts playing it again. Reports whether that changed it.
func (r *Registry) Claim(userID, itemID, deviceID string) bool {
	if deviceID == "" {
		return false
	}
	r.mu.Lock()
	p, ok := r.byID[key(userID, itemID)]
	changed := ok && p.DeviceID != deviceID
	if changed {
		p.DeviceID = deviceID
	}
	r.mu.Unlock()
	if changed {
		r.changed()
	}
	return changed
}

// HeldElsewhere reports whether a user's playback of an item is now another
// device's, still playing: what a device that handed the title over (and
// sits paused on it) must not overwrite. A device that resumes playing
// takes it back.
func (r *Registry) HeldElsewhere(userID, itemID, deviceID string) bool {
	if deviceID == "" {
		return false
	}
	p, ok := r.Get(userID, itemID)
	return ok && p.DeviceID != "" && p.DeviceID != deviceID && !p.Paused
}

// List returns active playbacks, most recently started first.
func (r *Registry) List() []Playback {
	r.mu.Lock()
	out := make([]Playback, 0, len(r.byID))
	for _, p := range r.byID {
		out = append(out, *p)
	}
	r.mu.Unlock()
	for i := 1; i < len(out); i++ { // insertion sort: the list is tiny
		for j := i; j > 0 && out[j].StartedAt.After(out[j-1].StartedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Run reaps playbacks whose client has gone silent. Call in a goroutine.
func (r *Registry) Run(ctx context.Context) {
	t := time.NewTicker(r.IdleTimeout / 3)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			r.reap(now)
		}
	}
}

func (r *Registry) reap(now time.Time) {
	r.mu.Lock()
	var ended []Playback
	for id, p := range r.byID {
		if now.Sub(p.LastSeenAt) > r.IdleTimeout {
			ended = append(ended, *p)
			delete(r.byID, id)
		}
	}
	// A marker whose client never came back would otherwise leak.
	for k, at := range r.stopped {
		if now.Sub(at) > stopGrace {
			delete(r.stopped, k)
		}
	}
	r.mu.Unlock()
	for _, p := range ended {
		r.ended(p)
	}
	if len(ended) > 0 {
		r.changed()
	}
}

// ended notifies OnEnd outside the lock — the callback writes to the
// database, and holding the registry's mutex across that would stall every
// progress report.
func (r *Registry) ended(p Playback) {
	if r.OnEnd != nil {
		r.OnEnd(p)
	}
}

func (r *Registry) changed() {
	if r.OnChange != nil {
		r.OnChange()
	}
}

// ParseUserAgent pulls a browser and OS name out of a User-Agent string —
// enough for "Firefox · Linux" on the Activity page, no more.
func ParseUserAgent(ua string) (browser, os string) {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "edg/"):
		browser = "Edge"
	case strings.Contains(l, "opr/"):
		browser = "Opera"
	case strings.Contains(l, "firefox/"):
		browser = "Firefox"
	case strings.Contains(l, "chrome/") || strings.Contains(l, "crios/"):
		browser = "Chrome"
	case strings.Contains(l, "safari/"):
		browser = "Safari"
	case strings.Contains(l, "cineva"):
		browser = "Cineva app"
	case ua == "":
		browser = "Unknown"
	default:
		browser = strings.SplitN(ua, "/", 2)[0]
	}
	switch {
	case strings.Contains(l, "android"):
		os = "Android"
	case strings.Contains(l, "iphone") || strings.Contains(l, "ipad"):
		os = "iOS"
	case strings.Contains(l, "windows"):
		os = "Windows"
	case strings.Contains(l, "mac os x") || strings.Contains(l, "macintosh"):
		os = "macOS"
	case strings.Contains(l, "cros"):
		os = "ChromeOS"
	case strings.Contains(l, "linux"):
		os = "Linux"
	default:
		os = ""
	}
	return browser, os
}

// StopByAdmin ends a playback on an administrator's behalf and remembers it,
// so the client's next heartbeat is told to stop rather than silently
// re-registering. Returns the HLS session id, if any, for the caller to tear
// down.
//
// This is how a direct play is stopped at all: the server only hands out
// bytes over range requests, so there is nothing to kill server-side the way
// a transcode can be killed. The client has to cooperate, and the heartbeat
// is where it is told to.
func (r *Registry) StopByAdmin(userID, itemID string) (sessionID string, ok bool) {
	k := key(userID, itemID)
	r.mu.Lock()
	p, ok := r.byID[k]
	var ended Playback
	if ok {
		sessionID = p.SessionID
		ended = *p
		delete(r.byID, k)
	}
	// Recorded whether or not it was found: a client can be mid-heartbeat
	// when the stop arrives, and the marker is what stops it coming back.
	r.stopped[k] = time.Now().UTC()
	r.mu.Unlock()
	if ok {
		r.ended(ended)
	}
	r.changed()
	return sessionID, ok
}

// Stopped reports whether this playback was ended by an administrator
// recently, and clears the marker — it is asked once, by the heartbeat that
// is about to be refused, and must not refuse every later attempt too.
func (r *Registry) Stopped(userID, itemID string) bool {
	k := key(userID, itemID)
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.stopped[k]
	if !ok {
		return false
	}
	delete(r.stopped, k)
	return time.Since(at) < stopGrace
}
