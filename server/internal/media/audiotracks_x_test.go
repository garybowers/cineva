package media

import (
	"os"
	"testing"
)

func TestProbeAudioTracksReal(t *testing.T) {
	p := os.Getenv("PROBE_FILE")
	if p == "" {
		t.Skip("set PROBE_FILE")
	}
	tracks, err := ProbeAudioTracks(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range tracks {
		t.Logf("idx=%d codec=%-6s lang=%-4s ch=%d bitrate=%d default=%v title=%q",
			a.Index, a.Codec, a.Language, a.Channels, a.BitRate, a.Default, a.Title)
	}
}
