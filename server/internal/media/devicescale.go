package media

import (
	"fmt"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// gpuFrames turns frames decoded on the device into frames the encoder
// takes, scaling on the device first where that is possible.
//
// Every decoded surface has to come back to system memory: subtitles are
// painted there, HDR is tone mapped there, and libx264 can take nothing
// else. What matters is the size it comes back at. Downloading a 4K 10-bit
// surface is 24 MB a frame, and on an AMD APU the copy alone made a GPU
// decode into libx264 slower than decoding on the CPU (0.68x realtime
// against 1.05x, measured side by side). Scaling on the device first leaves
// a quarter of that to copy, and the measured result, 4K HEVC to 1080p:
//
//   - hardware encode: 3.25x -> 7.0x realtime, 20.6 s -> 4.5 s of CPU (SDR);
//     2.14x -> 2.87x with the HDR tone map, which stays on the CPU
//   - libx264: 4.17x realtime against 3.07x for an all-CPU transcode, with
//     62% less CPU (39% with the tone map, 2.11x against 1.67x)
//
// Device scaling is only attempted where it has been verified — VAAPI, with
// scale_vaapi in the build — and only for the standard chain. Anything else,
// or a device graph that will not build, downloads at full size and scales
// on the CPU exactly as before.
type gpuFrames struct {
	spec       string // the CPU chain, ending in the encoder's format
	outW, outH int
	timeBase   *ffmpeg.AVRational
	scale      bool // try scaling on the device
	kind       HWKind

	graph    *parsedGraph
	onDevice bool            // the graph takes device surfaces itself
	sw       *ffmpeg.AVFrame // download target when scaling on the CPU
}

// Push runs one decoded frame through to fn. The graph is built on the
// first frame, because only then is the decoder's frames context known.
func (p *gpuFrames) Push(frame *ffmpeg.AVFrame, fn func(*ffmpeg.AVFrame) error) error {
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
func (p *gpuFrames) Flush(fn func(*ffmpeg.AVFrame) error) error {
	if p.graph == nil {
		return nil // nothing was decoded, so nothing to drain
	}
	if err := p.graph.Push(nil); err != nil {
		return err
	}
	return p.graph.Pull(fn)
}

func (p *gpuFrames) Close() {
	if p.graph != nil {
		p.graph.Close()
		p.graph = nil
	}
	if p.sw != nil {
		ffmpeg.AVFrameFree(&p.sw)
	}
}

func (p *gpuFrames) build(frame *ffmpeg.AVFrame) error {
	if p.scale && frame.HwFramesCtx() != nil {
		swFormat := ffmpeg.ToAVHWFramesContext(frame.HwFramesCtx().Data()).SwFormat()
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

// deviceScaleSpec scales on a VAAPI device and brings the result back to
// system memory, in the surfaces' own format (swFormat: P010 for a 10-bit
// source, NV12 for 8-bit). Only the size changes on the device; the bit
// depth is left for the CPU chain to reduce, as it does on a software
// decode. Asking the device for NV12 from a 10-bit source lifted SDR luma
// by 1.6 levels across the picture — its conversion is not the one swscale
// does — where scaling alone matches the CPU chain to a fraction of a level.
func deviceScaleSpec(swFormat ffmpeg.AVPixelFormat, w, h int) (string, error) {
	f := ffmpeg.AVGetPixFmtName(swFormat).String()
	if f == "" {
		return "", fmt.Errorf("device surfaces have no software format (%d)", swFormat)
	}
	return fmt.Sprintf("scale_vaapi=w=%d:h=%d:format=%s,hwdownload,format=%s", w, h, f, f), nil
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
