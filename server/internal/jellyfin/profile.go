package jellyfin

import "strings"

// Playable is what a device profile says the client can play as it is:
// containers and codecs in the names Cineva's playback decision compares
// against (the probe's: mkv, mp4, h264, hevc, aac, eac3, ...). Nil lists
// mean the profile named nothing, and the server's conservative defaults
// apply.
type Playable struct {
	Containers  []string
	VideoCodecs []string
	AudioCodecs []string
	// MaxBitrate is the client's streaming ceiling in bit/s; 0 for none.
	MaxBitrate int
	// ExternalSubtitles says the client can fetch text subtitles as files
	// and overlay them itself, so a subtitle need not force a transcode.
	ExternalSubtitles bool
}

// Every codec a client might mean when its profile names none, in the
// probe's spellings.
var (
	anyVideoCodec = []string{"h264", "hevc", "av1", "vp9", "vp8", "mpeg4", "mpeg2video", "mpeg1video", "msmpeg4v3", "vc1"}
	anyAudioCodec = []string{"aac", "ac3", "eac3", "mp3", "mp2", "flac", "opus", "vorbis", "dts", "truehd", "pcm_s16le", "pcm_s24le", "alac", "wmav2"}
)

// codecAliases maps the names clients use onto the probe's.
var codecAliases = map[string]string{
	"h265": "hevc", "x265": "hevc", "x264": "h264", "avc": "h264", "dca": "dts", "ac-3": "ac3", "e-ac-3": "eac3",
	"mp4a": "aac", "mpeg2": "mpeg2video", "pcm": "pcm_s16le", "mp4v": "mpeg4", "xvid": "mpeg4", "divx": "mpeg4",
}

// containerAliases maps container names onto the file extensions Cineva
// stores.
var containerAliases = map[string][]string{
	"mp4":      {"mp4", "m4v"},
	"m4v":      {"m4v", "mp4"},
	"mkv":      {"mkv"},
	"matroska": {"mkv"},
	"mov":      {"mov"},
	"webm":     {"webm"},
	"ts":       {"ts", "m2ts", "mts"},
	"mpegts":   {"ts", "m2ts", "mts"},
	"m2ts":     {"m2ts"},
	"avi":      {"avi"},
	"wmv":      {"wmv"},
	"asf":      {"wmv", "asf"},
	"flv":      {"flv"},
	"mpeg":     {"mpg", "mpeg"},
	"mpg":      {"mpg", "mpeg"},
	"3gp":      {"3gp"},
	"ogv":      {"ogv"},
}

// Read turns a device profile into what the client can play. A nil
// profile, or one with no video direct-play profile, gives nil lists.
func Read(p *DeviceProfile) Playable {
	var out Playable
	if p == nil {
		return out
	}
	out.MaxBitrate = p.MaxStreamingBitrate
	seen := map[string]bool{}
	add := func(list *[]string, vals []string) {
		for _, v := range vals {
			if v != "" && !seen[v] {
				seen[v] = true
				*list = append(*list, v)
			}
		}
	}
	for _, dp := range p.DirectPlayProfiles {
		if !strings.EqualFold(dp.Type, "Video") {
			continue
		}
		for _, c := range ParseList(strings.ToLower(dp.Container)) {
			if exts, ok := containerAliases[c]; ok {
				add(&out.Containers, exts)
			} else {
				add(&out.Containers, []string{c})
			}
		}
		if dp.VideoCodec == "" {
			add(&out.VideoCodecs, anyVideoCodec)
		} else {
			add(&out.VideoCodecs, codecs(dp.VideoCodec))
		}
		if dp.AudioCodec == "" {
			add(&out.AudioCodecs, anyAudioCodec)
		} else {
			add(&out.AudioCodecs, codecs(dp.AudioCodec))
		}
	}
	for _, sp := range p.SubtitleProfiles {
		if strings.EqualFold(sp.Method, "External") {
			out.ExternalSubtitles = true
		}
	}
	return out
}

// codecs splits a profile's codec list into the probe's names.
func codecs(list string) []string {
	var out []string
	for _, c := range ParseList(strings.ToLower(list)) {
		if a, ok := codecAliases[c]; ok {
			c = a
		}
		out = append(out, c)
	}
	return out
}

// QualityFor is the quality level Cineva should transcode at when the
// file's bit rate (bit/s) is over the client's ceiling: "" when it is not
// (play as the device can), else the highest of Cineva's levels under the
// ceiling.
func QualityFor(maxBitrate, fileBitrate int) string {
	if maxBitrate <= 0 || fileBitrate <= 0 || fileBitrate <= maxBitrate {
		return ""
	}
	switch {
	case maxBitrate >= 8_000_000:
		return "1080p"
	case maxBitrate >= 4_000_000:
		return "720p"
	default:
		return "480p"
	}
}
