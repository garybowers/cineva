package media

import (
	"errors"
	"fmt"
	"log"
	"runtime"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

// threadedDecoder is av.NewDecoder with one thing added: thread_count is set
// before the codec is opened.
//
// The vendored av package opens the decoder immediately and exposes no hook,
// and thread_count is ignored after avcodec_open2 — so a decoder built there
// is single-threaded. That costs more than any encoder setting: decoding 4K
// HEVC runs at ~1.3x realtime on one thread and ~7x with all of them, which
// is the difference between a transcode that keeps ahead of the playhead and
// one that stalls. The vendored package is third-party upstream, so the
// constructor lives here rather than as a local patch to the submodule.
type threadedDecoder struct {
	ctx   *ffmpeg.AVCodecContext
	frame *ffmpeg.AVFrame
	// hwPixFmt is the surface format this decoder outputs when decoding on
	// the GPU, or AVPixFmtNone for software. Frames then live in device
	// memory and must stay there — downloading them to scale on the CPU
	// throws away most of the benefit.
	hwPixFmt ffmpeg.AVPixelFormat
	// hwFramesCtx is the decoder's own frame pool, needed by the filter
	// graph so it can be told what kind of frames it will receive.
	hwFramesCtx *ffmpeg.AVBufferRef
}

// HardwareFrames reports the decoder's device frame pool, or nil for a
// software decoder.
func (d *threadedDecoder) HardwareFrames() *ffmpeg.AVBufferRef {
	if d == nil {
		return nil
	}
	return d.hwFramesCtx
}

// HardwarePixFmt is the surface format decoded frames arrive in, or
// AVPixFmtNone when decoding on the CPU.
func (d *threadedDecoder) HardwarePixFmt() ffmpeg.AVPixelFormat {
	if d == nil {
		return ffmpeg.AVPixFmtNone
	}
	return d.hwPixFmt
}

// newThreadedDecoder opens a software decoder.
func newThreadedDecoder(stream *ffmpeg.AVStream) (*threadedDecoder, error) {
	return newDecoder(stream, nil)
}

// newDecoder opens a decoder, on the given device when one is supplied and
// the codec can use it.
//
// Hardware decode is the larger half of the transcode cost: with encode on
// the GPU and decode still on the CPU, a 4K HEVC source burns most of a
// machine's cores on decoding alone. Moving it over keeps frames in device
// memory from decode through scale to encode, so nothing crosses the PCIe
// bus in either direction.
//
// Falls back to software for any reason at all — the codec has no hardware
// config for this device, the device will not take this profile, the open
// fails. A slow transcode is a far better outcome than none.
func newDecoder(stream *ffmpeg.AVStream, hw *HWContext) (*threadedDecoder, error) {
	if stream == nil {
		return nil, errors.New("nil stream")
	}
	codecID := stream.Codecpar().CodecId()
	codec := ffmpeg.AVCodecFindDecoder(codecID)
	if codec == nil {
		return nil, fmt.Errorf("find decoder (codec %v): %w",
			codecID, ffmpeg.WrapErr(ffmpeg.AVErrorDecoderNotFoundConst))
	}

	// There are two ways to decode on a GPU, and which is available depends
	// on how ffmpeg was built.
	//
	//  - A *standalone* hardware decoder: a separate codec such as
	//    hevc_qsv or hevc_cuvid, found by name. QSV and NVIDIA work this
	//    way, and this build has eighteen of them.
	//  - An *hwaccel*: the ordinary software decoder with a device attached,
	//    choosing a hardware surface in get_format. VAAPI works only this
	//    way — and this build reports zero hwaccels, so VAAPI cannot
	//    hardware-decode here however the code is written.
	//
	// The standalone decoder is tried first because it is the one that
	// actually exists in this build; the hwaccel path follows for builds
	// that have them.
	var standalone bool
	if hw != nil {
		if c, name := standaloneHWDecoder(codecID, hw); c != nil {
			codec, standalone = c, true
			log.Printf("hwdec: using %s", name)
		}
	}
	ctx := ffmpeg.AVCodecAllocContext3(codec)
	if ctx == nil {
		return nil, errors.New("alloc decoder context failed")
	}
	d := &threadedDecoder{ctx: ctx, hwPixFmt: ffmpeg.AVPixFmtNone}
	if _, err := ffmpeg.AVCodecParametersToContext(ctx, stream.Codecpar()); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("copy codec parameters to context: %w", err)
	}
	ctx.SetPktTimebase(stream.TimeBase())

	// Frame threading has the better speed-up but delays output by roughly
	// one frame per thread; slice threading covers codecs that lack it. Both
	// are advertised per codec, so ask for whichever this one supports.
	var threadType int
	if codec.Capabilities()&ffmpeg.AVCodecCapFrameThreads != 0 {
		threadType |= ffmpeg.FFThreadFrame
	}
	if codec.Capabilities()&ffmpeg.AVCodecCapSliceThreads != 0 {
		threadType |= ffmpeg.FFThreadSlice
	}
	if threadType != 0 {
		ctx.SetThreadType(threadType)
		ctx.SetThreadCount(decodeThreads())
	}

	// Hardware decode, when a device is available and this codec can use it.
	// Set up before open: get_format is consulted during avcodec_open2, and
	// hw_device_ctx has to be in place before the decoder negotiates.
	if standalone {
		// A standalone hardware decoder needs the device, and then emits
		// that device's surfaces directly — no get_format negotiation,
		// because the codec itself is the hardware one.
		ctx.SetHwDeviceCtx(ffmpeg.AVBufferRef_(hw.ref))
		d.hwPixFmt = hw.PixFmt
	}
	if hw != nil && !standalone {
		if surface, ok := hwDecodePixFmt(codec, hw); ok {
			d.hwPixFmt = surface
			ctx.SetHwDeviceCtx(ffmpeg.AVBufferRef_(hw.ref))
			// libavcodec offers the formats it can decode into and takes
			// the one we return. Choosing the device surface is what makes
			// the decode happen on the GPU; anything else is a CPU decode.
			ctx.SetGetFormat(func(_ *ffmpeg.AVCodecContext, formats []ffmpeg.AVPixelFormat) ffmpeg.AVPixelFormat {
				for _, f := range formats {
					if f == surface {
						return f
					}
				}
				// The device cannot take this stream — an unsupported
				// profile or bit depth. Fall back to whatever libavcodec
				// offered first, which is the software format.
				log.Printf("hwdec: device cannot decode this stream, using the CPU")
				d.hwPixFmt = ffmpeg.AVPixFmtNone
				if len(formats) > 0 {
					return formats[0]
				}
				return ffmpeg.AVPixFmtNone
			})
		}
	}

	if _, err := ffmpeg.AVCodecOpen2(ctx, codec, nil); err != nil {
		// A device that refuses the stream can fail the open outright, so
		// retry in software rather than failing the playback.
		if d.hwPixFmt != ffmpeg.AVPixFmtNone || standalone {
			log.Printf("hwdec: %s open failed (%v), retrying on the CPU", codec.Name().String(), err)
			_ = d.Close()
			return newDecoder(stream, nil)
		}
		_ = d.Close()
		return nil, fmt.Errorf("open decoder: %w", err)
	}
	if d.hwPixFmt != ffmpeg.AVPixFmtNone {
		// The pool libavcodec allocated for its output. The filter graph
		// needs it to know what it is being fed.
		d.hwFramesCtx = ctx.HwFramesCtx()
	}
	d.frame = ffmpeg.AVFrameAlloc()
	if d.frame == nil {
		_ = d.Close()
		return nil, errors.New("alloc decoder frame failed")
	}
	return d, nil
}

// decodeThreads leaves a core free so a transcode cannot starve the HTTP
// server that has to serve its own segments. Capped at 16: frame threading
// stops scaling well beyond that and each thread costs a frame of latency.
func decodeThreads() int {
	n := runtime.NumCPU() - 1
	if n < 1 {
		n = 1
	}
	if n > 16 {
		n = 16
	}
	return n
}

func (d *threadedDecoder) Decode(pkt *ffmpeg.AVPacket, fn func(*ffmpeg.AVFrame) error) error {
	if _, err := ffmpeg.AVCodecSendPacket(d.ctx, pkt); err != nil {
		return fmt.Errorf("send packet: %w", err)
	}
	return d.drain(fn)
}

func (d *threadedDecoder) Flush(fn func(*ffmpeg.AVFrame) error) error {
	if _, err := ffmpeg.AVCodecSendPacket(d.ctx, nil); err != nil {
		return fmt.Errorf("flush decoder: %w", err)
	}
	return d.drain(fn)
}

func (d *threadedDecoder) drain(fn func(*ffmpeg.AVFrame) error) error {
	for {
		if _, err := ffmpeg.AVCodecReceiveFrame(d.ctx, d.frame); err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				return nil
			}
			return fmt.Errorf("receive frame: %w", err)
		}
		err := fn(d.frame)
		ffmpeg.AVFrameUnref(d.frame)
		if err != nil {
			return err
		}
	}
}

// Raw returns the underlying decode context. Do not free it; Close owns it.
func (d *threadedDecoder) Raw() *ffmpeg.AVCodecContext { return d.ctx }

// Close frees the scratch frame and decode context. A second call is a no-op.
func (d *threadedDecoder) Close() error {
	if d.frame != nil {
		ffmpeg.AVFrameFree(&d.frame)
		d.frame = nil
	}
	if d.ctx != nil {
		d.ctx.ClearGetFormat()
		ffmpeg.AVCodecFreeContext(&d.ctx)
		d.ctx = nil
	}
	return nil
}

// Reset discards the decoder's buffered state after a seek, so the next
// packet is decoded as the start of a new sequence.
//
// Distinct from Flush, which *drains*: Flush sends a NULL packet to get the
// last frames out and leaves the decoder in its end-of-stream state, after
// which further sends fail. Seeking needs the opposite — forget what came
// before and carry on.
func (d *threadedDecoder) Reset() {
	ffmpeg.AVCodecFlushBuffers(d.ctx)
}

// hwDecodePixFmt asks a codec whether it can decode on this device, and in
// what surface format.
//
// A codec advertises its hardware configurations one at a time; each names a
// device type and the pixel format frames come back in. Only the
// HW_DEVICE_CTX method is any use here — the older HW_FRAMES_CTX and ad-hoc
// methods need the caller to allocate the pool before the decoder knows what
// size it wants, which libavcodec does better itself.
func hwDecodePixFmt(codec *ffmpeg.AVCodec, hw *HWContext) (ffmpeg.AVPixelFormat, bool) {
	if hw == nil {
		return ffmpeg.AVPixFmtNone, false
	}
	for i := 0; ; i++ {
		cfg := ffmpeg.AVCodecGetHWConfig(codec, i)
		if cfg == nil {
			return ffmpeg.AVPixFmtNone, false // ran out of configurations
		}
		if cfg.Methods()&ffmpeg.AVCodecHWConfigMethodHWDeviceCtx == 0 {
			continue
		}
		if cfg.DeviceType() == hw.DevType {
			return cfg.PixFmt(), true
		}
	}
}

// hwDecoderSuffix is the naming convention each backend's standalone
// decoders follow. VAAPI has none — it decodes only through an hwaccel.
func hwDecoderSuffix(kind HWKind) (string, bool) {
	switch kind {
	case HWQSV:
		return "qsv", true
	case HWNVENC:
		return "cuvid", true
	}
	return "", false
}

// standaloneHWDecoder looks for a dedicated hardware decoder for this codec
// on this backend — "hevc_qsv", "h264_cuvid" and so on. Returns the codec
// and the name it was found under.
func standaloneHWDecoder(id ffmpeg.AVCodecID, hw *HWContext) (*ffmpeg.AVCodec, string) {
	suffix, ok := hwDecoderSuffix(hw.Kind)
	if !ok {
		return nil, ""
	}
	base := ffmpeg.AVCodecGetName(id).String()
	if base == "" {
		return nil, ""
	}
	name := base + "_" + suffix
	cs := ffmpeg.ToCStr(name)
	codec := ffmpeg.AVCodecFindDecoderByName(cs)
	cs.Free()
	if codec == nil {
		return nil, ""
	}
	return codec, name
}

// hwDecodeAvailable reports whether this build can decode a file's video on
// a given device. Used to tell "hardware decode did not help" apart from
// "there is no hardware decode path here at all", which are very different
// findings.
func hwDecodeAvailable(path string, hw *HWContext) bool {
	if hw == nil {
		return false
	}
	in, err := av.Open(path)
	if err != nil {
		return false
	}
	defer in.Close()
	vs, err := in.BestStream(ffmpeg.AVMediaTypeVideo)
	if err != nil || vs == nil {
		return false
	}
	id := vs.Codecpar().CodecId()
	if c, _ := standaloneHWDecoder(id, hw); c != nil {
		return true
	}
	if codec := ffmpeg.AVCodecFindDecoder(id); codec != nil {
		if _, ok := hwDecodePixFmt(codec, hw); ok {
			return true
		}
	}
	return false
}

// hwDecodableCodecs lists the common video codecs this device can decode,
// by either route: a standalone decoder, or an hwaccel built into this
// ffmpeg. VAAPI has no standalone decoders, so without this an all-hwaccel
// backend looks like it cannot decode at all.
//
// The list is the codecs worth naming in a startup summary, not every codec
// ffmpeg knows — the point is to tell an operator whether the files they
// actually hold will decode on the GPU.
func hwDecodableCodecs(hw *HWContext) []string {
	if hw == nil {
		return nil
	}
	common := []struct {
		name string
		id   ffmpeg.AVCodecID
	}{
		{"h264", ffmpeg.AVCodecIdH264},
		{"hevc", ffmpeg.AVCodecIdHevc},
		{"av1", ffmpeg.AVCodecIdAV1},
		{"vp9", ffmpeg.AVCodecIdVp9},
		{"mpeg2", ffmpeg.AVCodecIdMpeg2Video},
	}
	var out []string
	for _, c := range common {
		if _, name := standaloneHWDecoder(c.id, hw); name != "" {
			out = append(out, c.name)
			continue
		}
		if dec := ffmpeg.AVCodecFindDecoder(c.id); dec != nil {
			if _, ok := hwDecodePixFmt(dec, hw); ok {
				out = append(out, c.name)
			}
		}
	}
	return out
}
