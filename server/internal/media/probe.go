// Package media wraps ffmpeg-statigo for media inspection (and, in milestone 3,
// transcoding).
package media

import (
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

// Tags is embedded metadata found in a video container (iTunes-style mp4/m4v
// tags mostly; mkv rarely carries any).
type Tags struct {
	Title      string
	Year       int
	Overview   string
	Genre      string
	Show       string
	SeasonNum  int
	EpisodeNum int
}

type Info struct {
	RuntimeSecs int
	Container   string
	VideoCodec  string
	AudioCodec  string
	Width       int
	Height      int
	Tags        Tags
	HasCoverArt bool
	// Subtitles and AudioTracks are the file's embedded subtitle and
	// audio streams, read from the same open as the rest: a scan records
	// them per file, and opening a file three times for three lists was
	// most of what a file cost a scan.
	Subtitles   []SubtitleStream
	AudioTracks []AudioStream

	// HDRFormat is "pq", "hlg" or "" — set from the transfer characteristic.
	// Re-encoding HDR without tone mapping produces the washed-out, flat
	// picture you get from showing PQ-coded pixels as if they were SDR.
	HDRFormat string
	// FrameRateNum/Den is the video's average frame rate as the exact
	// rational the container stores (24000/1001, not 23.976). Kept exact
	// because the HLS segment duration is derived from it, and rounding
	// here is what makes a playlist's timings drift over a long film.
	FrameRateNum int
	FrameRateDen int

	// Chapters are the container's own chapter marks, in order. Empty for
	// the many files that carry none. Read here rather than stored, because
	// the play handler already probes the file and chapters are a property
	// of the file, not of the library.
	Chapters []Chapter

	// DVProfile is the Dolby Vision profile, 0 when the stream carries none.
	// DVBLCompat is its base-layer compatibility id: 1 = HDR10, 2 = SDR,
	// 4 = HLG, 0 = none (profile 5's IPT base layer, which is false colour
	// on anything without a Dolby Vision pipeline).
	DVProfile  int
	DVBLCompat int
}

// Chapter is one chapter mark: where it starts, and what it is called.
// Titles are often absent (a disc rip usually numbers them), so the caller
// supplies a fallback rather than this inventing one.
type Chapter struct {
	StartSecs float64 `json:"startSecs"`
	EndSecs   float64 `json:"endSecs"`
	Title     string  `json:"title,omitempty"`
}

// Probe opens the file with the bundled FFmpeg and extracts technical info.
func Probe(path string) (Info, error) {
	in, err := av.Open(path)
	if err != nil {
		return Info{}, fmt.Errorf("probe %s: %w", path, err)
	}
	defer in.Close()

	info := Info{
		Container: strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."),
	}
	if d := in.Raw().Duration(); d > 0 {
		info.RuntimeSecs = int(d / ffmpeg.AVTimeBase)
	}
	if vs, err := in.BestStream(ffmpeg.AVMediaTypeVideo); err == nil && vs != nil {
		par := vs.Codecpar()
		info.VideoCodec = ffmpeg.AVCodecGetName(par.CodecId()).String()
		info.Width = par.Width()
		info.Height = par.Height()
		switch par.ColorTrc() {
		case ffmpeg.AVColTrcSmpte2084:
			info.HDRFormat = "pq"
		case ffmpeg.AVColTrcAribStdB67:
			info.HDRFormat = "hlg"
		}
		info.DVProfile, info.DVBLCompat = dolbyVision(par)
		if fr := vs.AvgFrameRate(); fr != nil && fr.Num() > 0 && fr.Den() > 0 {
			info.FrameRateNum, info.FrameRateDen = fr.Num(), fr.Den()
		}
	}
	if as, err := in.BestStream(ffmpeg.AVMediaTypeAudio); err == nil && as != nil {
		info.AudioCodec = ffmpeg.AVCodecGetName(as.Codecpar().CodecId()).String()
	}

	info.Chapters = readChapters(in.Raw())

	meta := in.Raw().Metadata()
	info.Tags = Tags{
		Title:      dictValue(meta, "title"),
		Genre:      dictValue(meta, "genre"),
		Show:       dictValue(meta, "show"),
		SeasonNum:  parseNum(dictValue(meta, "season_number")),
		EpisodeNum: parseNum(dictValue(meta, "episode_sort")),
	}
	if info.Tags.Overview = dictValue(meta, "synopsis"); info.Tags.Overview == "" {
		info.Tags.Overview = dictValue(meta, "description")
	}
	if date := dictValue(meta, "date"); len(date) >= 4 {
		info.Tags.Year = parseNum(date[:4]) // zero when the tag is not a year
	}
	streams := in.Streams()
	for i := uint(0); i < in.NbStreams(); i++ {
		if streams.Get(uintptr(i)).Disposition()&ffmpeg.AVDispositionAttachedPic != 0 {
			info.HasCoverArt = true
			break
		}
	}
	info.Subtitles = subtitleStreams(in)
	info.AudioTracks = audioStreams(in)
	return info, nil
}

// dolbyVision reads the Dolby Vision configuration from the stream's coded
// side data, returning the profile and base-layer compatibility id, or 0, 0
// when the stream carries none.
//
// AV_PKT_DATA_DOVI_CONF is an AVDOVIDecoderConfigurationRecord — a struct
// with one byte per field, not the bit-packed record from the ISO box. The
// layout was read off a real Dolby Vision stream rather than assumed:
//
//	01 00 08 06 01 00 01 01 00
//	 |  |  |  |  |  |  |  |
//	 |  |  |  |  |  |  |  +-- dv_bl_signal_compatibility_id (1 = HDR10)
//	 |  |  |  |  |  |  +----- bl_present_flag
//	 |  |  |  |  |  +-------- el_present_flag
//	 |  |  |  |  +----------- rpu_present_flag
//	 |  |  |  +-------------- dv_level
//	 |  |  +----------------- dv_profile
//	 |  +-------------------- dv_version_minor
//	 +----------------------- dv_version_major
func dolbyVision(par *ffmpeg.AVCodecParameters) (profile, blCompat int) {
	sd := ffmpeg.AVPacketSideDataGet(par.CodedSideData(), par.NbCodedSideData(), ffmpeg.AVPktDataDoviConf)
	if sd == nil || sd.Size() < 8 {
		return 0, 0
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(sd.Data())), sd.Size())
	return int(b[2]), int(b[7])
}

// readChapters pulls the container's chapter marks out of the format
// context. Each chapter carries its own time base — mkv uses nanoseconds,
// mp4 the movie timescale — so the raw values mean nothing until rescaled.
func readChapters(ctx *ffmpeg.AVFormatContext) []Chapter {
	n := int(ctx.NbChapters())
	if n == 0 {
		return nil
	}
	arr := ctx.Chapters()
	if arr == nil {
		return nil
	}
	out := make([]Chapter, 0, n)
	for i := 0; i < n; i++ {
		ch := arr.Get(uintptr(i))
		if ch == nil {
			continue
		}
		tb := ch.TimeBase()
		if tb == nil || tb.Den() == 0 {
			continue
		}
		scale := float64(tb.Num()) / float64(tb.Den())
		c := Chapter{
			StartSecs: float64(ch.Start()) * scale,
			EndSecs:   float64(ch.End()) * scale,
			Title:     dictValue(ch.Metadata(), "title"),
		}
		// A zero-length or backwards chapter is malformed; skipping to it
		// would strand the player.
		if c.EndSecs > c.StartSecs {
			out = append(out, c)
		}
	}
	return out
}
