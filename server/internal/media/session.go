package media

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math"
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

	mu          sync.Mutex
	hw          *HWContext // device this session encodes on, nil for software
	audioTrack  int        // stream index to play, -1 for the best stream
	hdr         string     // Info.HDRFormat, so a restart-at-offset job tone maps too
	burnSub     int        // image subtitle stream to paint in, 0 for none
	segSecsReal float64    // true segment length; see realSegmentSecs
	runtimeSecs int        // the item's duration, kept so State() can describe the session
	// restored marks a session rebuilt from stored state, which has no
	// transcoder running until the first segment is requested.
	restored bool
	cancel   context.CancelFunc
	// done is closed when the transcode goroutine returns. shutdown waits on
	// it before deleting the directory, so ffmpeg is never still opening a
	// file in a directory that is being removed underneath it.
	done         chan struct{}
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

	// Persist and Forget record and remove a session's description, so a
	// restart can pick up where it left off instead of discarding segments
	// that are still on disk. Hooks rather than a store dependency: the
	// media package knows how to transcode, not where anything is kept.
	// Both may be nil, in which case sessions simply do not survive.
	Persist func(SessionState)
	Forget  func(id string)

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
	// FrameRateNum/Den is the source's exact average frame rate, used to
	// state real segment durations in the playlist. Zero falls back to the
	// nominal segment length.
	FrameRateNum int
	FrameRateDen int
	// BurnSubtitle is the stream index of an image-based subtitle to paint
	// into the picture, or 0 for none. Only honoured when the video is
	// being re-encoded — nothing can be drawn on a copied stream.
	BurnSubtitle int
}

// SessionState is everything needed to re-register a session after a
// restart. The transcoder's own progress is deliberately absent: a restored
// session is idle until a segment is requested, and the restart-at-offset
// path then starts it wherever the viewer actually is.
type SessionState struct {
	ID           string
	ItemID       string
	Title        string
	InputPath    string
	Dir          string
	SegmentSecs  int
	NumSegments  int
	RuntimeSecs  int
	Quality      Quality
	AudioTrack   int
	HDR          string
	SegSecsReal  float64
	BurnSubtitle int
}

// State describes the session for persistence.
func (s *Session) State() SessionState {
	return SessionState{
		ID: s.ID, ItemID: s.ItemID, Title: s.Title, InputPath: s.InputPath,
		Dir: s.Dir, SegmentSecs: s.SegmentSecs, NumSegments: s.NumSegments,
		RuntimeSecs: s.runtimeSecs, Quality: s.Quality,
		AudioTrack: s.audioTrack, HDR: s.hdr, SegSecsReal: s.segSecsReal,
		BurnSubtitle: s.burnSub,
	}
}

// Restore re-registers sessions from stored state, skipping any whose
// segment directory has gone. Nothing is transcoded here: a restored session
// starts work only when a segment is asked for.
//
// Returns the directories it took ownership of, so the orphan sweep leaves
// them alone — they are not orphans, they are the point.
func (sm *SessionManager) Restore(states []SessionState) []string {
	kept := make([]string, 0, len(states))
	for _, st := range states {
		if fi, err := os.Stat(st.Dir); err != nil || !fi.IsDir() {
			// The directory went (swept, or the disk was cleared), so the
			// session has nothing to offer; forget it.
			if sm.Forget != nil {
				sm.Forget(st.ID)
			}
			continue
		}
		hw := sm.hardware()
		s := &Session{
			hdr: st.HDR, segSecsReal: st.SegSecsReal, audioTrack: st.AudioTrack,
			burnSub: st.BurnSubtitle,
			ID:      st.ID, ItemID: st.ItemID, Title: st.Title, InputPath: st.InputPath,
			Dir: st.Dir, SegmentSecs: st.SegmentSecs, NumSegments: st.NumSegments,
			runtimeSecs: st.RuntimeSecs,
			Quality:     st.Quality,
			Accel:       hw.Name(),
			hw:          hw,
			lastAccess:  time.Now(),
			// No transcoder is running, and restartLocked has not been
			// called — the first segment request starts one at the right
			// offset.
			restored: true,
		}
		sm.mu.Lock()
		sm.sessions[s.ID] = s
		sm.mu.Unlock()
		kept = append(kept, st.Dir)
	}
	if len(kept) > 0 {
		log.Printf("transcode: restored %d session(s) from the previous run", len(kept))
	}
	return kept
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
		hdr:         o.HDR,
		burnSub:     o.BurnSubtitle,
		segSecsReal: realSegmentSecs(sm.SegmentSecs, o.FrameRateNum, o.FrameRateDen),
		audioTrack:  audioTrack,
		ID:          id, ItemID: itemID, Title: title, InputPath: inputPath,
		Dir: dir, SegmentSecs: sm.SegmentSecs, NumSegments: numSeg,
		runtimeSecs: runtimeSecs,
		Quality:     q,
		Accel:       hw.Name(),
		hw:          hw,
		lastAccess:  time.Now(),
	}
	s.restartLocked(sm.baseCtx, startSecs/sm.SegmentSecs)

	sm.mu.Lock()
	sm.sessions[id] = s
	sm.mu.Unlock()
	if sm.Persist != nil {
		sm.Persist(s.State())
	}
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
		// Deliberately ended, so there is nothing worth restoring; the
		// directory has gone with it.
		if sm.Forget != nil {
			sm.Forget(id)
		}
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
//
// Transcoders are halted but their segments are kept, so the next start can
// restore the sessions and pick up where they stopped. Anything genuinely
// finished has already been removed by the idle reap or an explicit stop,
// and anything left behind by a crash is swept at startup.
func (sm *SessionManager) Close() {
	sm.stop()
	sm.mu.Lock()
	sessions := sm.sessions
	sm.sessions = map[string]*Session{}
	sm.mu.Unlock()
	for _, s := range sessions {
		s.stopTranscode()
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
					if sm.Forget != nil {
						sm.Forget(id)
					}
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

// A viewer who has gone stops asking for segments. Until the janitor reaps
// the session minutes later, the transcoder would otherwise keep working all
// the way to throttleAheadSecs past the last segment anyone wanted — on a 4K
// source that is a real burst of CPU for a stream nobody is watching.
//
// So the look-ahead collapses once requests dry up. A viewer who merely
// paused loses nothing but buffer depth, which refills the moment they ask
// for the next segment; a viewer who closed the tab stops costing anything
// within idleAheadAfter rather than within sessionIdleTimeout.
const (
	idleAheadAfter = 20 * time.Second
	idleAheadSecs  = 15
)

// aheadSecs is how far past the last requested segment the transcoder may
// run right now.
func (s *Session) aheadSecs() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	ahead := int64(throttleAheadSecs)
	if time.Since(s.lastAccess) > idleAheadAfter {
		ahead = idleAheadSecs
	}
	return int64(s.lastReq*s.SegmentSecs) + ahead
}

// restartLocked (re)launches the transcode goroutine at the given segment.
func (s *Session) restartLocked(base context.Context, startSegment int) {
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(base)
	s.cancel = cancel
	done := make(chan struct{})
	s.done = done
	s.startSegment = startSegment
	s.lastReq = startSegment
	job := HLSJob{
		InputPath: s.InputPath, OutDir: s.Dir,
		SegmentSecs: s.SegmentSecs, StartSegment: startSegment,
		VideoHeight: s.Quality.Height, VideoBitrateKbps: s.Quality.BitrateKbps,
		HDR:          s.hdr,
		BurnSubtitle: s.burnSub,
		HW:           s.hw,
		AudioTrack:   s.audioTrack,
		// RunHLS reports the path it actually built, so a device that opens
		// but cannot allocate frames is not still advertised as hardware.
		OnAccel: func(name string) {
			s.mu.Lock()
			s.Accel = name
			s.mu.Unlock()
		},
		Throttle: func(ctx context.Context, posSecs int64) error {
			for {
				if posSecs <= s.aheadSecs() {
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
		defer close(done)
		if err := RunHLS(ctx, job); err != nil && ctx.Err() == nil {
			log.Printf("session %s: transcode: %v", s.ID, err)
		}
	}()
}

// shutdown stops the transcoder and discards the session's segments. For a
// session that is finished with: an idle reap, an explicit stop, or being
// replaced by a new one for the same item.
func (s *Session) shutdown() {
	s.stopTranscode()
	os.RemoveAll(s.Dir)
}

// stopTranscode halts the transcoder but leaves the segments where they are.
// Used on server shutdown: the work is done and sitting on disk, and the
// next start restores the session and carries on from it. Deleting here
// would throw away the very thing that makes a session worth persisting.
func (s *Session) stopTranscode() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return // restored session: nothing was ever started
	}
	// Wait for the transcoder to actually exit rather than sleeping and
	// hoping. This used to be a flat 200ms, which is longer than ffmpeg
	// needs to shut down and shorter than it needs to *start* — so a session
	// superseded moments after it began had its directory deleted while
	// ffmpeg was still opening init.mp4, which it then reported as
	// "Failed to open segment". Media on a network share made that the
	// common case rather than the rare one.
	select {
	case <-done:
	case <-time.After(transcodeStopTimeout):
		log.Printf("session %s: transcoder did not stop within %s", s.ID, transcodeStopTimeout)
	}
}

// transcodeStopTimeout bounds the wait above. A transcoder that ignores its
// cancelled context for this long is wedged, and holding up the caller — a
// shutdown, or the next session for the same item — helps nobody.
const transcodeStopTimeout = 5 * time.Second

func (s *Session) segmentFile(idx int) string {
	return filepath.Join(s.Dir, fmt.Sprintf("seg%05d.m4s", idx))
}

// realSegmentSecs is how long a segment actually lasts.
//
// The encoder's GOP is a whole number of frames — `round(segmentSecs * fps)`
// — so at 23.976 fps a nominal six-second segment is really 144 frames, or
// 6.006 seconds. Writing the nominal 6.0 into every EXTINF loses six
// milliseconds a segment, which over a two-hour film is about seven seconds
// of drift between the player's idea of the time and the stream's. Stating
// the true duration costs nothing and removes the drift entirely.
//
// A frame rate we do not know falls back to the nominal length.
func realSegmentSecs(segmentSecs, frNum, frDen int) float64 {
	frames := gopFrames(segmentSecs, frNum, frDen)
	if frames <= 0 || frNum <= 0 || frDen <= 0 {
		return float64(segmentSecs)
	}
	fps := float64(frNum) / float64(frDen)
	return float64(frames) / fps
}

// gopFrames is how many frames make up one segment: the encoder's GOP, and
// the number realSegmentSecs converts back into a duration. One definition,
// used by both, so the keyframe interval and the playlist cannot disagree
// about how long a segment is.
func gopFrames(segmentSecs, frNum, frDen int) int {
	if segmentSecs <= 0 || frNum <= 0 || frDen <= 0 {
		return 0
	}
	fps := float64(frNum) / float64(frDen)
	n := int(math.Round(float64(segmentSecs) * fps))
	if n < 1 {
		n = 1
	}
	return n
}

// Playlist renders the VOD playlist covering the full item duration.
//
// EXTINF is the real segment duration (see realSegmentSecs) rather than the
// nominal one. On the re-encode path that is exact, because the GOP is
// forced to that length. On a remux the boundaries land on the source's own
// keyframes, which we do not control, so it remains the best estimate
// available — hls.js tolerates the variance.
func (s *Session) Playlist() string {
	var b strings.Builder
	dur := s.segSecsReal
	if dur <= 0 {
		dur = float64(s.SegmentSecs)
	}
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", int(math.Ceil(dur))+1)
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	b.WriteString("#EXT-X-MAP:URI=\"init.mp4\"\n")
	for i := 0; i < s.NumSegments; i++ {
		fmt.Fprintf(&b, "#EXTINF:%.6f,\nseg%05d.m4s\n", dur, i)
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

	// A session restored after a restart has segments on disk but nothing
	// producing more. The look-ahead test below would see the requested
	// segment sitting just past the last one written and decide no restart
	// was needed — and then nothing would ever write it. The first request
	// always starts a transcoder.
	if s.restored {
		s.restored = false
		log.Printf("session %s: resuming after restart at segment %d", s.ID, segIdx)
		s.restartLocked(context.Background(), segIdx)
		return
	}

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
