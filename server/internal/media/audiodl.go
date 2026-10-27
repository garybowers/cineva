package media

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

// DownloadFormat describes a transcoded audio download variant.
type DownloadFormat struct {
	Encoder string // ffmpeg encoder name
	Ext     string // file extension / muxer hint
}

var downloadFormats = map[string]DownloadFormat{
	"mp3":  {Encoder: "libmp3lame", Ext: "mp3"},
	"ogg":  {Encoder: "vorbis", Ext: "ogg"},
	"opus": {Encoder: "libopus", Ext: "opus"}, // streaming: best quality per bit
	"aac":  {Encoder: "aac", Ext: "m4a"},      // streaming: the universal fallback (Safari)
}

// AudioDownloadFormat resolves a user-facing format name ("mp3", "ogg").
func AudioDownloadFormat(name string) (DownloadFormat, bool) {
	f, ok := downloadFormats[name]
	return f, ok
}

// lame only accepts the standard MPEG sample rates.
var mp3Rates = map[int]bool{8000: true, 11025: true, 12000: true, 16000: true,
	22050: true, 24000: true, 32000: true, 44100: true, 48000: true}

// audioFilterSpec builds the conversion chain for feeding an encoder. The
// explicit aformat is essential: without it the graph converts rate and
// layout but can pass packed frames (e.g. FLAC's s16) straight through to a
// planar-configured encoder, which reads invalid channel pointers.
func audioFilterSpec(rate, frameSize int) string {
	return audioFilterSpecFmt("fltp", rate, frameSize)
}

// audioFilterSpecFmt is audioFilterSpec for encoders that want a different
// sample format (libopus takes packed flt, not planar).
func audioFilterSpecFmt(sampleFmt string, rate, frameSize int) string {
	spec := fmt.Sprintf("aformat=sample_fmts=%s:sample_rates=%d:channel_layouts=stereo", sampleFmt, rate)
	if frameSize > 0 {
		spec += fmt.Sprintf(",asetnsamples=n=%d:p=0", frameSize)
	}
	return spec
}

// TranscodeAudio converts a music file to dst (extension selects the muxer)
// using the named encoder at the given bitrate, downmixing to stereo.
//
// onRead, when set, is called each time a read from src returns, so a
// caller can give up on a source that has stopped answering (see
// library.WithinIdle).
func TranscodeAudio(ctx context.Context, src, dst, encoderName string, bitrateKbps int, onRead func()) (retErr error) {
	if onRead == nil {
		onRead = func() {}
	}
	in, err := av.Open(src)
	if err != nil {
		return fmt.Errorf("audio transcode open: %w", err)
	}
	defer in.Close()
	onRead()

	aStream, err := in.BestStream(ffmpeg.AVMediaTypeAudio)
	if err != nil {
		return fmt.Errorf("audio transcode: no audio stream: %w", err)
	}
	adec, err := av.NewDecoder(aStream)
	if err != nil {
		return fmt.Errorf("audio transcode decoder: %w", err)
	}
	defer adec.Close()

	rate := adec.Raw().SampleRate()
	if rate <= 0 || (encoderName == "libmp3lame" && !mp3Rates[rate]) {
		rate = 44100
	}
	sampleFmt, fmtName := ffmpeg.AVSampleFmtFltp, "fltp"
	if encoderName == "libopus" {
		rate = 48000 // Opus is 48 kHz by definition
		sampleFmt, fmtName = ffmpeg.AVSampleFmtFlt, "flt"
	}

	encName := ffmpeg.ToCStr(encoderName)
	codec := ffmpeg.AVCodecFindEncoderByName(encName)
	encName.Free()
	if codec == nil {
		return fmt.Errorf("encoder %s unavailable", encoderName)
	}
	aenc, err := av.NewEncoder(codec, func(cc *ffmpeg.AVCodecContext) {
		cc.SetSampleFmt(sampleFmt)
		cc.SetSampleRate(rate)
		ffmpeg.AVChannelLayoutDefault(cc.ChLayout(), 2)
		cc.SetBitRate(int64(bitrateKbps) * 1000)
		cc.SetTimeBase(ffmpeg.AVMakeQ(1, rate))
		if encoderName == "vorbis" {
			cc.SetStrictStdCompliance(-2) // native vorbis is 'experimental'
		}
		cc.SetFlags(cc.Flags() | ffmpeg.AVCodecFlagGlobalHeader)
	})
	if err != nil {
		return fmt.Errorf("audio transcode encoder: %w", err)
	}
	defer aenc.Close()

	filter, err := av.NewAudioFilterGraphFromContext(adec.Raw(),
		audioFilterSpecFmt(fmtName, rate, aenc.Raw().FrameSize()),
		sampleFmt, rate, aenc.Raw().ChLayout())
	if err != nil {
		return fmt.Errorf("audio transcode filter: %w", err)
	}
	defer filter.Close()

	out, err := av.CreateOutput(dst)
	if err != nil {
		return fmt.Errorf("audio transcode output: %w", err)
	}
	// Fragmented MP4 for AAC, so the file plays while it is still being
	// written: a plain .m4a keeps its index (the moov atom) for the end,
	// and a player reading the growing file would wait for all of it.
	if strings.HasSuffix(strings.ToLower(dst), ".m4a") {
		setMuxerOption(out.Raw(), "movflags", "frag_keyframe+empty_moov+default_base_moof")
	}
	// Closing flushes and closes dst; a failure there leaves it incomplete.
	defer func() {
		if err := out.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("audio transcode close: %w", err)
		}
	}()
	aOut, err := out.AddStream(aenc)
	if err != nil {
		return fmt.Errorf("audio transcode stream: %w", err)
	}
	if err := out.WriteHeader(); err != nil {
		return fmt.Errorf("audio transcode header: %w", err)
	}

	writePkt := func(pkt *ffmpeg.AVPacket) error {
		ffmpeg.AVPacketRescaleTs(pkt, aenc.Raw().TimeBase(), aOut.TimeBase())
		pkt.SetStreamIndex(aOut.Index())
		return out.WritePacket(pkt)
	}
	encode := func(frame *ffmpeg.AVFrame) error {
		return aenc.Encode(frame, writePkt)
	}

	pkt := ffmpeg.AVPacketAlloc()
	defer ffmpeg.AVPacketFree(&pkt)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := in.ReadPacket(pkt)
		onRead()
		if errors.Is(err, ffmpeg.AVErrorEOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("audio transcode read: %w", err)
		}
		if pkt.StreamIndex() != aStream.Index() {
			ffmpeg.AVPacketUnref(pkt)
			continue
		}
		err = adec.Decode(pkt, func(frame *ffmpeg.AVFrame) error {
			frame.SetPts(frame.BestEffortTimestamp())
			if err := filter.Push(frame); err != nil {
				return err
			}
			return filter.Pull(encode)
		})
		if err != nil {
			return fmt.Errorf("audio transcode: %w", err)
		}
		ffmpeg.AVPacketUnref(pkt)
	}
	if err := adec.Flush(func(frame *ffmpeg.AVFrame) error {
		frame.SetPts(frame.BestEffortTimestamp())
		if err := filter.Push(frame); err != nil {
			return err
		}
		return filter.Pull(encode)
	}); err != nil {
		return fmt.Errorf("audio transcode flush: %w", err)
	}
	if err := filter.Push(nil); err == nil {
		_ = filter.Pull(encode)
	}
	if err := aenc.Flush(writePkt); err != nil {
		return fmt.Errorf("audio transcode flush encoder: %w", err)
	}
	return out.WriteTrailer()
}

// setMuxerOption sets a private option on a muxer before its header is
// written. An option the muxer does not know is ignored: it is a hint, not
// a requirement.
func setMuxerOption(ctx *ffmpeg.AVFormatContext, name, value string) {
	if ctx == nil || ctx.PrivData() == nil {
		return
	}
	k, v := ffmpeg.ToCStr(name), ffmpeg.ToCStr(value)
	defer k.Free()
	defer v.Free()
	_, _ = ffmpeg.AVOptSet(ctx.PrivData(), k, v, 0)
}
