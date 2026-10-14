// Package events is the in-process pub/sub bus behind the /api/v1/events
// stream: state changes (scan progress, library changes) are published here
// and pushed to connected UIs so they update without polling or refreshes.
package events

import (
	"context"
	"sync"
)

// Event is one server-side state change. Type names the change ("scan",
// "libraries"); Data is the JSON-serialisable payload, may be nil when the
// event is just an invalidation hint ("refetch your list").
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
	// User, when set, addresses the event to that user's streams only;
	// Session narrows it to one device (a session's public id). Both are
	// routing, not payload, and are never sent.
	User    string `json:"-"`
	Session string `json:"-"`
	// Allow, when set, narrows the event further to the users it returns
	// true for (admins always get it): a Home Assistant entity's state
	// goes only to those who may see that entity. Called once per stream
	// per event, so it must be cheap.
	Allow func(userID string) bool `json:"-"`
}

// Bus fans events out to subscribers. Slow subscribers never block a
// publish: when a subscriber's buffer is full the event is dropped for that
// subscriber only — the stream is advisory (clients refetch on receipt), so
// a dropped event costs staleness until the next event, not correctness.
type Bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func NewBus() *Bus {
	return &Bus{subs: map[chan Event]struct{}{}}
}

// Subscribe registers a listener; the channel closes when ctx is done.
func (b *Bus) Subscribe(ctx context.Context) <-chan Event {
	ch := make(chan Event, 16)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
		close(ch)
	}()
	return ch
}

// Publish delivers the event to every subscriber that has buffer space.
// Safe on a nil bus so publishers don't need wiring checks.
func (b *Bus) Publish(ev Event) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}
