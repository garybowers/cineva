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

type Info struct {
	RuntimeSecs int
	Container   string
	VideoCodec  string
	AudioCodec  string
	Width       int
	Height      int
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
	return info, nil
}
