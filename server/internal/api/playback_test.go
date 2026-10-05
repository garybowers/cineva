package api

import (
	"testing"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

// chromeLinux is the capability set a desktop Chrome on Linux reports: no
// mkv, no HEVC, no E-AC-3.
var chromeLinux = capabilities{
	Containers:  []string{"mp4", "m4v", "webm"},
	VideoCodecs: []string{"h264", "vp8", "vp9", "av1"},
	AudioCodecs: []string{"aac", "mp3", "opus", "vorbis", "flac"},
}

func TestAutoTranscodeReEncodesUnsupportedVideo(t *testing.T) {
	tests := []struct {
		name       string
		item       store.Item
		caps       capabilities
		info       media.Info // Dolby Vision / HDR facts from the probe
		wantDirect bool
		wantHeight int // 0 = remux (copy the video stream)
	}{
		{
			// The freeze: transcoding was chosen because the device cannot
			// decode HEVC, then the HEVC was copied into the HLS output.
			name:       "hevc source is re-encoded, never copied",
			item:       store.Item{Container: "mkv", VideoCodec: "hevc", AudioCodec: "eac3", Height: 2160},
			caps:       chromeLinux,
			wantHeight: 1080,
		},
		{
			// A device that really can decode HEVC gets the video copied
			// bit-for-bit: only the container and audio are the problem.
			name:       "hevc on an hevc-capable device is remuxed, not re-encoded",
			item:       store.Item{Container: "mkv", VideoCodec: "hevc", AudioCodec: "eac3", Height: 2160},
			caps:       capabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac"}},
			info:       media.Info{DVProfile: 8, DVBLCompat: 1}, // HDR10 base layer
			wantHeight: 0,
		},
		{
			// Profile 5's base layer is IPT: copying it gives false colour,
			// so it has to be re-encoded even though the device says hevc.
			name:       "dolby vision profile 5 is re-encoded",
			item:       store.Item{Container: "mkv", VideoCodec: "hevc", AudioCodec: "eac3", Height: 2160},
			caps:       capabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac"}},
			info:       media.Info{DVProfile: 5, DVBLCompat: 0},
			wantHeight: 1080,
		},
		{
			name:       "container-only mismatch remuxes",
			item:       store.Item{Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Height: 1080},
			caps:       chromeLinux,
			wantHeight: 0,
		},
		{
			name:       "audio-only mismatch remuxes",
			item:       store.Item{Container: "mp4", VideoCodec: "h264", AudioCodec: "eac3", Height: 1080},
			caps:       chromeLinux,
			wantHeight: 0,
		},
		{
			name:       "fully supported direct plays",
			item:       store.Item{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Height: 1040},
			caps:       chromeLinux,
			wantDirect: true,
		},
		{
			name:       "unsupported codec on a 720p source drops a rung",
			item:       store.Item{Container: "mkv", VideoCodec: "hevc", AudioCodec: "aac", Height: 720},
			caps:       chromeLinux,
			wantHeight: 720,
		},
		{
			name:       "unknown height is treated as full size",
			item:       store.Item{Container: "mkv", VideoCodec: "hevc", AudioCodec: "aac"},
			caps:       chromeLinux,
			wantHeight: 1080,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := directPlayReason(tt.item, tt.caps)
			if tt.wantDirect {
				if reason != "" {
					t.Fatalf("expected direct play, got transcode: %s", reason)
				}
				return
			}
			if reason == "" {
				t.Fatal("expected a transcode reason, got direct play")
			}
			height := 0 // 0 = remux: the video stream is copied
			if !videoCodecSupported(tt.item, tt.caps) || !remuxSafe(tt.item, tt.info) {
				height = autoVideoQuality(tt.item).Height
			}
			if height != tt.wantHeight {
				t.Errorf("encode height = %d, want %d", height, tt.wantHeight)
			}
		})
	}
}
