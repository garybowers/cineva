package media

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Quality selects the video treatment for a session. Zero value = copy the
// video stream (remux). Height > 0 = re-encode at that height/bitrate.
type Quality struct {
	Label       string `json:"label"` // "auto" (remux) or "1080p"/"720p"/...
	Height      int    `json:"height,omitempty"`
	BitrateKbps int    `json:"bitrateKbps,omitempty"`
}

// Session is one HLS playback session: a temp dir of segments plus the
// transcode goroutine currently filling it. Seeking past produced segments
// cancels the goroutine and restarts it at the requested offset.
type Session struct {
	ID          string  `json:"id"`
	ItemID      string  `json:"itemId"`
	Title       string  `json:"title"`
	InputPath   string  `json:"-"`
	Dir         string  `json:"-"`
	SegmentSecs int     `json:"segmentSecs"`
	NumSegments int     `json:"numSegments"`
	Quality     Quality `json:"quality"`
	// Accel names the path this session's pipeline was built with
	// ("vaapi (/dev/dri/renderD128)", "none"), so the admin Activity page
	// shows whether a stream is costing GPU or CPU.
	Accel string `json:"accel"`

	mu           sync.Mutex
	hw           *HWContext // device this session encodes on, nil for software
	audioTrack   int        // stream index to play, -1 for the best stream
	hdr          string     // Info.HDRFormat, so a restart-at-offset job tone maps too
	cancel       context.CancelFunc
	startSegment int
	lastReq      int // highest segment index a client asked for
	lastAccess   time.Time
	initBytes    []byte // cached init.mp4; restarts rewrite the file mid-flight
}

type SessionManager struct {
	Root        string
	SegmentSecs int
	// HW is the hardware transcoder detected at startup, or nil when none
	// is usable. The device stays open either way; the admin switch only
	// decides whether new sessions use it, so toggling needs no restart
	// and no re-probe.
	HW        *HWContext
	hwEnabled atomic.Bool

	// MaxBytes caps the total size of the segment cache; the janitor evicts
	// least-recently-used session directories to stay under it. Zero
	// disables the cap.
	MaxBytes int64

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
		MaxBytes:    DefaultTranscodeMaxBytes,
	}
	sm.hwEnabled.Store(true) // the admin switch defaults to on
	go sm.janitor()
	return sm
}

// SetHWEnabled flips the admin switch. Running sessions keep the pipeline
// they were built with; the next one picks up the change.
func (m *SessionManager) SetHWEnabled(on bool) { m.hwEnabled.Store(on) }

// hardware returns the device a new session should use: nil when none was
// detected, or when an admin has switched hardware transcoding off.
func (m *SessionManager) hardware() *HWContext {
	if !m.hwEnabled.Load() {
		return nil
	}
	return m.HW
}

// Start creates a session and begins transcoding from the given position.
// Any existing session for the same item is stopped first — replaying or
// changing quality must not stack transcoders (single-user until milestone 4).
// StartOpts describes the playback a session is being started for. It is a
// struct rather than a positional list because the fields are mostly strings
// and ints in adjacent runs, which is exactly the shape that silently
// transposes when someone inserts an argument.
type StartOpts struct {
	ItemID      string
	Title       string
	InputPath   string
	RuntimeSecs int
	StartSecs   int
	Quality     Quality
	AudioTrack  int
	// HDR is Info.HDRFormat ("pq", "hlg" or ""), used to tone map when the
	// video is re-encoded. See HLSJob.HDR.
	HDR string
}

func (sm *SessionManager) Start(o StartOpts) (*Session, error) {
	itemID, title, inputPath := o.ItemID, o.Title, o.InputPath
	runtimeSecs, startSecs, q, audioTrack := o.RuntimeSecs, o.StartSecs, o.Quality, o.AudioTrack
	sm.mu.Lock()
	var stale []*Session
	for id, existing := range sm.sessions {
		if existing.ItemID == itemID {
			delete(sm.sessions, id)
			stale = append(stale, existing)
		}
	}
	sm.mu.Unlock()
	for _, s := range stale {
		s.shutdown()
	}

	numSeg := (runtimeSecs + sm.SegmentSecs - 1) / sm.SegmentSecs
	if numSeg < 1 {
		numSeg = 1
	}
	id := uuid.NewString()
	dir := filepath.Join(sm.Root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session dir: %w", err)
	}
	// Resolved once, at session start: the admin switch can flip while this
	// session runs, but its pipeline is already built, so the device bound
	// here stays correct for the life of the session. Accel starts as the
	// intended path and RunHLS corrects it if the device turns out to be
	// unusable and it falls back to software.
	hw := sm.hardware()
	s := &Session{
		hdr:        o.HDR,
		audioTrack: audioTrack,
		ID:         id, ItemID: itemID, Title: title, InputPath: inputPath,
		Dir: dir, SegmentSecs: sm.SegmentSecs, NumSegments: numSeg,
		Quality:    q,
		Accel:      hw.Name(),
		hw:         hw,
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
			// Live session directories, which must survive eviction even if
			// they are the largest thing in the cache.
			live := make(map[string]bool, len(sm.sessions))
			for id := range sm.sessions {
				live[id] = true
			}
			sm.mu.Unlock()
			if n, freed := enforceCap(sm.Root, sm.MaxBytes, live); n > 0 {
				log.Printf("transcode cache over %s: evicted %d session(s), freed %s",
					humanBytes(sm.MaxBytes), n, humanBytes(freed))
			}
		}
	}
}

// humanBytes formats a byte count for a log line.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

func (s *Session) touch() {
	s.mu.Lock()
	s.lastAccess = time.Now()
	s.mu.Unlock()
}

// throttleAheadSecs: how far past the playhead the transcoder may run before
// pausing. Bounds disk usage — a full episode no longer lands on disk unless
// it is actually watched.
const throttleAheadSecs = 120

// restartLocked (re)launches the transcode goroutine at the given segment.
func (s *Session) restartLocked(base context.Context, startSegment int) {
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(base)
	s.cancel = cancel
	s.startSegment = startSegment
	s.lastReq = startSegment
	job := HLSJob{
		InputPath: s.InputPath, OutDir: s.Dir,
		SegmentSecs: s.SegmentSecs, StartSegment: startSegment,
		VideoHeight: s.Quality.Height, VideoBitrateKbps: s.Quality.BitrateKbps,
		HDR:        s.hdr,
		HW:         s.hw,
		AudioTrack: s.audioTrack,
		// RunHLS reports the path it actually built, so a device that opens
		// but cannot allocate frames is not still advertised as hardware.
		OnAccel: func(name string) {
			s.mu.Lock()
			s.Accel = name
			s.mu.Unlock()
		},
		Throttle: func(ctx context.Context, posSecs int64) error {
			for {
				s.mu.Lock()
				allowed := int64(s.lastReq*s.SegmentSecs + throttleAheadSecs)
				s.mu.Unlock()
				if posSecs <= allowed {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(500 * time.Millisecond):
				}
			}
		},
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

// Init returns the session's init segment. The muxer rewrites init.mp4 on
// every seek-restart, so serving the file directly races a restart and can
// hand the player a truncated init (which it then caches — the
// "failed to generate initSegment" bug). Instead the first complete init is
// cached in memory: init content depends only on stream parameters, so it is
// identical across restarts.
func (s *Session) Init(ctx context.Context) ([]byte, error) {
	s.touch()
	s.mu.Lock()
	cached := s.initBytes
	s.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	// The init is guaranteed complete once the first segment of the current
	// run exists (the muxer writes init fully before any segment completes).
	deadline := time.Now().Add(30 * time.Second)
	initPath := filepath.Join(s.Dir, "init.mp4")
	for time.Now().Before(deadline) {
		s.mu.Lock()
		first := s.segmentFile(s.startSegment)
		s.mu.Unlock()
		if _, err := os.Stat(first); err == nil {
			data, err := os.ReadFile(initPath)
			if err != nil {
				return nil, fmt.Errorf("read init: %w", err)
			}
			// Sessions preserve source timestamps (segment N carries pts
			// ~N*segmentSecs, matching the playlist), so the muxer's edit
			// list — an "empty edit" spanning the whole start offset — is
			// wrong for us. Firefox honours edit lists in MSE and shifts the
			// buffered range far past the playhead (silent stall); Chrome
			// ignores them. Strip edts so both agree with the playlist.
			data = stripEdts(data)
			data = forceHVC1(data)
			s.mu.Lock()
			if s.initBytes == nil {
				s.initBytes = data
			}
			data = s.initBytes
			s.mu.Unlock()
			return data, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("init segment not produced in time")
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
		s.mu.Lock()
		if segIdx > s.lastReq {
			s.lastReq = segIdx // advances the throttle window
		}
		s.mu.Unlock()
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

// forceHVC1 rewrites an HEVC sample entry from hev1 to hvc1 in a written
// init segment.
//
// The two differ in where parameter sets may live: hvc1 keeps them in the
// hvcC box, hev1 also allows them in-band. Browsers are far happier with
// hvc1 — a copied HEVC stream labelled hev1 is a common cause of a player
// that loads segments and renders nothing. Setting codec_tag on the output
// stream is the documented way to ask for it and does reach
// avformat_write_header, but the HLS muxer writes hev1 into init.mp4
// regardless, so the four bytes are corrected here, next to stripEdts which
// patches the same file for the same kind of reason.
//
// Only the first occurrence is touched: that is the sample entry in stsd.
// A file with no hev1 in it is returned unchanged.
func forceHVC1(data []byte) []byte {
	return bytes.Replace(data, []byte("hev1"), []byte("hvc1"), 1)
}
