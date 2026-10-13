package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Tone mapping runs on every pixel, so doing it before the downscale costs
// four times the work on a 4K source. This measures what scaling first buys.
func TestZToneMapOrderThroughput(t *testing.T) {
	if _, err := os.Stat(hdrSample); err != nil {
		t.Skip("no sample")
	}
	hw := DetectHW("auto")
	if hw == nil {
		t.Skip("no hw")
	}
	defer hw.Close()

	for _, tc := range []struct {
		label string
		spec  string
	}{
		{"tone map then scale (today)", toneChainAfterScale(1920, 1080, false)},
		{"scale first (pinned colour)", toneChainScaleFirstPinned(1920, 1080)},
		{"scale in linear light", toneChainLinearScale(1920, 1080)},
	} {
		dir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		const segSecs, want = 6, 5
		start := time.Now()
		job := HLSJob{
			InputPath: hdrSample, OutDir: dir, SegmentSecs: segSecs,
			StartSegment: 300, VideoHeight: 1080, VideoBitrateKbps: 6000,
			AudioTrack: -1, HW: hw, HDR: "pq",
			FilterSpecOverride: tc.spec,
			Throttle: func(_ context.Context, pos int64) error {
				if pos > 300*segSecs+want*segSecs {
					cancel()
				}
				return nil
			},
		}
		_ = RunHLS(ctx, job)
		wall := time.Since(start)
		cancel()
		files, _ := filepath.Glob(filepath.Join(dir, "*.m4s"))
		produced := float64(len(files) * segSecs)
		if produced == 0 {
			t.Errorf("%s produced nothing", tc.label)
			continue
		}
		t.Logf("%-28s %4.0fs of video in %5.1fs = %.2fx realtime", tc.label, produced, wall.Seconds(), produced/wall.Seconds())
	}
}

// toneChainAfterScale builds the current chain, optionally with the scale
// moved to the front.
func toneChainAfterScale(w, h int, scaleFirst bool) string {
	scale := fmt.Sprintf("scale=%d:%d", w, h)
	chain := "zscale=tin=smpte2084:t=linear:npl=100," +
		"format=gbrpf32le,zscale=p=bt709," +
		"tonemap=tonemap=hable:desat=0," +
		"zscale=t=bt709:m=bt709:r=tv"
	if scaleFirst {
		return scale + "," + chain
	}
	return chain + "," + scale
}

// toneChainScaleFirstPinned scales first, then states the input colour
// properties outright instead of relying on what survives the scale.
//
// swscale does not carry HDR metadata through, so a bare "scale,zscale=tin=…"
// leaves zimg with no idea what it is being handed and it refuses the frame.
// Naming the matrix, primaries and range makes the chain independent of that.
func toneChainScaleFirstPinned(w, h int) string {
	return fmt.Sprintf("scale=%d:%d,", w, h) +
		"zscale=tin=smpte2084:min=bt2020nc:pin=bt2020:rin=tv:t=linear:npl=100," +
		"format=gbrpf32le,zscale=p=bt709," +
		"tonemap=tonemap=hable:desat=0," +
		"zscale=t=bt709:m=bt709:r=tv"
}

// toneChainLinearScale scales after the conversion to linear light but
// before the tone map. Downscaling in linear light is the correct way to do
// it — averaging gamma-coded values darkens the result — so this is the
// order that is both faster than today and more defensible than scaling the
// PQ-coded frame directly.
func toneChainLinearScale(w, h int) string {
	return "zscale=tin=smpte2084:t=linear:npl=100," +
		"format=gbrpf32le," +
		fmt.Sprintf("scale=%d:%d,", w, h) +
		"zscale=p=bt709," +
		"tonemap=tonemap=hable:desat=0," +
		"zscale=t=bt709:m=bt709:r=tv"
}

// Scaling before the tone map is more than twice as fast, but it tone maps
// an already-downscaled picture. This measures what that costs in the
// picture itself, rather than assuming it is either free or ruinous.
func TestZToneMapOrderPicture(t *testing.T) {
	if _, err := os.Stat(hdrSample); err != nil {
		t.Skip("no sample")
	}
	const w, h = 640, 360
	today := lumaPlanesSpec(t, hdrSample, toneChainAfterScale(w, h, false))
	first := lumaPlanesSpec(t, hdrSample, toneChainScaleFirstPinned(w, h))
	linear := lumaPlanesSpec(t, hdrSample, toneChainLinearScale(w, h))

	// Same chain twice must be identical, or the measurement means nothing.
	if d := meanAbsDiff(t, today, lumaPlanesSpec(t, hdrSample, toneChainAfterScale(w, h, false))); d != 0 {
		t.Fatalf("the control differs from itself by %.3f", d)
	}
	t.Logf("mean luma — today %.1f, scale-first %.1f, linear-scale %.1f",
		mean(today[0]), mean(first[0]), mean(linear[0]))
	t.Logf("difference from today: scale-first(pinned) %.2f/255, linear-scale %.2f/255",
		meanAbsDiff(t, today, first), meanAbsDiff(t, today, linear))
}
