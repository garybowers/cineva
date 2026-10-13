package library

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/garybowers/cineva/server/internal/store"
)

// Media usually lives on a network share, and a share that stops answering
// does not fail: a read or a stat on a hung mount blocks, sometimes for as
// long as the mount is gone. Anything that waited on one used to wait with
// it — a press of Resume sat for half an hour with nothing on screen. So
// storage is watched, and every touch of a media file on a request path has
// a deadline.

// ErrNotResponding is returned when storage does not answer in time.
var ErrNotResponding = errors.New("not responding")

// Within runs fn and gives up after d, returning ErrNotResponding.
//
// A call blocked in the kernel on a hung mount cannot be interrupted, so on
// a timeout it is abandoned rather than stopped: its goroutine finishes
// whenever the mount does answer, and its result is discarded.
func Within[T any](d time.Duration, fn func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1) // buffered: an abandoned call must not block forever on send
	go func() {
		v, err := fn()
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		return r.v, r.err
	case <-time.After(d):
		var zero T
		return zero, ErrNotResponding
	}
}

// StorageStatus is whether a library's folders can be reached.
type StorageStatus struct {
	Available bool `json:"available"`
	// Since is when it last changed; zero until the first check.
	Since time.Time `json:"since,omitzero"`
	// Path and Error say which folder failed and how, when unavailable.
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

// Message is the sentence shown to someone whose request this stopped.
func (st StorageStatus) Message(library string) string {
	return fmt.Sprintf("The storage for %q isn't reachable (%s: %s). Check that the share is connected, then try again.",
		library, st.Path, st.Error)
}

// StorageMonitor checks every library's folders on an interval and keeps the
// result, so a request can fail at once with a clear reason instead of
// waiting on storage that is already known to be down.
type StorageMonitor struct {
	Store    *store.Store
	Interval time.Duration // between checks
	Timeout  time.Duration // for one folder to answer
	// OnChange, when set, is called when a library becomes reachable or
	// unreachable.
	OnChange func(lib store.Library, st StorageStatus)

	mu     sync.Mutex
	status map[string]StorageStatus // by library id
	// inflight holds paths whose last check has not returned. A hung mount
	// is not stat'ed again while one is still stuck, or each check would
	// strand another goroutine.
	inflight map[string]bool
}

func NewStorageMonitor(st *store.Store) *StorageMonitor {
	return &StorageMonitor{
		Store: st, Interval: 30 * time.Second, Timeout: 5 * time.Second,
		status: map[string]StorageStatus{}, inflight: map[string]bool{},
	}
}

// Status reports a library's last known state. A library not yet checked is
// taken to be available: the deadlines on each request still apply.
func (m *StorageMonitor) Status(libraryID string) StorageStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.status[libraryID]
	if !ok {
		return StorageStatus{Available: true}
	}
	return st
}

// Run checks every library now and then on the interval, until ctx ends.
func (m *StorageMonitor) Run(ctx context.Context) {
	t := time.NewTicker(m.Interval)
	defer t.Stop()
	for {
		libs, err := m.Store.Libraries(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("storage check: %v", err)
		}
		for _, lib := range libs {
			m.Check(lib)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Check looks at one library's folders now and records the result.
func (m *StorageMonitor) Check(lib store.Library) StorageStatus {
	st := StorageStatus{Available: true}
	for _, p := range lib.Paths {
		if err := m.checkPath(p); err != nil {
			st = StorageStatus{Path: p, Error: err.Error()}
			break
		}
	}
	m.record(lib, st)
	return m.Status(lib.ID)
}

func (m *StorageMonitor) checkPath(p string) error {
	m.mu.Lock()
	if m.inflight[p] {
		m.mu.Unlock()
		return ErrNotResponding
	}
	m.inflight[p] = true
	m.mu.Unlock()

	fi, err := Within(m.Timeout, func() (os.FileInfo, error) {
		defer func() {
			m.mu.Lock()
			delete(m.inflight, p)
			m.mu.Unlock()
		}()
		return os.Stat(p)
	})
	switch {
	case err != nil:
		// The bare OS error ("stat /mnt/x: host is down") repeats the path
		// the message already names; keep only what went wrong.
		var pe *os.PathError
		if errors.As(err, &pe) {
			return pe.Err
		}
		return err
	case !fi.IsDir():
		return errors.New("not a folder")
	}
	return nil
}

func (m *StorageMonitor) record(lib store.Library, st StorageStatus) {
	m.mu.Lock()
	prev, seen := m.status[lib.ID]
	changed := !seen || prev.Available != st.Available
	if changed {
		st.Since = time.Now()
	} else {
		st.Since = prev.Since
	}
	m.status[lib.ID] = st
	m.mu.Unlock()
	if !changed {
		return
	}
	// The first check of a healthy library is not news.
	if !seen && st.Available {
		return
	}
	if st.Available {
		log.Printf("storage: %q reachable again", lib.Name)
	} else {
		log.Printf("storage: %q unreachable: %s: %s", lib.Name, st.Path, st.Error)
	}
	if m.OnChange != nil {
		m.OnChange(lib, st)
	}
}
