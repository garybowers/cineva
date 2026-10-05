package media

import (
	"errors"
	"fmt"
	"log"
	"strings"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// Hardware-accelerated transcoding, detected at runtime.
//
// Cineva ships one static ffmpeg for every machine it might run on, so the
// vendor cannot be a build-time choice: the same binary has to find NVENC on
// one box, Quick Sync on another, VAAPI on a third, and fall back to software
// where there is nothing. Detection therefore asks ffmpeg itself — can this
// device be opened, does this encoder exist — rather than inspecting the host.
//
// Every path degrades to software rather than failing a playback: a machine
// with no GPU, a container without /dev/dri passed through, or a driver that
// opens but cannot encode all end up transcoding on the CPU.

// HWKind names a hardware transcoding backend.
type HWKind string

const (
	HWNone  HWKind = "none"
	HWNVENC HWKind = "nvenc" // NVIDIA, through a CUDA device
	HWQSV   HWKind = "qsv"   // Intel Quick Sync
	HWVAAPI HWKind = "vaapi" // Intel and AMD on Linux
)

// hwCandidate is one backend worth trying, with the encoder that proves it is
// usable and the device paths to attempt.
type hwCandidate struct {
	kind    HWKind
	label   string // vendor name as an operator would recognise it
	devType ffmpeg.AVHWDeviceType
	encoder string               // must exist in this build, or the backend is not usable
	pixFmt  ffmpeg.AVPixelFormat // the surface format frames live in on this backend
	devices []string             // tried in order; "" lets the driver pick
}

// Ordered by how much work they take off the CPU in practice: a discrete
// NVIDIA card first, then Intel's dedicated media engine, then VAAPI, which
// covers AMD and also Intel where Quick Sync is unavailable.
var hwCandidates = []hwCandidate{
	{HWNVENC, "NVIDIA NVENC", ffmpeg.AVHWDeviceTypeCuda, "h264_nvenc", ffmpeg.AVPixFmtCuda, []string{""}},
	{HWQSV, "Intel Quick Sync", ffmpeg.AVHWDeviceTypeQsv, "h264_qsv", ffmpeg.AVPixFmtQsv, []string{""}},
	{HWVAAPI, "AMD/Intel VAAPI", ffmpeg.AVHWDeviceTypeVaapi, "h264_vaapi", ffmpeg.AVPixFmtVaapi, []string{"", "/dev/dri/renderD128", "/dev/dri/renderD129"}},
}

// HWContext is an opened hardware device, shared by every transcode session.
// A nil *HWContext means software, so callers need no special case.
type HWContext struct {
	Kind    HWKind
	Device  string               // the path that opened, "" when the driver chose
	Encoder string               // the h264 encoder to use with it
	PixFmt  ffmpeg.AVPixelFormat // surface format the encoder consumes
	ref     *ffmpeg.AVBufferRef
}

// NewFrames builds a frame pool on the device, in the surface format this
// backend encodes from, backed by NV12 in system memory. Software-decoded
// frames are uploaded into it a frame at a time.
//
// initialPoolSize has to cover every frame in flight at once: the encoder
// holds a few for lookahead and reordering while the filter graph fills the
// next. Too small and AVHWFrameGetBuffer starts failing mid-stream.
func (h *HWContext) NewFrames(width, height int) (*ffmpeg.AVBufferRef, error) {
	if h == nil {
		return nil, errors.New("no hardware context")
	}
	ref := ffmpeg.AVHWFrameCtxAlloc(h.ref)
	if ref == nil {
		return nil, errors.New("allocate hardware frames context")
	}
	fc := ffmpeg.ToAVHWFramesContext(ref.Data())
	fc.SetFormat(h.PixFmt)
	fc.SetSwFormat(ffmpeg.AVPixFmtNv12)
	fc.SetWidth(width)
	fc.SetHeight(height)
	fc.SetInitialPoolSize(32)
	if _, err := ffmpeg.AVHWFrameCtxInit(ref); err != nil {
		ffmpeg.AVBufferUnref(&ref)
		return nil, fmt.Errorf("initialise hardware frames context: %w", err)
	}
	return ref, nil
}

// Raw returns the device context to hand to a codec. Safe on nil.
func (h *HWContext) Raw() *ffmpeg.AVBufferRef {
	if h == nil {
		return nil
	}
	return h.ref
}

// Name describes the backend for logs and the admin UI.
func (h *HWContext) Name() string {
	if h == nil {
		return string(HWNone)
	}
	if h.Device == "" {
		return string(h.Kind)
	}
	return string(h.Kind) + " (" + h.Device + ")"
}

func (h *HWContext) Close() {
	if h != nil && h.ref != nil {
		ffmpeg.AVBufferUnref(&h.ref)
		h.ref = nil
	}
}

// encoderExists reports whether this ffmpeg build carries the named encoder.
func encoderExists(name string) bool {
	n := ffmpeg.ToCStr(name)
	defer n.Free()
	return ffmpeg.AVCodecFindEncoderByName(n) != nil
}

// openHWDevice tries one device path, returning nil when it cannot be opened.
func openHWDevice(devType ffmpeg.AVHWDeviceType, path string) *ffmpeg.AVBufferRef {
	var ref *ffmpeg.AVBufferRef
	var dev *ffmpeg.CStr
	if path != "" {
		dev = ffmpeg.ToCStr(path)
		defer dev.Free()
	}
	if _, err := ffmpeg.AVHWDeviceCtxCreate(&ref, devType, dev, nil, 0); err != nil {
		return nil
	}
	return ref
}

// DetectHW finds a usable hardware transcoder. pref is the configured
// preference: "auto" tries every backend in order, "none" disables hardware
// entirely, and a specific name ("vaapi", "nvenc", "qsv") tries only that one
// — useful on a host with several GPUs, or to pin behaviour in a deployment.
//
// It returns nil when nothing is usable, which is a supported outcome, not an
// error: the caller transcodes on the CPU.
func DetectHW(pref string) *HWContext {
	pref = strings.ToLower(strings.TrimSpace(pref))
	if pref == "" {
		pref = "auto"
	}
	if pref == string(HWNone) || pref == "off" || pref == "false" {
		log.Printf("transcode: hardware acceleration disabled by config — encoding on the CPU")
		return nil
	}

	// Probing a device that is not there makes ffmpeg log its own failures
	// ("Cannot load libcuda.so.1", "Failed to initialise VAAPI connection"),
	// which read like errors on a machine that simply has no NVIDIA card.
	// They are expected, so they are silenced and the outcome is reported
	// once, per vendor, below.
	prev, _ := ffmpeg.AVLogGetLevel()
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogQuiet)

	var found *HWContext
	results := make([]string, 0, len(hwCandidates))
	for _, c := range hwCandidates {
		switch {
		case pref != "auto" && pref != string(c.kind):
			results = append(results, c.label+": not requested")
		case found != nil:
			results = append(results, c.label+": not tried")
		case !encoderExists(c.encoder):
			results = append(results, c.label+": unavailable in this build")
		case c.kind == HWVAAPI && !libvaPresent():
			results = append(results, c.label+": libva not installed")
		default:
			if hw := c.open(); hw != nil {
				found = hw
				results = append(results, c.label+": detected"+deviceSuffix(hw.Device))
			} else {
				results = append(results, c.label+": not detected")
			}
		}
	}
	ffmpeg.AVLogSetLevel(prev)

	summary := strings.Join(results, " · ")
	if found != nil {
		log.Printf("transcode: %s — using %s", summary, found.Encoder)
	} else {
		log.Printf("transcode: %s — no hardware acceleration, encoding on the CPU", summary)
	}
	return found
}

// open tries each device path for a candidate, returning the first that
// opens.
func (c hwCandidate) open() *HWContext {
	for _, path := range c.devices {
		if ref := openHWDevice(c.devType, path); ref != nil {
			return &HWContext{Kind: c.kind, Device: path, Encoder: c.encoder, PixFmt: c.pixFmt, ref: ref}
		}
	}
	return nil
}

func deviceSuffix(device string) string {
	if device == "" {
		return ""
	}
	return " (" + device + ")"
}

// ValidHWPref reports whether a configured value names a backend we know, so
// `config validate` can reject a typo instead of silently using software.
func ValidHWPref(pref string) error {
	switch strings.ToLower(strings.TrimSpace(pref)) {
	case "", "auto", string(HWNone), "off", "false",
		string(HWNVENC), string(HWQSV), string(HWVAAPI):
		return nil
	}
	names := []string{"auto", "none"}
	for _, c := range hwCandidates {
		names = append(names, string(c.kind))
	}
	return fmt.Errorf("unknown transcode.hwaccel %q (want one of: %s)", pref, strings.Join(names, ", "))
}
