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
	Client       Client    `json:"client"`
	PositionSecs int       `json:"positionSecs"`
	DurationSecs int       `json:"durationSecs"`
	Paused       bool      `json:"paused"`
	StartedAt    time.Time `json:"startedAt"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
}

// Registry is the in-memory set of active playbacks, keyed by user+item so
// a client that restarts the same item replaces its own entry.
type Registry struct {
	IdleTimeout time.Duration
	OnChange    func() // called after any change (event bus)

	mu   sync.Mutex
	byID map[string]*Playback
}

func NewRegistry(idle time.Duration) *Registry {
	if idle <= 0 {
		idle = 45 * time.Second
	}
	return &Registry{IdleTimeout: idle, byID: map[string]*Playback{}}
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
	if ok {
		p.PositionSecs, p.Paused, p.LastSeenAt = positionSecs, paused, time.Now().UTC()
	}
	r.mu.Unlock()
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
	if ok {
		sessionID = p.SessionID
		delete(r.byID, p.ID)
	}
	r.mu.Unlock()
	if ok {
		r.changed()
	}
	return sessionID, ok
}

// StopSession removes whichever playback owns the given HLS session.
func (r *Registry) StopSession(sessionID string) {
	r.mu.Lock()
	var removed bool
	for id, p := range r.byID {
		if p.SessionID == sessionID {
			delete(r.byID, id)
			removed = true
		}
	}
	r.mu.Unlock()
	if removed {
		r.changed()
	}
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
	var removed bool
	for id, p := range r.byID {
		if now.Sub(p.LastSeenAt) > r.IdleTimeout {
			delete(r.byID, id)
			removed = true
		}
	}
	r.mu.Unlock()
	if removed {
		r.changed()
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
