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
	// Encoders this backend offers beyond h264, keyed by codec name. Which
	// of them the hardware can actually do is a property of the chip, not
	// the build, so each is probed at detection time rather than assumed:
	// an older card has the h265 block and not the AV1 one.
	alt map[string]string
}

// Ordered by how much work they take off the CPU in practice: a discrete
// NVIDIA card first, then Intel's dedicated media engine, then VAAPI, which
// covers AMD and also Intel where Quick Sync is unavailable.
var hwCandidates = []hwCandidate{
	{HWNVENC, "NVIDIA NVENC", ffmpeg.AVHWDeviceTypeCuda, "h264_nvenc", ffmpeg.AVPixFmtCuda, []string{""},
		map[string]string{"hevc": "hevc_nvenc", "av1": "av1_nvenc"}},
	{HWQSV, "Intel Quick Sync", ffmpeg.AVHWDeviceTypeQsv, "h264_qsv", ffmpeg.AVPixFmtQsv, []string{""},
		map[string]string{"hevc": "hevc_qsv", "av1": "av1_qsv"}},
	{HWVAAPI, "AMD/Intel VAAPI", ffmpeg.AVHWDeviceTypeVaapi, "h264_vaapi", ffmpeg.AVPixFmtVaapi,
		[]string{"", "/dev/dri/renderD128", "/dev/dri/renderD129"},
		map[string]string{"hevc": "hevc_vaapi", "av1": "av1_vaapi"}},
}

// HWContext is an opened hardware device, shared by every transcode session.
// A nil *HWContext means software, so callers need no special case.
type HWContext struct {
	Kind    HWKind
	Device  string               // the path that opened, "" when the driver chose
	Encoder string               // the h264 encoder to use with it
	PixFmt  ffmpeg.AVPixelFormat // surface format the encoder consumes
	// Alt holds the other codecs this device can encode, keyed by codec
	// name ("hevc", "av1"). Only encoders present in this build are listed;
	// whether the chip itself supports one is not knowable without trying,
	// so a failure to open still falls back like any other.
	//
	// h264 remains the default output and should stay that way: it is the
	// one codec every browser decodes. These exist for clients that can do
	// better — a native app, or a future quality level — rather than to be
	// chosen automatically.
	Alt map[string]string
	// DevType is the libavformat device type this context wraps, used to
	// ask a codec whether it can decode on this device.
	DevType ffmpeg.AVHWDeviceType
	ref     *ffmpeg.AVBufferRef
}

// EncoderFor returns the encoder to use for a codec on this device, and
// whether one exists. An unknown codec, or a nil context, reports false so
// the caller falls back to software.
func (h *HWContext) EncoderFor(codec string) (string, bool) {
	if h == nil {
		return "", false
	}
	if codec == "" || codec == "h264" {
		return h.Encoder, h.Encoder != ""
	}
	name, ok := h.Alt[codec]
	return name, ok
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

// Codecs lists what this device can encode, h264 first, for display. Returns
// nil for software.
func (h *HWContext) Codecs() []string {
	if h == nil || h.Encoder == "" {
		return nil
	}
	out := []string{"h264"}
	for _, c := range []string{"hevc", "av1"} { // stable order, not map order
		if _, ok := h.Alt[c]; ok {
			out = append(out, c)
		}
	}
	return out
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
		// Naming the other codecs the device can encode makes it obvious
		// why h264 was chosen anyway: it is the compatible one, not the
		// only one available.
		extra := ""
		if codecs := found.Codecs(); len(codecs) > 1 {
			extra = " (device also encodes " + strings.Join(codecs[1:], ", ") + ")"
		}
		log.Printf("transcode: %s — using %s%s", summary, found.Encoder, extra)
		// Decode is the larger half of the cost, so whether it is on the
		// GPU is worth saying plainly rather than leaving someone to wonder
		// why a "hardware" transcode still pegs the CPU.
		if codecs := hwDecodableCodecs(found); len(codecs) > 0 {
			log.Printf("transcode: decoding on the GPU for %s", strings.Join(codecs, ", "))
		} else {
			log.Printf("transcode: decoding on the CPU — this ffmpeg has no %s decoder or hwaccel built in", found.Kind)
		}
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
			return &HWContext{
				Kind: c.kind, Device: path, Encoder: c.encoder, PixFmt: c.pixFmt,
				Alt: availableEncoders(c.alt), DevType: c.devType, ref: ref,
			}
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

// availableEncoders filters a backend's alternative encoders down to those
// this ffmpeg build actually contains. Presence in the build is necessary
// but not sufficient — whether the chip has the encode block for a codec is
// only discoverable by opening it — so a caller that picks one still has to
// handle failure, exactly as it does for the h264 encoder.
func availableEncoders(candidates map[string]string) map[string]string {
	if len(candidates) == 0 {
		return nil
	}
	out := map[string]string{}
	for codec, encoder := range candidates {
		name := ffmpeg.ToCStr(encoder)
		found := ffmpeg.AVCodecFindEncoderByName(name) != nil
		name.Free()
		if found {
			out[codec] = encoder
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
