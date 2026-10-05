package media

import (
	"os"
	"testing"
)

// Dolby Vision and HDR detection against a real file. The layout of
// AV_PKT_DATA_DOVI_CONF is not documented in the bindings and was read off a
// real stream, so this guards it against a future ffmpeg bump changing it.
// Point PROBE_FILE at an HDR/DV file to run it.
func TestProbeHDRDolbyVision(t *testing.T) {
	p := os.Getenv("PROBE_FILE")
	if p == "" {
		t.Skip("set PROBE_FILE to an HDR or Dolby Vision file to run")
	}
	info, err := Probe(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("codec=%s %dx%d hdr=%q dvProfile=%d dvBLCompat=%d",
		info.VideoCodec, info.Width, info.Height, info.HDRFormat, info.DVProfile, info.DVBLCompat)
	if info.DVProfile != 0 && info.DVProfile < 4 || info.DVProfile > 10 {
		t.Errorf("dv profile %d is outside the plausible range — side-data layout may have changed", info.DVProfile)
	}
}
