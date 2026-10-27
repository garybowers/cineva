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
	Compilation bool
	// Genre is the genre tag as written, which may hold several separated
	// by ";" or "/" — splitting is the scanner's business.
	Genre string

	// What the file is: kbit/s (the stream's, else the file's), Hz, bits
	// per sample (0 for lossy codecs, which have none) and channels.
	Bitrate    int
	SampleRate int
	BitDepth   int
	Channels   int
	// The MusicBrainz ids Picard writes: the release (album), the
	// recording (this track) and the album artist. Empty when untagged.
	MBReleaseID   string
	MBRecordingID string
	MBArtistID    string
	// Lyrics is the lyrics tag as written (USLT in ID3, LYRICS or
	// UNSYNCEDLYRICS in Vorbis comments), unsynced or timed.
	Lyrics string
}

// tagAny returns the first of the keys the file carries, compared without
// case: the same tag is spelt differently by ID3 (TXXX "MusicBrainz Album
// Id"), Vorbis comments (MUSICBRAINZ_ALBUMID) and MP4 freeform atoms.
func tagAny(meta *ffmpeg.AVDictionary, keys ...string) string {
	for _, k := range keys {
		if v := dictValue(meta, k); v != "" {
			return v
		}
	}
	return ""
}

// lyricsTag finds the lyrics tag: ID3's USLT arrives as "lyrics-eng" (the
// language as a suffix), so the key is matched by prefix.
func lyricsTag(meta *ffmpeg.AVDictionary) string {
	if meta == nil {
		return ""
	}
	for _, key := range []string{"lyrics", "unsyncedlyrics", "unsynced lyrics"} {
		k := ffmpeg.ToCStr(key)
		entry := ffmpeg.AVDictGet(meta, k, nil, ffmpeg.AVDictIgnoreSuffix)
		k.Free()
		if entry != nil {
			if v := entry.Value(); v != nil {
				return v.String()
			}
		}
	}
	return ""
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
		Genre:       dictValue(meta, "genre"),
		MBReleaseID: tagAny(meta, "MUSICBRAINZ_ALBUMID", "MusicBrainz Album Id"),
		MBRecordingID: tagAny(meta, "MUSICBRAINZ_TRACKID", "MusicBrainz Track Id",
			"MUSICBRAINZ_RECORDINGID", "MusicBrainz Recording Id"),
		MBArtistID: tagAny(meta, "MUSICBRAINZ_ALBUMARTISTID", "MusicBrainz Album Artist Id",
			"MUSICBRAINZ_ARTISTID", "MusicBrainz Artist Id"),
		Lyrics: lyricsTag(meta),
	}
	switch strings.ToLower(dictValue(meta, "compilation")) {
	case "1", "true", "yes":
		info.Compilation = true
	}
	if d := in.Raw().Duration(); d > 0 {
		info.RuntimeSecs = int(d / ffmpeg.AVTimeBase)
	}
	if as, err := in.BestStream(ffmpeg.AVMediaTypeAudio); err == nil && as != nil {
		par := as.Codecpar()
		info.Codec = ffmpeg.AVCodecGetName(par.CodecId()).String()
		info.SampleRate = par.SampleRate()
		if ch := par.ChLayout(); ch != nil {
			info.Channels = ch.NbChannels()
		}
		// Lossless codecs carry their sample size; a lossy one reports 0
		// and has none to report.
		info.BitDepth = par.BitsPerRawSample()
		if info.BitDepth == 0 {
			info.BitDepth = par.BitsPerCodedSample()
		}
		info.Bitrate = int(par.BitRate() / 1000)
	}
	// FLAC and some MP3s leave the stream's bit rate unset; the container
	// works one out from size and duration.
	if info.Bitrate == 0 {
		info.Bitrate = int(in.Raw().BitRate() / 1000)
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
