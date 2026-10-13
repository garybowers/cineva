package media

import (
	"fmt"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// videoGraph turns decoded frames into frames the encoder takes. It serves
// every re-encode — software decode as well as hardware — and builds its
// graph from the first frame, because only then is everything about the
// source known: the decoder's frames context on a hardware decode, and the
// colour space and range on any decode (see newParsedGraph for why those
// have to be declared).
//
// On a hardware decode it picks the cheapest route that has been verified,
// trying each in turn and falling back to the next when a graph will not
// build:
//
//  1. Kept on the device end to end (encodeOnDevice): scale_vaapi straight
//     into the hardware encoder, nothing copied back. Only when nothing
//     needs the pixels in system memory — no subtitle to burn in, no CPU
//     tone map — and only from 8-bit surfaces (see deviceEncodeSpec).
//     Measured on an 8-bit H.264 episode against route 2, the picture
//     bit-identical after the encoder: 1080p to 720p 19.7x -> 23.8x
//     realtime with 47% less CPU, 1080p at 1080p 13.4x -> 16.7x with 63%
//     less (TestDeviceEncodeCost).
//  2. Scaled on the device, then downloaded: what subtitles, the CPU tone
//     map and libx264 need. Downloading a 4K 10-bit surface is 24 MB a
//     frame, and on an AMD APU the copy alone made a GPU decode into
//     libx264 slower than decoding on the CPU (0.68x realtime against
//     1.05x, measured side by side). Scaling first leaves a quarter of
//     that to copy, and the measured result, 4K HEVC to 1080p:
//     - hardware encode: 3.25x -> 7.0x realtime, 20.6 s -> 4.5 s of CPU
//     (SDR); 2.14x -> 2.87x with the HDR tone map, which stays on the CPU
//     - libx264: 4.17x realtime against 3.07x for an all-CPU transcode,
//     with 62% less CPU (39% with the tone map, 2.11x against 1.67x)
//  3. Downloaded at full size and scaled on the CPU, exactly as a software
//     decode is.
//
// Device scaling is only attempted where it has been verified — VAAPI, with
// scale_vaapi in the build — and only for the standard chain.
type videoGraph struct {
	spec       string // the CPU chain, ending in the encoder's format
	outW, outH int
	timeBase   *ffmpeg.AVRational
	scale      bool // try scaling on the device
	// encodeOnDevice tries handing scaled surfaces straight to the
	// hardware encoder. Only set when the encoder is a hardware one and
	// nothing between decoder and encoder needs system memory.
	encodeOnDevice bool
	kind           HWKind

	graph     *parsedGraph
	onDevice  bool            // the graph takes device surfaces itself
	deviceOut bool            // the graph emits device surfaces, for the encoder
	sw        *ffmpeg.AVFrame // download target when scaling on the CPU
}

// Push runs one decoded frame through to fn. The graph is built on the
// first frame, because only then is the decoder's frames context known.
func (p *videoGraph) Push(frame *ffmpeg.AVFrame, fn func(*ffmpeg.AVFrame) error) error {
	if p.graph == nil {
		if err := p.build(frame); err != nil {
			return err
		}
	}
	in := frame
	if !p.onDevice {
		var err error
		if in, err = downloadFrame(&p.sw, frame); err != nil {
			return err
		}
	}
	if err := p.graph.Push(in); err != nil {
		return err
	}
	return p.graph.Pull(fn)
}

// Flush drains the graph at end of stream.
func (p *videoGraph) Flush(fn func(*ffmpeg.AVFrame) error) error {
	if p.graph == nil {
		return nil // nothing was decoded, so nothing to drain
	}
	if err := p.graph.Push(nil); err != nil {
		return err
	}
	return p.graph.Pull(fn)
}

func (p *videoGraph) Close() {
	if p.graph != nil {
		p.graph.Close()
		p.graph = nil
	}
	if p.sw != nil {
		ffmpeg.AVFrameFree(&p.sw)
	}
}

func (p *videoGraph) build(frame *ffmpeg.AVFrame) error {
	if frame.HwFramesCtx() == nil {
		// A software decode, or a hardware decoder that handed over to
		// software before its first frame.
		g, err := newParsedGraph(p.spec, frame, p.timeBase)
		if err != nil {
			return fmt.Errorf("hls video filter: %w", err)
		}
		p.graph = g
		return nil
	}
	if p.scale {
		swFormat := ffmpeg.ToAVHWFramesContext(frame.HwFramesCtx().Data()).SwFormat()
		if p.encodeOnDevice {
			spec, err := deviceEncodeSpec(swFormat, frame, p.outW, p.outH)
			var g *parsedGraph
			if err == nil {
				g, err = newParsedGraph(spec, frame, p.timeBase)
			}
			if err == nil {
				p.graph, p.onDevice, p.deviceOut = g, true, true
				logger.Debug("decoding, scaling and encoding on the GPU", "kind", p.kind, "width", p.outW, "height", p.outH)
				return nil
			}
			logger.Debug("frames cannot stay on the GPU, copying them back", "kind", p.kind, "reason", err)
		}
		spec, err := deviceScaleSpec(swFormat, p.outW, p.outH)
		var g *parsedGraph
		if err == nil {
			g, err = newParsedGraph(spec+","+p.spec, frame, p.timeBase)
		}
		if err == nil {
			p.graph, p.onDevice = g, true
			logger.Debug("decoding and scaling on the GPU", "kind", p.kind, "width", p.outW, "height", p.outH)
			return nil
		}
		logger.Warn("scaling on the GPU unavailable, scaling on the CPU", "kind", p.kind, "err", err)
	}
	sw, err := downloadFrame(&p.sw, frame)
	if err != nil {
		return err
	}
	if p.graph, err = newParsedGraph(p.spec, sw, p.timeBase); err != nil {
		return fmt.Errorf("hls download filter: %w", err)
	}
	logger.Debug("decoding on the GPU, scaling on the CPU", "kind", p.kind)
	return nil
}

// deviceEncodeSpec scales on a VAAPI device and leaves the result there, as
// NV12 surfaces the hardware encoder takes directly.
//
// Only from NV12 surfaces, i.e. 8-bit 4:2:0 sources, which are most of a
// library; there the result is bit-identical to the download path. From
// P010 the device would also have to reduce the bit depth, and its
// reduction is not the one swscale and reference ffmpeg do: it came out
// 0.05-0.27 levels darker than reducing on the CPU across the samples
// here (a 4K PQ film at five points, a 10-bit SDR clip). Small, but not
// shown to be harmless, so 10-bit sources keep the download path.
//
// The output range is stated, as the source's own. It changes nothing about
// the picture — limited to limited, full to full — but it stops scale_vaapi
// passing a same-size frame straight through, which would hand the encoder
// the decoder's own surfaces: sized to the codec's alignment rather than
// the picture (1920x1088 for 1080p), and not something the encoder has been
// checked with. A same-size source that states no range keeps the download
// path for that reason.
func deviceEncodeSpec(swFormat ffmpeg.AVPixelFormat, src *ffmpeg.AVFrame, w, h int) (string, error) {
	if swFormat != ffmpeg.AVPixFmtNv12 {
		return "", fmt.Errorf("surfaces are %s, not nv12", ffmpeg.AVGetPixFmtName(swFormat).String())
	}
	spec := fmt.Sprintf("scale_vaapi=w=%d:h=%d:format=nv12", w, h)
	switch src.ColorRange() {
	case ffmpeg.AVColRangeMpeg:
		spec += ":out_range=limited"
	case ffmpeg.AVColRangeJpeg:
		spec += ":out_range=full"
	default:
		if src.Width() == w && src.Height() == h {
			return "", fmt.Errorf("same-size source states no colour range")
		}
	}
	return spec, nil
}

// deviceScaleSpec scales on a VAAPI device and brings the result back to
// system memory, in the surfaces' own format (swFormat: P010 for a 10-bit
// source, NV12 for 8-bit). Only the size changes on the device; the bit
// depth is left for the CPU chain to reduce, as it does on a software
// decode, which is the reduction reference ffmpeg makes (see
// deviceEncodeSpec for how far the device's own differs).
func deviceScaleSpec(swFormat ffmpeg.AVPixelFormat, w, h int) (string, error) {
	f := ffmpeg.AVGetPixFmtName(swFormat).String()
	if f == "" {
		return "", fmt.Errorf("device surfaces have no software format (%d)", swFormat)
	}
	return fmt.Sprintf("scale_vaapi=w=%d:h=%d:format=%s,hwdownload,format=%s", w, h, f, f), nil
}

// encodesFromDevice reports whether scaled surfaces can go straight from the
// device scaler to this device's encoder without a copy back. VAAPI only,
// for the same reason as scalesOnDevice.
func (h *HWContext) encodesFromDevice() bool {
	return h.scalesOnDevice() && !h.noDeviceEncode
}

// scalesOnDevice reports whether frames decoded on this device can be scaled
// there before they are downloaded. VAAPI only: it is the backend this has
// been measured on, and scale_qsv and scale_cuda would each need the same
// verification before being trusted with playback.
func (h *HWContext) scalesOnDevice() bool {
	return h != nil && h.Kind == HWVAAPI && !h.noDeviceScale && filterExists("scale_vaapi")
}

// filterExists reports whether this build carries the named filter.
func filterExists(name string) bool {
	n := ffmpeg.ToCStr(name)
	defer n.Free()
	return ffmpeg.AVFilterGetByName(n) != nil
}
