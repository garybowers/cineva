package media

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

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

	// Throttle, when set, is called with the current output position; it may
	// block to keep the transcoder from racing arbitrarily far ahead of the
	// playhead (and filling the disk with segments nobody may ever watch).
	Throttle func(ctx context.Context, posSecs int64) error
}

const (
	aacSampleRate = 48000
	aacBitrate    = 192_000
)

func RunHLS(ctx context.Context, job HLSJob) error {
	in, err := av.Open(job.InputPath)
	if err != nil {
		return fmt.Errorf("hls open: %w", err)
	}
	defer in.Close()

	vStream, err := in.BestStream(ffmpeg.AVMediaTypeVideo)
	if err != nil {
		return fmt.Errorf("hls: no video stream: %w", err)
	}
	aStream, err := in.BestStream(ffmpeg.AVMediaTypeAudio)
	if err != nil {
		return fmt.Errorf("hls: no audio stream: %w", err)
	}

	// Seek before wiring the pipeline so decoders start clean at the offset.
	if job.StartSegment > 0 {
		ts := int64(job.StartSegment*job.SegmentSecs) * int64(ffmpeg.AVTimeBase)
		if _, err := ffmpeg.AVSeekFrame(in.Raw(), -1, ts, ffmpeg.AVSeekFlagBackward); err != nil {
			return fmt.Errorf("hls seek: %w", err)
		}
	}

	adec, err := av.NewDecoder(aStream)
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
		"asetnsamples=n=1024:p=0",
		ffmpeg.AVSampleFmtFltp, aacSampleRate, aenc.Raw().ChLayout())
	if err != nil {
		return fmt.Errorf("hls audio filter: %w", err)
	}
	defer filter.Close()

	out, err := av.CreateOutput(filepath.Join(job.OutDir, "live.m3u8"))
	if err != nil {
		return fmt.Errorf("hls output: %w", err)
	}
	defer out.Close()

	// Stream 0: video — copied untouched, or decoded/scaled/re-encoded when a
	// quality level was requested.
	var vdec *av.Decoder
	var vfilter *av.FilterGraph
	var venc *av.Encoder
	var vOut *ffmpeg.AVStream

	if job.VideoHeight > 0 {
		vdec, err = av.NewDecoder(vStream)
		if err != nil {
			return fmt.Errorf("hls video decoder: %w", err)
		}
		defer vdec.Close()

		srcW, srcH := vStream.Codecpar().Width(), vStream.Codecpar().Height()
		outH := job.VideoHeight
		if outH > srcH {
			outH = srcH // never upscale
		}
		outW := srcW * outH / srcH / 2 * 2 // keep aspect, force even

		vfilter, err = av.NewVideoFilterGraphFromContext(vdec.Raw(),
			fmt.Sprintf("scale=%d:%d", outW, outH), ffmpeg.AVPixFmtYuv420P)
		if err != nil {
			return fmt.Errorf("hls video filter: %w", err)
		}
		defer vfilter.Close()

		// Keyframe every segment so the HLS muxer can cut clean boundaries.
		fps := 25
		if fr := vStream.AvgFrameRate(); fr != nil && fr.Den() > 0 && fr.Num() > 0 {
			fps = (fr.Num() + fr.Den() - 1) / fr.Den()
		}
		venc, err = av.NewEncoderByID(ffmpeg.AVCodecIdH264, func(cc *ffmpeg.AVCodecContext) {
			cc.SetWidth(outW)
			cc.SetHeight(outH)
			cc.SetPixFmt(ffmpeg.AVPixFmtYuv420P)
			cc.SetTimeBase(vStream.TimeBase())
			cc.SetBitRate(int64(job.VideoBitrateKbps) * 1000)
			cc.SetGopSize(fps * job.SegmentSecs)
			cc.SetMaxBFrames(2)
			cc.SetFlags(cc.Flags() | ffmpeg.AVCodecFlagGlobalHeader)
		})
		if err != nil {
			return fmt.Errorf("hls h264 encoder: %w", err)
		}
		defer venc.Close()

		vOut, err = out.AddStream(venc)
		if err != nil {
			return fmt.Errorf("hls add video stream: %w", err)
		}
	} else {
		vOut = ffmpeg.AVFormatNewStream(out.Raw(), nil)
		if vOut == nil {
			return errors.New("hls: cannot add video stream")
		}
		if _, err := ffmpeg.AVCodecParametersCopy(vOut.Codecpar(), vStream.Codecpar()); err != nil {
			return fmt.Errorf("hls copy video params: %w", err)
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
		pkt.SetStreamIndex(vOut.Index())
		return out.WritePacket(pkt)
	}
	encodeVideoFrame := func(frame *ffmpeg.AVFrame) error {
		frame.SetPictType(ffmpeg.AVPictureTypeNone) // let the encoder decide
		return venc.Encode(frame, writeVideoPacket)
	}

	pkt := ffmpeg.AVPacketAlloc()
	defer ffmpeg.AVPacketFree(&pkt)

	oneSecond := ffmpeg.AVMakeQ(1, 1)

	for {
		if err := ctx.Err(); err != nil {
			return err // cancelled: session stopped or seeking elsewhere
		}
		err := in.ReadPacket(pkt)
		if errors.Is(err, ffmpeg.AVErrorEOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("hls read: %w", err)
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
					if err := vfilter.Push(frame); err != nil {
						return err
					}
					return vfilter.Pull(encodeVideoFrame)
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
			if err := vfilter.Push(frame); err != nil {
				return err
			}
			return vfilter.Pull(encodeVideoFrame)
		}); err != nil {
			return fmt.Errorf("hls flush video decoder: %w", err)
		}
		if err := vfilter.Push(nil); err == nil {
			_ = vfilter.Pull(encodeVideoFrame)
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
