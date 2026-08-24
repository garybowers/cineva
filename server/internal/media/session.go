package media

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Session is one HLS playback session: a temp dir of segments plus the
// transcode goroutine currently filling it. Seeking past produced segments
// cancels the goroutine and restarts it at the requested offset.
type Session struct {
	ID          string `json:"id"`
	ItemID      string `json:"itemId"`
	Title       string `json:"title"`
	InputPath   string `json:"-"`
	Dir         string `json:"-"`
	SegmentSecs int    `json:"segmentSecs"`
	NumSegments int    `json:"numSegments"`

	mu           sync.Mutex
	cancel       context.CancelFunc
	startSegment int
	lastAccess   time.Time
}

type SessionManager struct {
	Root        string
	SegmentSecs int

	mu       sync.Mutex
	sessions map[string]*Session
	baseCtx  context.Context
	stop     context.CancelFunc
}

func NewSessionManager(root string, segmentSecs int) *SessionManager {
	ctx, cancel := context.WithCancel(context.Background())
	sm := &SessionManager{
		Root:        root,
		SegmentSecs: segmentSecs,
		sessions:    map[string]*Session{},
		baseCtx:     ctx,
		stop:        cancel,
	}
	go sm.janitor()
	return sm
}

// Start creates a session and begins transcoding from the given position.
func (sm *SessionManager) Start(itemID, title, inputPath string, runtimeSecs, startSecs int) (*Session, error) {
	numSeg := (runtimeSecs + sm.SegmentSecs - 1) / sm.SegmentSecs
	if numSeg < 1 {
		numSeg = 1
	}
	id := uuid.NewString()
	dir := filepath.Join(sm.Root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session dir: %w", err)
	}
	s := &Session{
		ID: id, ItemID: itemID, Title: title, InputPath: inputPath,
		Dir: dir, SegmentSecs: sm.SegmentSecs, NumSegments: numSeg,
		lastAccess: time.Now(),
	}
	s.restartLocked(sm.baseCtx, startSecs/sm.SegmentSecs)

	sm.mu.Lock()
	sm.sessions[id] = s
	sm.mu.Unlock()
	return s, nil
}

func (sm *SessionManager) Get(id string) (*Session, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	s, ok := sm.sessions[id]
	if ok {
		s.touch()
	}
	return s, ok
}

func (sm *SessionManager) Stop(id string) {
	sm.mu.Lock()
	s, ok := sm.sessions[id]
	delete(sm.sessions, id)
	sm.mu.Unlock()
	if ok {
		s.shutdown()
	}
}

// Sessions lists active sessions (for the admin UI).
func (sm *SessionManager) Sessions() []*Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	out := make([]*Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		out = append(out, s)
	}
	return out
}

// Close stops every session; called on server shutdown.
func (sm *SessionManager) Close() {
	sm.stop()
	sm.mu.Lock()
	sessions := sm.sessions
	sm.sessions = map[string]*Session{}
	sm.mu.Unlock()
	for _, s := range sessions {
		s.shutdown()
	}
}

const sessionIdleTimeout = 5 * time.Minute

func (sm *SessionManager) janitor() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-sm.baseCtx.Done():
			return
		case <-ticker.C:
			sm.mu.Lock()
			for id, s := range sm.sessions {
				s.mu.Lock()
				idle := time.Since(s.lastAccess)
				s.mu.Unlock()
				if idle > sessionIdleTimeout {
					delete(sm.sessions, id)
					go s.shutdown()
					log.Printf("session %s: idle, cleaned up", id)
				}
			}
			sm.mu.Unlock()
		}
	}
}

func (s *Session) touch() {
	s.mu.Lock()
	s.lastAccess = time.Now()
	s.mu.Unlock()
}

// restartLocked (re)launches the transcode goroutine at the given segment.
func (s *Session) restartLocked(base context.Context, startSegment int) {
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(base)
	s.cancel = cancel
	s.startSegment = startSegment
	job := HLSJob{
		InputPath: s.InputPath, OutDir: s.Dir,
		SegmentSecs: s.SegmentSecs, StartSegment: startSegment,
	}
	go func() {
		if err := RunHLS(ctx, job); err != nil && ctx.Err() == nil {
			log.Printf("session %s: transcode: %v", s.ID, err)
		}
	}()
}

func (s *Session) shutdown() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	// Give the pipeline a beat to release file handles before removal.
	time.Sleep(200 * time.Millisecond)
	os.RemoveAll(s.Dir)
}

func (s *Session) segmentFile(idx int) string {
	return filepath.Join(s.Dir, fmt.Sprintf("seg%05d.m4s", idx))
}

// Playlist renders the VOD playlist covering the full item duration. Segment
// durations are the target duration (boundaries land on keyframes, so real
// durations drift slightly); hls.js tolerates the variance.
func (s *Session) Playlist() string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", s.SegmentSecs+1)
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	b.WriteString("#EXT-X-MAP:URI=\"init.mp4\"\n")
	for i := 0; i < s.NumSegments; i++ {
		fmt.Fprintf(&b, "#EXTINF:%d.0,\nseg%05d.m4s\n", s.SegmentSecs, i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// EnsureFile blocks until the requested playlist file exists, restarting the
// transcoder at the right offset when the player seeks outside the produced
// range. Returns the absolute path to serve.
func (s *Session) EnsureFile(ctx context.Context, name string) (string, error) {
	s.touch()
	path := filepath.Join(s.Dir, filepath.Base(name)) // Base() forbids traversal

	var segIdx = -1
	if n, err := fmt.Sscanf(filepath.Base(name), "seg%05d.m4s", &segIdx); n != 1 || err != nil {
		segIdx = -1
	}

	if segIdx >= 0 {
		s.maybeRestart(segIdx)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("segment %s not produced in time", name)
}

// maybeRestart decides whether the requested segment needs a pipeline restart:
// behind the current start, or beyond the produced head plus a small window.
func (s *Session) maybeRestart(segIdx int) {
	if _, err := os.Stat(s.segmentFile(segIdx)); err == nil {
		return // already produced
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	head := s.startSegment
	for ; head < s.NumSegments; head++ {
		if _, err := os.Stat(s.segmentFile(head)); err != nil {
			break
		}
	}
	const lookahead = 4 // segments we're willing to wait for instead of seeking
	if segIdx < s.startSegment || segIdx > head+lookahead {
		log.Printf("session %s: seek to segment %d (head %d)", s.ID, segIdx, head)
		s.restartLocked(context.Background(), segIdx)
	}
}
