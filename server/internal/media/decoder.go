package media

import (
	"errors"
	"fmt"
	"runtime"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
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
}

func newThreadedDecoder(stream *ffmpeg.AVStream) (*threadedDecoder, error) {
	if stream == nil {
		return nil, errors.New("nil stream")
	}
	codec := ffmpeg.AVCodecFindDecoder(stream.Codecpar().CodecId())
	if codec == nil {
		return nil, fmt.Errorf("find decoder (codec %v): %w",
			stream.Codecpar().CodecId(), ffmpeg.WrapErr(ffmpeg.AVErrorDecoderNotFoundConst))
	}
	ctx := ffmpeg.AVCodecAllocContext3(codec)
	if ctx == nil {
		return nil, errors.New("alloc decoder context failed")
	}
	d := &threadedDecoder{ctx: ctx}
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

	if _, err := ffmpeg.AVCodecOpen2(ctx, codec, nil); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("open decoder: %w", err)
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
