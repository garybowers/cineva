package media

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

// AudioInfo is what a music file yields: technical info plus its tags.
type AudioInfo struct {
	RuntimeSecs int
	Codec       string
	Container   string

	Title       string
	Artist      string
	AlbumArtist string
	Album       string
	TrackNum    int
	DiscNum     int
	Year        int
}

// parseNum handles "3" and "3/12" style track/disc tags.
func parseNum(v string) int {
	v, _, _ = strings.Cut(v, "/")
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

// ProbeAudio reads technical info and format-level tags from an audio file.
func ProbeAudio(path string) (AudioInfo, error) {
	in, err := av.Open(path)
	if err != nil {
		return AudioInfo{}, fmt.Errorf("probe audio %s: %w", path, err)
	}
	defer in.Close()

	meta := in.Raw().Metadata()
	info := AudioInfo{
		Container:   strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."),
		Title:       dictValue(meta, "title"),
		Artist:      dictValue(meta, "artist"),
		AlbumArtist: dictValue(meta, "album_artist"),
		Album:       dictValue(meta, "album"),
		TrackNum:    parseNum(dictValue(meta, "track")),
		DiscNum:     parseNum(dictValue(meta, "disc")),
	}
	if d := in.Raw().Duration(); d > 0 {
		info.RuntimeSecs = int(d / ffmpeg.AVTimeBase)
	}
	if as, err := in.BestStream(ffmpeg.AVMediaTypeAudio); err == nil && as != nil {
		info.Codec = ffmpeg.AVCodecGetName(as.Codecpar().CodecId()).String()
	}
	if date := dictValue(meta, "date"); len(date) >= 4 {
		info.Year, _ = strconv.Atoi(date[:4])
	}
	return info, nil
}

// ExtractCoverArt returns the embedded cover image (attached picture stream)
// of an audio file, or nil when there is none.
func ExtractCoverArt(path string) []byte {
	in, err := av.Open(path)
	if err != nil {
		return nil
	}
	defer in.Close()

	streams := in.Streams()
	for i := uint(0); i < in.NbStreams(); i++ {
		st := streams.Get(uintptr(i))
		if st.Disposition()&ffmpeg.AVDispositionAttachedPic == 0 {
			continue
		}
		pkt := st.AttachedPic()
		if pkt == nil || pkt.Size() == 0 {
			continue
		}
		data := unsafe.Slice((*byte)(pkt.Data()), pkt.Size())
		out := make([]byte, len(data))
		copy(out, data)
		return out
	}
	return nil
}
