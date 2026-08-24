// Package media wraps ffmpeg-statigo for media inspection (and, in milestone 3,
// transcoding).
package media

import (
	"fmt"
	"path/filepath"
	"strings"

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
	}
	if as, err := in.BestStream(ffmpeg.AVMediaTypeAudio); err == nil && as != nil {
		info.AudioCodec = ffmpeg.AVCodecGetName(as.Codecpar().CodecId()).String()
	}

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
		fmt.Sscanf(date[:4], "%d", &info.Tags.Year)
	}
	streams := in.Streams()
	for i := uint(0); i < in.NbStreams(); i++ {
		if streams.Get(uintptr(i)).Disposition()&ffmpeg.AVDispositionAttachedPic != 0 {
			info.HasCoverArt = true
			break
		}
	}
	return info, nil
}
