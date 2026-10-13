package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Playback stalls when the transcoder produces video slower than it is
// watched. This measures the ratio directly: anything under 1.0 means the
// buffer drains and the player pauses, however much CPU is spare.
// This is a timing test, so it is only meaningful on a machine that is not
// otherwise busy. It is here because the failure it guards against — a
// transcode that cannot sustain realtime — is invisible to every other test
// in this package: the output is correct, there is simply not enough of it.
func TestTranscodeKeepsUpWithRealtime(t *testing.T) {
	if _, err := os.Stat(hdrSample); err != nil {
		t.Skip("4K sample not present")
	}
	hw := DetectHW("auto")
	if hw == nil {
		t.Skip("no hardware transcoder")
	}
	defer hw.Close()

	run := func(label string, height int, hdr string) {
		dir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		const segSecs = 6
		const want = 5 // segments to produce
		start := time.Now()
		job := HLSJob{
			InputPath: hdrSample, OutDir: dir, SegmentSecs: segSecs,
			StartSegment: 300, VideoHeight: height, VideoBitrateKbps: 6000,
			AudioTrack: -1, HW: hw, HDR: hdr,
			Throttle: func(_ context.Context, pos int64) error {
				if pos > 300*segSecs+want*segSecs {
					cancel()
				}
				return nil
			},
		}
		_ = RunHLS(ctx, job)
		wall := time.Since(start)
		files, _ := filepath.Glob(filepath.Join(dir, "*.m4s"))
		produced := time.Duration(len(files)*segSecs) * time.Second
		if produced == 0 {
			t.Errorf("%s: produced nothing", label)
			return
		}
		ratio := produced.Seconds() / wall.Seconds()
		verdict := "keeps up"
		if ratio < 1.0 {
			verdict = "TOO SLOW — playback will stall"
		} else if ratio < 1.5 {
			verdict = "marginal"
		}
		t.Logf("%-22s %4.1fs of video in %5.1fs wall = %.2fx realtime  (%s)",
			label, produced.Seconds(), wall.Seconds(), ratio, verdict)
		// A ratio under 1 means the buffer drains and the player pauses
		// however much CPU is spare, which is what HDR did before the tone
		// map was moved after the downscale. 1.2 rather than 1.0 because a
		// stream that only just keeps up stalls on the first busy scene.
		if ratio < 1.2 {
			t.Errorf("%s runs at %.2fx realtime — playback will stall", label, ratio)
		}
	}

	run("4K HDR -> 1080p", 1080, "pq")
	run("4K HDR -> 720p", 720, "pq")
	run("4K as SDR -> 1080p", 1080, "")
}
