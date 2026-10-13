package library

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Deadlines for reads that take longer than one call: a transcode, a
// subtitle extracted from a whole film, a file streamed to a browser. Within
// suits a single stat or probe; these suit work that is long but should keep
// moving, where the right question is not "has it finished?" but "has it
// stopped?".

// WithinIdle runs fn, which calls progress whenever it gets anywhere, and
// gives up with ErrNotResponding once d passes without a call. Work that
// keeps moving may take as long as it needs; work that stops is abandoned,
// as with Within.
func WithinIdle[T any](d time.Duration, fn func(progress func()) (T, error)) (T, error) {
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	progress := func() { last.Store(time.Now().UnixNano()) }

	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1) // buffered: an abandoned call must not block forever on send
	go func() {
		v, err := fn(progress)
		done <- result{v, err}
	}()
	tick := time.NewTicker(idleTick(d))
	defer tick.Stop()
	for {
		select {
		case r := <-done:
			return r.v, r.err
		case now := <-tick.C:
			if now.Sub(time.Unix(0, last.Load())) > d {
				var zero T
				return zero, ErrNotResponding
			}
		}
	}
}

// WithinFile is Within for a call that reads path. When the call is
// abandoned, path is marked stuck until it returns (see Stuck).
func WithinFile[T any](path string, d time.Duration, fn func() (T, error)) (T, error) {
	finished := make(chan struct{})
	v, err := Within(d, func() (T, error) {
		defer close(finished)
		return fn()
	})
	markIfAbandoned(path, err, finished)
	return v, err
}

// WithinIdleFile is WithinIdle for work that reads path. When the work is
// abandoned, path is marked stuck until it returns (see Stuck).
func WithinIdleFile[T any](path string, d time.Duration, fn func(progress func()) (T, error)) (T, error) {
	finished := make(chan struct{})
	v, err := WithinIdle(d, func(progress func()) (T, error) {
		defer close(finished)
		return fn(progress)
	})
	markIfAbandoned(path, err, finished)
	return v, err
}

// markIfAbandoned marks path stuck when a deadline gave up on a call that
// has not yet returned. A call that failed with ErrNotResponding by itself
// has returned, and leaves nothing behind.
func markIfAbandoned(path string, err error, finished <-chan struct{}) {
	if !errors.Is(err, ErrNotResponding) {
		return
	}
	select {
	case <-finished:
	default:
		MarkStuck(path, finished)
	}
}

// idleTick is how often WithinIdle looks: often enough to give up close to
// the deadline, rarely enough to cost nothing.
func idleTick(d time.Duration) time.Duration {
	return max(d/4, 10*time.Millisecond)
}

// A read abandoned on a deadline is still blocked in the kernel, and another
// read of the same file would only join it: on a hung share each attempt
// strands one more goroutine and one more request on the server's SMB
// connection, which is how stuck reads wedged a host's share before. So the
// abandoned ones are counted by path until they return, and new requests for
// that file are refused at once.
var stuck = struct {
	sync.Mutex
	n map[string]int
}{n: map[string]int{}}

// MarkStuck records that a read of path was abandoned while still blocked.
// It stays recorded until done is closed — when the read finally returns.
func MarkStuck(path string, done <-chan struct{}) {
	stuck.Lock()
	stuck.n[path]++
	stuck.Unlock()
	go func() {
		<-done
		stuck.Lock()
		if stuck.n[path]--; stuck.n[path] <= 0 {
			delete(stuck.n, path)
		}
		stuck.Unlock()
		storageLog.Info("an abandoned read has returned", "path", path)
	}()
}

// Stuck reports whether an abandoned read of path is still blocked.
func Stuck(path string) bool {
	stuck.Lock()
	defer stuck.Unlock()
	return stuck.n[path] > 0
}

// StuckReason is the error a StorageStatus carries when a file is refused
// because an earlier read of it is still blocked.
const StuckReason = "an earlier read is still waiting on the storage"

// MediaFile is a media file opened with a deadline on every touch of the
// storage, for serving to a client. A read that does not answer within the
// timeout is abandoned and returns ErrNotResponding, and so does every read
// after it: the file's offset is unknown while one is still outstanding.
//
// It is an io.ReadSeeker, so http.ServeContent can serve ranges from it.
type MediaFile struct {
	path    string
	f       *os.File
	size    int64
	timeout time.Duration
	// buf is what reads land in before being copied out: an abandoned read
	// still owns the buffer it was given and may write into it whenever it
	// returns, so the caller's slice is never handed to one.
	buf []byte
	// pending is read ahead and not yet handed out. Reads from the share go
	// in chunks of at least readAhead, so a caller copying 32 KB at a time
	// (http.ServeContent does) costs one deadline-guarded read per chunk,
	// not one per 32 KB.
	pending []byte
	err     error
}

// readAhead is the smallest read MediaFile makes of the share: large enough
// that serving a stream is a few reads a second, small enough to arrive well
// inside the read deadline.
const readAhead = 1 << 20

// OpenMedia opens path and stats it, giving up after d.
func OpenMedia(path string, d time.Duration) (*MediaFile, os.FileInfo, error) {
	type opened struct {
		f   *os.File
		fi  os.FileInfo
		err error
	}
	ch := make(chan opened, 1)
	go func() {
		f, err := os.Open(path)
		if err != nil {
			ch <- opened{err: err}
			return
		}
		fi, err := f.Stat()
		if err != nil {
			closeQuietly(f)
			ch <- opened{err: fmt.Errorf("stat %s: %w", path, err)}
			return
		}
		ch <- opened{f: f, fi: fi}
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case o := <-ch:
		if o.err != nil {
			return nil, nil, o.err
		}
		return &MediaFile{path: path, f: o.f, size: o.fi.Size(), timeout: d}, o.fi, nil
	case <-timer.C:
		done := make(chan struct{})
		go func() {
			// A late open still holds a descriptor; close it when it comes.
			if o := <-ch; o.f != nil {
				closeQuietly(o.f)
			}
			close(done)
		}()
		MarkStuck(path, done)
		return nil, nil, fmt.Errorf("open %s: %w", path, ErrNotResponding)
	}
}

// Read reads into p, giving up after the file's timeout.
func (m *MediaFile) Read(p []byte) (int, error) {
	if len(m.pending) > 0 {
		n := copy(p, m.pending)
		m.pending = m.pending[n:]
		return n, nil
	}
	if m.err != nil {
		return 0, m.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	size := max(len(p), readAhead)
	if cap(m.buf) < size {
		m.buf = make([]byte, size)
	}
	buf := m.buf[:size]
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := m.f.Read(buf)
		ch <- result{n, err}
	}()
	timer := time.NewTimer(m.timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		n := copy(p, buf[:r.n])
		m.pending = buf[n:r.n]
		if len(m.pending) > 0 {
			// The rest is handed out first; an error (EOF) waits until it has
			// been, so no data is lost behind it.
			if r.err != nil && !errors.Is(r.err, io.EOF) {
				m.err = r.err
			}
			return n, nil
		}
		return n, r.err
	case <-timer.C:
		m.err = fmt.Errorf("read %s: %w", m.path, ErrNotResponding)
		m.buf = nil // the abandoned read still owns it
		done := make(chan struct{})
		go func() {
			<-ch
			close(done)
		}()
		MarkStuck(m.path, done)
		return 0, m.err
	}
}

// Seek moves the offset. Seeking from the end uses the size from the open
// rather than asking the file: a network filesystem revalidates the size
// for that, which is a round trip to storage that may not answer.
func (m *MediaFile) Seek(offset int64, whence int) (int64, error) {
	if m.err != nil {
		return 0, m.err
	}
	if whence == io.SeekEnd {
		offset, whence = m.size+offset, io.SeekStart
	}
	// The file's own offset is past what was read ahead; a relative seek
	// counts from where the caller has got to.
	if whence == io.SeekCurrent {
		offset -= int64(len(m.pending))
	}
	m.pending = nil
	return m.f.Seek(offset, whence)
}

// Err is the deadline failure that ended the file's reads, or nil.
func (m *MediaFile) Err() error { return m.err }

// Close releases the file without waiting: closing a file on a network
// share is itself a round trip, and nothing the caller does next depends on
// it having finished.
func (m *MediaFile) Close() error {
	go closeQuietly(m.f)
	return nil
}

func closeQuietly(f *os.File) {
	if err := f.Close(); err != nil {
		storageLog.Debug("closing a media file failed", "path", f.Name(), "err", err)
	}
}
