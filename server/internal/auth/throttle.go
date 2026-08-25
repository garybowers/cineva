package auth

import (
	"sync"
	"time"
)

// Throttle slows credential guessing: after `Threshold` consecutive
// failures on a key (a user id, or a username for password sign-in) the
// key is locked for a delay that doubles with every further failure, up
// to Max. PINs are 4–6 digits, so without this they would fall in
// minutes; passwords get the same protection for free.
type Throttle struct {
	Threshold int
	Base, Max time.Duration
	now       func() time.Time

	mu    sync.Mutex
	state map[string]*throttleEntry
}

type throttleEntry struct {
	fails int
	until time.Time
}

func NewThrottle() *Throttle {
	return &Throttle{Threshold: 5, Base: 30 * time.Second, Max: 15 * time.Minute, now: time.Now, state: map[string]*throttleEntry{}}
}

// Allow reports whether an attempt on key may proceed now; when locked it
// returns how long to wait.
func (t *Throttle) Allow(key string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.state[key]
	if !ok {
		return true, 0
	}
	if wait := e.until.Sub(t.now()); wait > 0 {
		return false, wait
	}
	return true, 0
}

// Fail records a failed attempt and returns the lock delay now in force
// (0 while still under the threshold).
func (t *Throttle) Fail(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.state[key]
	if e == nil {
		e = &throttleEntry{}
		t.state[key] = e
	}
	e.fails++
	if e.fails < t.Threshold {
		return 0
	}
	delay := t.Base << uint(e.fails-t.Threshold)
	if delay > t.Max || delay <= 0 {
		delay = t.Max
	}
	e.until = t.now().Add(delay)
	return delay
}

// Reset clears a key after a successful sign-in.
func (t *Throttle) Reset(key string) {
	t.mu.Lock()
	delete(t.state, key)
	t.mu.Unlock()
}
