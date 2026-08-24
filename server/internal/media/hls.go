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

// HLSJob remuxes a file into HLS fMP4 segments: the h264 video stream is
// copied untouched; audio is transcoded to AAC (the one thing browsers can't
// handle in typical media files is ac3/eac3 audio). Restart-at-offset seeking
// is done by running a new job with StartSegment > 0.
type HLSJob struct {
	InputPath    string
	OutDir       string
	SegmentSecs  int
	StartSegment int
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

	// asetnsamples chunks frames to the AAC frame size; aresample smooths
	// timestamp gaps after seeks. Output layout borrows the encoder's.
	filter, err := av.NewAudioFilterGraphFromContext(adec.Raw(),
		"aresample=async=1:first_pts=0,asetnsamples=n=1024:p=0",
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

	// Stream 0: copied video. Raw AVStream with codec parameters cloned.
	vOut := ffmpeg.AVFormatNewStream(out.Raw(), nil)
	if vOut == nil {
		return errors.New("hls: cannot add video stream")
	}
	if _, err := ffmpeg.AVCodecParametersCopy(vOut.Codecpar(), vStream.Codecpar()); err != nil {
		return fmt.Errorf("hls copy video params: %w", err)
	}
	vOut.SetTimeBase(vStream.TimeBase())

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

	pkt := ffmpeg.AVPacketAlloc()
	defer ffmpeg.AVPacketFree(&pkt)

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
			ffmpeg.AVPacketRescaleTs(pkt, vStream.TimeBase(), vOut.TimeBase())
			pkt.SetStreamIndex(vOut.Index())
			pkt.SetPos(-1)
			if err := out.WritePacket(pkt); err != nil {
				return fmt.Errorf("hls write video: %w", err)
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

	// Drain: decoder -> filter -> encoder -> muxer.
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
