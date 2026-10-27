package media

import (
	"fmt"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

// AudioStream is one embedded audio track: a language, a commentary, or a
// different mix of the same programme.
type AudioStream struct {
	Index    int
	Codec    string
	Language string
	Title    string
	Channels int
	BitRate  int64
	Default  bool
}

// ProbeAudioTracks lists the audio streams of a media file, in file order.
// A file with one track still reports it, so the player can name what it is
// playing rather than showing an empty picker. Probe reads them too
// (Info.AudioTracks), from its one open.
func ProbeAudioTracks(path string) ([]AudioStream, error) {
	in, err := av.Open(path)
	if err != nil {
		return nil, fmt.Errorf("probe audio %s: %w", path, err)
	}
	defer in.Close()
	return audioStreams(in), nil
}

func audioStreams(in *av.Input) []AudioStream {
	var out []AudioStream
	streams := in.Streams()
	for i := uint(0); i < in.NbStreams(); i++ {
		st := streams.Get(uintptr(i))
		par := st.Codecpar()
		if ffmpeg.AVCodecGetType(par.CodecId()) != ffmpeg.AVMediaTypeAudio {
			continue
		}
		ch := 0
		if layout := par.ChLayout(); layout != nil {
			ch = layout.NbChannels()
		}
		out = append(out, AudioStream{
			Index:    st.Index(),
			Codec:    ffmpeg.AVCodecGetName(par.CodecId()).String(),
			Language: dictValue(st.Metadata(), "language"),
			Title:    dictValue(st.Metadata(), "title"),
			Channels: ch,
			BitRate:  par.BitRate(),
			Default:  st.Disposition()&ffmpeg.AVDispositionDefault != 0,
		})
	}
	return out
}
