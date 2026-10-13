package media

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"

	"github.com/garybowers/cineva/server/internal/logging"
	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

// subtitleLog reports subtitle burn-in during a transcode.
var subtitleLog = logging.For("subtitles")

// HLSJob transcodes a file into HLS fMP4 segments. Audio is always
// transcoded to AAC (browsers can't decode the ac3/eac3 common in media
// files). Video is copied untouched by default; when VideoHeight > 0 it is
// re-encoded to h264 at that height with VideoBitrateKbps (the Plex-style
// quality levels). Restart-at-offset seeking runs a new job with
// StartSegment > 0.
type HLSJob struct {
	InputPath    string
	OutDir       string
	SegmentSecs  int
	StartSegment int

	VideoHeight      int // 0 = copy the video stream
	VideoBitrateKbps int
	// FilterSpecOverride replaces the filter chain videoFilterSpec would
	// build. For measuring one chain against another; empty in normal use.
	FilterSpecOverride string

	// HW, when set, decodes and encodes on the GPU where the device can.
	// Frames that have to visit system memory (subtitles, the CPU tone
	// map, a software decode) are uploaded into a device frame pool for
	// the vendor encoder; otherwise they never leave the device (see
	// videoGraph). Any failure setting that up falls back to software
	// rather than failing the playback.
	HW *HWContext

	// HDR is "pq", "hlg" or "" (from Info.HDRFormat). When set and the video
	// is being re-encoded, the filter graph tone maps to SDR BT.709 —
	// without it, PQ-coded pixels are written into an 8-bit SDR h264 stream
	// and the picture comes out washed out, dark and flat. Ignored when the
	// video is copied, which preserves the HDR stream as it is.
	HDR string

	// BurnSubtitle is the stream index of an image-based subtitle track to
	// paint into the picture, or 0 for none. PGS and VobSub are bitmaps
	// with no text to hand a player, so burning them in is the only way to
	// show them — and only possible when the video is being re-encoded, as
	// nothing can be drawn on a copied stream.
	BurnSubtitle int

	// AudioTrack is the stream index to play. -1 (or an index that is not
	// an audio stream) falls back to whichever stream ffmpeg rates best,
	// which is what a file with a single track always gets.
	AudioTrack int

	// OnAccel, when set, is called once with the path actually built —
	// the device name, or "none" when it fell back to software.
	OnAccel func(name string)

	// OnInput, when set, is called each time a read from the source returns:
	// the session's stall watchdog counts these as progress (see
	// Session.checkStall). Called once per packet, so it must be cheap.
	OnInput func()

	// Throttle, when set, is called with the current output position; it may
	// block to keep the transcoder from racing arbitrarily far ahead of the
	// playhead (and filling the disk with segments nobody may ever watch).
	Throttle func(ctx context.Context, posSecs int64) error
}

const (
	aacSampleRate = 48000
	aacBitrate    = 192_000

	// x264 speed/quality preset for live transcoding. See the note where it
	// is applied: anything slower cannot keep ahead of the playhead on a
	// 4K source.
	h264Preset = "veryfast"

	// MKTAG('h','v','c','1') — the fMP4 sample entry browsers accept.
	tagHVC1 = 0x31636876
)

// setPrivOpt sets a codec-private option (x264's preset/tune live there, not
// on the AVCodecContext itself). A codec that does not know the option is not
// an error worth failing a playback over, so it is logged and ignored.
func setPrivOpt(cc *ffmpeg.AVCodecContext, key, value string) {
	priv := cc.PrivData()
	if priv == nil {
		return
	}
	name, val := ffmpeg.ToCStr(key), ffmpeg.ToCStr(value)
	defer name.Free()
	defer val.Free()
	if _, err := ffmpeg.AVOptSet(priv, name, val, 0); err != nil {
		logger.Debug("encoder option ignored", "option", key, "value", value, "err", err)
	}
}

func RunHLS(ctx context.Context, job HLSJob) (retErr error) {
	onInput := job.OnInput
	if onInput == nil {
		onInput = func() {}
	}
	in, err := av.Open(job.InputPath)
	if err != nil {
		return fmt.Errorf("hls open: %w", err)
	}
	defer in.Close()
	onInput()

	vStream, err := in.BestStream(ffmpeg.AVMediaTypeVideo)
	if err != nil {
		return fmt.Errorf("hls: no video stream: %w", err)
	}
	aStream, err := in.BestStream(ffmpeg.AVMediaTypeAudio)
	if err != nil {
		return fmt.Errorf("hls: no audio stream: %w", err)
	}
	// A specific track was asked for (another language, a commentary): use
	// it in place of the best stream, as long as it really is audio.
	if job.AudioTrack >= 0 {
		streams := in.Streams()
		for i := uint(0); i < in.NbStreams(); i++ {
			st := streams.Get(uintptr(i))
			if st.Index() != job.AudioTrack {
				continue
			}
			if ffmpeg.AVCodecGetType(st.Codecpar().CodecId()) == ffmpeg.AVMediaTypeAudio {
				aStream = st
			}
			break
		}
	}

	// Seek before wiring the pipeline so decoders start clean at the offset.
	if job.StartSegment > 0 {
		ts := int64(job.StartSegment*job.SegmentSecs) * int64(ffmpeg.AVTimeBase)
		if _, err := ffmpeg.AVSeekFrame(in.Raw(), -1, ts, ffmpeg.AVSeekFlagBackward); err != nil {
			return fmt.Errorf("hls seek: %w", err)
		}
	}

	adec, err := newThreadedDecoder(aStream)
	if err != nil {
		return fmt.Errorf("hls audio decoder: %w", err)
	}
	defer adec.Close()

	aenc, err := av.NewEncoderByID(ffmpeg.AVCodecIdAac, func(cc *ffmpeg.AVCodecContext) {
		cc.SetSampleFmt(ffmpeg.AVSampleFmtFltp)
		cc.SetSampleRate(aacSampleRate)
		// cc.ChLayout() is C-owned and allocated; a Go-declared zero
		// AVChannelLayout wraps a nil pointer and segfaults.
		ffmpeg.AVChannelLayoutDefault(cc.ChLayout(), 2)
		cc.SetBitRate(aacBitrate)
		cc.SetTimeBase(ffmpeg.AVMakeQ(1, aacSampleRate))
		cc.SetFlags(cc.Flags() | ffmpeg.AVCodecFlagGlobalHeader)
	})
	if err != nil {
		return fmt.Errorf("hls aac encoder: %w", err)
	}
	defer aenc.Close()

	// asetnsamples chunks frames to the AAC frame size; format/rate/layout
	// conversion is auto-inserted by the graph. No aresample=async here:
	// sessions start mid-file after seeks/resume, and async compensation
	// pads the whole start offset with silence (the no-audio-on-resume bug).
	filter, err := av.NewAudioFilterGraphFromContext(adec.Raw(),
		audioFilterSpec(aacSampleRate, 1024),
		ffmpeg.AVSampleFmtFltp, aacSampleRate, aenc.Raw().ChLayout())
	if err != nil {
		return fmt.Errorf("hls audio filter: %w", err)
	}
	defer filter.Close()

	out, err := av.CreateOutput(filepath.Join(job.OutDir, "live.m3u8"))
	if err != nil {
		return fmt.Errorf("hls output: %w", err)
	}
	// Closing flushes whatever output the muxer opened itself; a failure
	// there leaves the stream incomplete.
	defer func() {
		if err := out.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("hls close: %w", err)
		}
	}()

	// Stream 0: video — copied untouched, or decoded/scaled/re-encoded when a
	// quality level was requested.
	// One frame's worth of output timebase units, used to fill in packet
	// durations the encoder leaves at zero. Set when the encoder is built.
	var videoFrameDur int64
	var vdec *threadedDecoder
	var venc *av.Encoder
	var vOut *ffmpeg.AVStream
	// Device frame pool, non-nil only when encoding on the GPU. It must
	// outlive the encoder that draws from it.
	var hwFrames *ffmpeg.AVBufferRef
	// Non-nil when image subtitles are being painted into the picture.
	var burner *subBurner
	// Set during setup; read by the packet loop, which is outside that
	// block. Non-nil whenever the video is re-encoded: it takes decoded
	// frames, from the CPU or the device, to what the encoder consumes
	// (see videoGraph).
	var vgraph *videoGraph
	defer func() {
		if vgraph != nil {
			vgraph.Close()
		}
	}()
	var srcW, srcH, outW, outH int
	defer func() {
		if hwFrames != nil {
			ffmpeg.AVBufferUnref(&hwFrames)
		}
	}()

	if job.VideoHeight > 0 {
		// Decoding on the device is the larger saving: with encode on the
		// GPU and decode still on the CPU, a 4K HEVC source spends most of
		// a machine's cores on decode alone. newDecoder falls back to
		// software by itself if the device cannot take the stream.
		vdec, err = newDecoder(vStream, job.HW)
		if err != nil {
			return fmt.Errorf("hls video decoder: %w", err)
		}
		defer vdec.Close()

		srcW, srcH = vStream.Codecpar().Width(), vStream.Codecpar().Height()
		outH = job.VideoHeight
		if outH > srcH {
			outH = srcH // never upscale
		}
		outW = srcW * outH / srcH / 2 * 2 // keep aspect, force even

		// The device frame pool the hardware encoder draws from. Whether it,
		// and then the encoder, can be built decides the format the filter
		// graph has to produce, so the graph is built after both.
		if job.HW != nil {
			hwFrames, err = job.HW.NewFrames(outW, outH)
			if err != nil {
				logger.Warn("hardware frames unusable, encoding on the CPU", "kind", job.HW.Kind, "err", err)
				hwFrames = nil
			}
		}
		// Decoding on the device emits device surfaces, which a software
		// filter cannot take. Unless they can stay there all the way to a
		// hardware encoder, they are brought back to system memory —
		// scaled on the device first where possible, at full size
		// otherwise — so HDR tone mapping works on a hardware decode
		// exactly as it does on a software one: the chain just runs after
		// the download rather than instead of it.
		//
		// The graph cannot be built here: libavcodec allocates the
		// decoder's frame pool lazily, during the first get_format call,
		// so hw_frames_ctx is still nil until a frame has been decoded.
		// videoGraph builds it on the first frame instead.
		//
		// Whether the device also encodes does not matter here. The
		// download graph ends in the encoder's own format
		// (encoderPixFmtName), so a decode-only device feeds libx264 the
		// same way a full one feeds its upload — and decode is the larger
		// half of the CPU cost, so it is worth having on its own.
		onGPU := vdec.HardwarePixFmt() != ffmpeg.AVPixFmtNone

		// Image subtitles are painted onto the scaled frame on its way to
		// the encoder. A failure to open the decoder loses the subtitles,
		// not the playback.
		if job.BurnSubtitle > 0 {
			if st := streamByIndex(in, job.BurnSubtitle); st != nil {
				if b, err := newSubBurner(st, srcW, srcH, outW, outH); err == nil {
					burner = b
					defer burner.Close()
					subtitleLog.Debug("burning in subtitle", "stream", job.BurnSubtitle)
				} else {
					subtitleLog.Warn("subtitle burn-in unavailable", "stream", job.BurnSubtitle, "err", err)
				}
			}
		}

		// Keyframe every segment so the HLS muxer can cut clean boundaries.
		//
		// The GOP is a whole number of frames, and it must be the same whole
		// number the playlist's EXTINF is derived from — see
		// realSegmentSecs. Computed from the exact rational frame rate for
		// that reason: rounding the rate first and multiplying second gives
		// a different answer for anything that is not a broadcast rate, and
		// the two would then disagree about how long a segment is.
		frNum, frDen := 25, 1
		if fr := vStream.AvgFrameRate(); fr != nil && fr.Den() > 0 && fr.Num() > 0 {
			frNum, frDen = fr.Num(), fr.Den()
		}
		gop := gopFrames(job.SegmentSecs, frNum, frDen)
		// Shared encoder settings; the hardware path differs only in the
		// pixel format it consumes and the frame pool it draws from.
		configure := func(cc *ffmpeg.AVCodecContext) {
			cc.SetWidth(outW)
			cc.SetHeight(outH)
			cc.SetTimeBase(vStream.TimeBase())
			cc.SetBitRate(int64(job.VideoBitrateKbps) * 1000)
			cc.SetGopSize(gop)
			cc.SetFlags(cc.Flags() | ffmpeg.AVCodecFlagGlobalHeader)
		}
		if hwFrames != nil {
			name := ffmpeg.ToCStr(job.HW.Encoder)
			codec := ffmpeg.AVCodecFindEncoderByName(name)
			name.Free()
			if codec == nil {
				err = fmt.Errorf("encoder %s not in this build", job.HW.Encoder)
			} else {
				venc, err = av.NewEncoder(codec, func(cc *ffmpeg.AVCodecContext) {
					configure(cc)
					cc.SetPixFmt(job.HW.PixFmt)
					// The frame rate, not just the time base. Without it
					// the encoder's rate control takes 1/time_base as the
					// frame rate — 1000 fps for a Matroska source — and
					// spreads the bitrate across that many frames a
					// second: h264_vaapi wrote 0.32 Mbit/s against a
					// 6 Mbit/s target, and from a source with a 1/16000
					// time base it never returned from its first frame.
					// libx264 is left as it was: it rates by timestamps,
					// and giving it the frame rate moved its bitrate
					// (5.2 -> 6.5 Mbit/s on an SDR episode), which is a
					// change of its own to weigh separately.
					cc.SetFramerate(ffmpeg.AVMakeQ(frNum, frDen))
					// A new reference, not ours: avcodec_free_context unrefs
					// hw_frames_ctx when the encoder closes, so handing it our
					// own reference and then unreffing it ourselves is a double
					// free (SIGABRT in av_buffer_unref).
					cc.SetHwFramesCtx(ffmpeg.AVBufferRef_(hwFrames))
					// B-frames are left to the driver: support varies by vendor
					// and generation, and forcing them is a common way to make
					// a hardware encoder refuse to open.
					cc.SetMaxBFrames(0)
				})
			}
			if err != nil {
				// The device opened but will not encode this — the build
				// lacks the encoder, or the chip has no block for it. Encode
				// on the CPU rather than fail the playback; the filter
				// graphs are built after this, so they follow.
				logger.Warn("hardware encoder unusable, encoding on the CPU", "encoder", job.HW.Encoder, "err", err)
				ffmpeg.AVBufferUnref(&hwFrames)
				hwFrames = nil
			} else {
				logger.Debug("encoding on hardware", "device", job.HW.Name(), "encoder", job.HW.Encoder, "width", outW, "height", outH)
				if job.OnAccel != nil {
					job.OnAccel(job.HW.Name())
				}
			}
		}
		if hwFrames == nil {
			venc, err = av.NewEncoderByID(ffmpeg.AVCodecIdH264, func(cc *ffmpeg.AVCodecContext) {
				configure(cc)
				cc.SetPixFmt(ffmpeg.AVPixFmtYuv420P)
				cc.SetMaxBFrames(2)
				// libx264 defaults to the "medium" preset, which is tuned
				// for file encoding, not for staying ahead of a playhead:
				// decoding 4K HEVC and re-encoding at medium runs near 1x
				// realtime and stalls. "veryfast" is the live choice.
				// Encode threads matter far less than decode threads, but
				// they are free, and this leaves a core for the HTTP server.
				cc.SetThreadCount(decodeThreads())
				setPrivOpt(cc, "preset", h264Preset)
				setPrivOpt(cc, "tune", "fastdecode")
			})
			if err != nil {
				return fmt.Errorf("hls h264 encoder: %w", err)
			}
			if job.OnAccel != nil {
				job.OnAccel(string(HWNone))
			}
		}
		defer venc.Close()

		// Built once the encoder is settled, because the encoder decides
		// what the graph must produce: NV12 for a GPU upload, YUV420P for
		// libx264.
		//
		// The software decode goes through the same graph as the hardware
		// one, built from the first frame. It used to be built from the
		// decoder context by the vendored helper, whose buffer source
		// declares no colour space or range; swscale then read every
		// source as something it was not, and the picture came out 1.7
		// (8-bit SDR) to 2.5 (10-bit) levels darker than reference
		// ffmpeg, while converting colours it had no need to touch.
		// Declared, it is bit-exact with reference ffmpeg
		// (TestCPUGraphMatchesReference), and the filter stage is
		// cheaper: 27% less CPU scaling 4K 10-bit to 1080p, 20% with the
		// tone map, 58% scaling 8-bit 1080p to 720p.
		//
		// Frames stay on the device all the way to the encoder only when
		// nothing in between needs them in system memory: a hardware
		// encoder, no subtitle to paint, no tone map, the standard chain.
		vgraph = &videoGraph{
			spec: job.filterSpec(outW, outH) + ",format=" + encoderPixFmtName(hwFrames != nil),
			outW: outW, outH: outH, timeBase: vStream.TimeBase(),
			scale: onGPU && job.FilterSpecOverride == "" && job.HW.scalesOnDevice(),
			encodeOnDevice: onGPU && hwFrames != nil && burner == nil && job.HDR == "" &&
				job.FilterSpecOverride == "" && job.HW.encodesFromDevice(),
		}
		if job.HW != nil {
			vgraph.kind = job.HW.Kind
		}

		vOut, err = out.AddStream(venc)
		if err != nil {
			return fmt.Errorf("hls add video stream: %w", err)
		}
		// One frame = 1/frame_rate seconds, expressed in the output
		// stream's timebase.
		videoFrameDur = ffmpeg.AVRescaleQ(1,
			ffmpeg.AVMakeQ(frDen, frNum), vOut.TimeBase())
	} else {
		vOut = ffmpeg.AVFormatNewStream(out.Raw(), nil)
		if vOut == nil {
			return errors.New("hls: cannot add video stream")
		}
		if _, err := ffmpeg.AVCodecParametersCopy(vOut.Codecpar(), vStream.Codecpar()); err != nil {
			return fmt.Errorf("hls copy video params: %w", err)
		}
		// fMP4 wants the hvc1 sample entry for HEVC; hev1 is what the muxer
		// writes by default and what browsers are least happy with. Setting
		// the tag here is necessary but not sufficient: it survives as far
		// as avformat_write_header (verified) and the HLS muxer still emits
		// hev1 into init.mp4, so forceHVC1 corrects the written bytes. Both
		// are kept — this one states the intent, that one makes it true.
		if vStream.Codecpar().CodecId() == ffmpeg.AVCodecIdHevc {
			vOut.Codecpar().SetCodecTag(tagHVC1)
		}
		vOut.SetTimeBase(vStream.TimeBase())
	}

	// Stream 1: encoded AAC audio.
	aOut, err := out.AddStream(aenc)
	if err != nil {
		return fmt.Errorf("hls add audio stream: %w", err)
	}

	// HLS muxer options. temp_file guarantees a segment file is only visible
	// once complete, so the HTTP handler can serve on file existence.
	muxOpts := map[string]string{
		"hls_time":               strconv.Itoa(job.SegmentSecs),
		"hls_segment_type":       "fmp4",
		"hls_segment_filename":   filepath.Join(job.OutDir, "seg%05d.m4s"),
		"hls_fmp4_init_filename": "init.mp4",
		"start_number":           strconv.Itoa(job.StartSegment),
		"hls_list_size":          "0",
		"hls_flags":              "temp_file",
	}
	for k, v := range muxOpts {
		name, val := ffmpeg.ToCStr(k), ffmpeg.ToCStr(v)
		// RawPtr(), not the wrapper struct — AVOptSet wants the C pointer.
		_, err := ffmpeg.AVOptSet(out.Raw().RawPtr(), name, val, ffmpeg.AVOptSearchChildren)
		name.Free()
		val.Free()
		if err != nil {
			return fmt.Errorf("hls option %s: %w", k, err)
		}
	}

	if err := out.WriteHeader(); err != nil {
		return fmt.Errorf("hls header: %w", err)
	}

	writeAudioPacket := func(pkt *ffmpeg.AVPacket) error {
		ffmpeg.AVPacketRescaleTs(pkt, aenc.Raw().TimeBase(), aOut.TimeBase())
		pkt.SetStreamIndex(aOut.Index())
		return out.WritePacket(pkt)
	}
	encodeFrame := func(frame *ffmpeg.AVFrame) error {
		return aenc.Encode(frame, writeAudioPacket)
	}
	writeVideoPacket := func(pkt *ffmpeg.AVPacket) error {
		ffmpeg.AVPacketRescaleTs(pkt, venc.Raw().TimeBase(), vOut.TimeBase())
		// Hardware encoders (h264_vaapi and friends) emit packets with no
		// duration, which leaves the HLS muxer guessing where a segment
		// ends: "packet with pts N has duration 0. The segment duration may
		// not be precise." Supply one frame's worth so segment boundaries
		// and the playlist's EXTINF agree.
		if pkt.Duration() == 0 && videoFrameDur > 0 {
			pkt.SetDuration(videoFrameDur)
		}
		pkt.SetStreamIndex(vOut.Index())
		return out.WritePacket(pkt)
	}
	// Reused for every upload: allocated once, unreffed after each encode so
	// the surface returns to the pool.
	hwFrame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&hwFrame)

	encodeVideoFrame := func(frame *ffmpeg.AVFrame) error {
		frame.SetPictType(ffmpeg.AVPictureTypeNone) // let the encoder decide
		// Painted after scaling and before the GPU upload: the frame is
		// still in system memory here, and at output resolution, which is
		// what the burner maps its coordinates onto.
		if burner != nil {
			burner.Paint(frame, ffmpeg.AVRescaleQ(frame.Pts(),
				vStream.TimeBase(), ffmpeg.AVMakeQ(1, ffmpeg.AVTimeBase)))
		}
		// Already a device surface — scaled on the device and left there
		// (videoGraph.deviceOut) — so the encoder takes it as it is.
		if hwFrames == nil || frame.HwFramesCtx() != nil {
			return venc.Encode(frame, writeVideoPacket)
		}
		// The scaled NV12 frame is in system memory and has to be copied
		// onto a device surface before the encoder will take it.
		if _, err := ffmpeg.AVHWFrameGetBuffer(hwFrames, hwFrame, 0); err != nil {
			return fmt.Errorf("hw frame from pool: %w", err)
		}
		if _, err := ffmpeg.AVHWFrameTransferData(hwFrame, frame, 0); err != nil {
			ffmpeg.AVFrameUnref(hwFrame)
			return fmt.Errorf("hw upload: %w", err)
		}
		hwFrame.SetPts(frame.Pts()) // the surface carries no timestamp of its own
		err := venc.Encode(hwFrame, writeVideoPacket)
		ffmpeg.AVFrameUnref(hwFrame)
		return err
	}

	pkt := ffmpeg.AVPacketAlloc()
	defer ffmpeg.AVPacketFree(&pkt)

	oneSecond := ffmpeg.AVMakeQ(1, 1)

	burnWarned := false
	for {
		if err := ctx.Err(); err != nil {
			return err // cancelled: session stopped or seeking elsewhere
		}
		err := in.ReadPacket(pkt)
		onInput()
		if errors.Is(err, ffmpeg.AVErrorEOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("hls read: %w", err)
		}

		if burner != nil && pkt.StreamIndex() == job.BurnSubtitle {
			// Once per session at warn: a broken subtitle stream fails on
			// every packet, and one line says as much as a thousand.
			if err := burner.Feed(pkt); err != nil {
				level := slog.LevelDebug
				if !burnWarned {
					level, burnWarned = slog.LevelWarn, true
				}
				subtitleLog.Log(ctx, level, "subtitle packet could not be burned in", "stream", job.BurnSubtitle, "err", err)
			}
			continue
		}
		switch pkt.StreamIndex() {
		case vStream.Index():
			if job.Throttle != nil && pkt.Pts() != ffmpeg.AVNoptsValue {
				pos := ffmpeg.AVRescaleQ(pkt.Pts(), vStream.TimeBase(), oneSecond)
				if err := job.Throttle(ctx, pos); err != nil {
					return err
				}
			}
			if venc != nil {
				err = vdec.Decode(pkt, func(frame *ffmpeg.AVFrame) error {
					frame.SetPts(frame.BestEffortTimestamp())
					return vgraph.Push(frame, encodeVideoFrame)
				})
				if err != nil {
					return fmt.Errorf("hls video: %w", err)
				}
			} else {
				ffmpeg.AVPacketRescaleTs(pkt, vStream.TimeBase(), vOut.TimeBase())
				pkt.SetStreamIndex(vOut.Index())
				pkt.SetPos(-1)
				if err := out.WritePacket(pkt); err != nil {
					return fmt.Errorf("hls write video: %w", err)
				}
			}
		case aStream.Index():
			err := adec.Decode(pkt, func(frame *ffmpeg.AVFrame) error {
				frame.SetPts(frame.BestEffortTimestamp())
				if err := filter.Push(frame); err != nil {
					return err
				}
				return filter.Pull(encodeFrame)
			})
			if err != nil {
				return fmt.Errorf("hls audio: %w", err)
			}
		}
		ffmpeg.AVPacketUnref(pkt)
	}

	// Drain: decoders -> filters -> encoders -> muxer.
	if venc != nil {
		if err := vdec.Flush(func(frame *ffmpeg.AVFrame) error {
			frame.SetPts(frame.BestEffortTimestamp())
			return vgraph.Push(frame, encodeVideoFrame)
		}); err != nil {
			return fmt.Errorf("hls flush video decoder: %w", err)
		}
		if err := vgraph.Flush(encodeVideoFrame); err != nil {
			return fmt.Errorf("hls flush video filter: %w", err)
		}
		if err := venc.Flush(writeVideoPacket); err != nil {
			return fmt.Errorf("hls flush video encoder: %w", err)
		}
	}
	if err := adec.Flush(func(frame *ffmpeg.AVFrame) error {
		frame.SetPts(frame.BestEffortTimestamp())
		if err := filter.Push(frame); err != nil {
			return err
		}
		return filter.Pull(encodeFrame)
	}); err != nil {
		return fmt.Errorf("hls flush decoder: %w", err)
	}
	if err := filter.Push(nil); err == nil {
		_ = filter.Pull(encodeFrame)
	}
	if err := aenc.Flush(writeAudioPacket); err != nil {
		return fmt.Errorf("hls flush encoder: %w", err)
	}
	if err := out.WriteTrailer(); err != nil {
		return fmt.Errorf("hls trailer: %w", err)
	}
	return nil
}

// videoFilterSpec builds the filter chain between the decoder and the
// encoder: a plain scale for SDR, and a tone-mapping chain for HDR.
//
// Re-encoding HDR as if it were SDR is what makes a transcoded 4K HDR film
// look washed out, grey and flat: the PQ transfer curve allocates its code
// values for a 1000+ nit display, so shown on an SDR one the midtones sit
// far too dark and the highlights clip to nothing. The fix is to linearise,
// map the HDR range into SDR with a tone curve, then re-encode in BT.709.
//
// The chain is the conventional zscale/tonemap one:
//
//	zscale=tin=<src>:t=linear  -> linear light, scaled to a 100-nit target
//	format=gbrpf32le           -> tonemap needs float RGB
//	zscale=p=bt709             -> BT.2020 primaries to BT.709
//	tonemap=hable              -> the curve; hable keeps highlight detail
//	                              where "clip" blows it out
//	zscale=t=bt709:m=bt709:r=tv -> back to a BT.709 video signal
//
// desat=0 keeps saturation rather than letting ffmpeg's default desaturate
// bright areas towards white, which is the usual complaint about the
// default tonemap settings. The caller's output pixel format (yuv420p for
// libx264, nv12 for a GPU upload) is applied by the graph itself, so this
// chain deliberately does not end in a format filter.
//
// npl=100 is the target display peak in nits; it is the SDR reference white
// that the HDR range is compressed into, not a property of the source.
// filterSpec is the chain this job will use, honouring an override.
func (j HLSJob) filterSpec(outW, outH int) string {
	if j.FilterSpecOverride != "" {
		return j.FilterSpecOverride
	}
	return videoFilterSpec(j.HDR, outW, outH)
}

func videoFilterSpec(hdr string, outW, outH int) string {
	scale := fmt.Sprintf("scale=%d:%d", outW, outH)
	tin := ""
	switch hdr {
	case "pq":
		tin = "smpte2084"
	case "hlg":
		tin = "arib-std-b67"
	default:
		return scale // SDR: nothing to map
	}
	// Scaling first. Tone mapping runs on every pixel through a float
	// conversion, so doing it at 4K costs four times what it costs at 1080p
	// — measured at 0.76x realtime against 1.61x, which is the difference
	// between a stream that stalls and one with headroom. The picture is
	// the same to 0.28/255 mean luma difference, against roughly 5/255 for
	// the tone map itself, so the earlier worry about downscaling
	// already-mapped values costs nothing anyone can see.
	//
	// The input colour properties are stated outright because swscale does
	// not carry HDR metadata through: without them zimg is handed a frame
	// it cannot classify and refuses it with "no path between colorspaces",
	// which is a stall rather than a wrong picture, but a stall either way.
	return scale + "," +
		"zscale=tin=" + tin + ":min=bt2020nc:pin=bt2020:rin=tv:t=linear:npl=100," +
		"format=gbrpf32le," +
		"zscale=p=bt709," +
		"tonemap=tonemap=hable:desat=0," +
		"zscale=t=bt709:m=bt709:r=tv"
}

// streamByIndex finds a stream by its index in the container.
func streamByIndex(in *av.Input, index int) *ffmpeg.AVStream {
	streams := in.Streams()
	for i := uint(0); i < in.NbStreams(); i++ {
		st := streams.Get(uintptr(i))
		if st != nil && st.Index() == index {
			return st
		}
	}
	return nil
}

// downloadFrame copies a decoded device surface into system memory, so the
// software filter graph can scale it.
//
// The fallback for when the device cannot scale (see videoGraph). The
// destination frame is reused across calls — allocating one per frame would
// churn a few megabytes at 24 frames a second.
func downloadFrame(dst **ffmpeg.AVFrame, src *ffmpeg.AVFrame) (*ffmpeg.AVFrame, error) {
	if *dst == nil {
		*dst = ffmpeg.AVFrameAlloc()
		if *dst == nil {
			return nil, fmt.Errorf("allocate download frame")
		}
	}
	if src.HwFramesCtx() == nil {
		// Already in system memory: the decoder could not take this
		// stream on the device after all and fell back to software.
		return src, nil
	}
	out := *dst
	ffmpeg.AVFrameUnref(out)
	if _, err := ffmpeg.AVHWFrameTransferData(out, src, 0); err != nil {
		return nil, fmt.Errorf("download frame from device: %w", err)
	}
	// The transfer copies pixels and nothing else. Timestamps matter for
	// muxing; the colour properties matter because the tone-mapping chain
	// reads the transfer function and primaries off the frame, and zimg
	// fails outright on a frame that claims none.
	out.SetPts(src.Pts())
	out.SetBestEffortTimestamp(src.BestEffortTimestamp())
	out.SetColorRange(src.ColorRange())
	out.SetColorPrimaries(src.ColorPrimaries())
	out.SetColorTrc(src.ColorTrc())
	out.SetColorspace(src.Colorspace())
	out.SetChromaLocation(src.ChromaLocation())
	return out, nil
}

// encoderPixFmtName is the pixel format the video encoder is fed. A
// hardware encoder takes NV12 surfaces; libx264 takes YUV420P. The
// download path pins this explicitly because its filter chain ends in
// whatever the last filter negotiates — for HDR that is a zscale output,
// not necessarily anything an encoder accepts.
func encoderPixFmtName(hardware bool) string {
	if hardware {
		return "nv12"
	}
	return "yuv420p"
}
