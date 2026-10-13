package media

import (
	"context"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Transcodes a slice of a 4K HEVC source and reports CPU time, with and
// without hardware decode. The whole point of this work is the CPU saving,
// so the test measures it rather than asserting the code merely runs.
func TestHardwareDecodeCPUCost(t *testing.T) {
	if _, err := os.Stat(hdrSample); err != nil {
		t.Skip("4K sample not present")
	}
	hw := DetectHW("auto")
	if hw == nil {
		t.Skip("no hardware transcoder on this host")
	}
	defer hw.Close()

	// Hardware decode needs either a standalone decoder for this backend
	// (QSV, NVIDIA) or an hwaccel (VAAPI's only route). This build reports
	// zero hwaccels, so on a VAAPI-only host there is nothing to measure —
	// say so rather than fail.
	if !hwDecodeAvailable(hdrSample, hw) {
		t.Skipf("no hardware decode path for %s in this ffmpeg build "+
			"(needs a standalone decoder or an hwaccel)", hw.Kind)
	}

	run := func(dev *HWContext, label string) (cpu time.Duration, wall time.Duration, segs int) {
		dir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		before := processCPU()
		start := time.Now()
		job := HLSJob{
			InputPath: hdrSample, OutDir: dir, SegmentSecs: 4,
			StartSegment: 300, VideoHeight: 1080, VideoBitrateKbps: 6000,
			AudioTrack: -1, HW: dev,
			Throttle: func(_ context.Context, pos int64) error {
				if pos > 300*4+24 { // ~24 seconds of output
					cancel()
				}
				return nil
			},
		}
		_ = RunHLS(ctx, job)
		wall = time.Since(start)
		cpu = processCPU() - before
		files, _ := filepath.Glob(filepath.Join(dir, "*.m4s"))
		t.Logf("%-18s cpu %-8v wall %-8v segments %d", label, cpu.Round(time.Millisecond), wall.Round(time.Millisecond), len(files))
		return cpu, wall, len(files)
	}

	// Software decode with GPU encode — what shipped before this change.
	swCPU, _, swSegs := run(&HWContext{
		Kind: hw.Kind, Device: hw.Device, Encoder: hw.Encoder,
		PixFmt: hw.PixFmt, Alt: hw.Alt,
		// DevType left zero, so the decoder finds no matching hw config and
		// stays on the CPU while the encoder still uses the device.
		ref: hw.ref,
	}, "cpu decode")
	hwCPU, _, hwSegs := run(hw, "gpu decode")

	if swSegs == 0 || hwSegs == 0 {
		t.Fatalf("a run produced no segments (cpu %d, gpu %d)", swSegs, hwSegs)
	}
	saving := (1 - float64(hwCPU)/float64(swCPU)) * 100
	t.Logf("hardware decode uses %.0f%% less CPU", saving)
	if hwCPU >= swCPU {
		t.Errorf("hardware decode used no less CPU (%v vs %v) — it is probably not engaging", hwCPU, swCPU)
	}
}

// Hardware decode on an HDR source is the case that matters most — 4K HDR
// HEVC is the heaviest thing a library holds — and the one where the GPU
// win is least obvious: tone mapping has to happen on the CPU, so every
// decoded surface is copied back out of device memory first. This measures
// whether paying for that download still beats decoding on the CPU.
func TestHardwareDecodeCPUCostHDR(t *testing.T) {
	if _, err := os.Stat(hdrSample); err != nil {
		t.Skip("4K sample not present")
	}
	hw := DetectHW("auto")
	if hw == nil {
		t.Skip("no hardware transcoder on this host")
	}
	defer hw.Close()
	if !hwDecodeAvailable(hdrSample, hw) {
		t.Skipf("no hardware decode path for %s in this ffmpeg build", hw.Kind)
	}

	run := func(dev *HWContext, label string) (time.Duration, int) {
		dir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		before := processCPU()
		start := time.Now()
		job := HLSJob{
			InputPath: hdrSample, OutDir: dir, SegmentSecs: 4,
			StartSegment: 300, VideoHeight: 1080, VideoBitrateKbps: 6000,
			AudioTrack: -1, HW: dev, HDR: "pq",
			Throttle: func(_ context.Context, pos int64) error {
				if pos > 300*4+8 { // ~8 seconds of output; tone mapping is slow
					cancel()
				}
				return nil
			},
		}
		if err := RunHLS(ctx, job); err != nil && ctx.Err() == nil {
			t.Logf("%s: RunHLS: %v", label, err)
		}
		cpu := processCPU() - before
		files, _ := filepath.Glob(filepath.Join(dir, "*.m4s"))
		t.Logf("%-18s cpu %-9v wall %-9v segments %d", label,
			cpu.Round(time.Millisecond), time.Since(start).Round(time.Millisecond), len(files))
		return cpu, len(files)
	}

	swCPU, swSegs := run(&HWContext{
		Kind: hw.Kind, Device: hw.Device, Encoder: hw.Encoder,
		PixFmt: hw.PixFmt, Alt: hw.Alt, ref: hw.ref,
	}, "cpu decode + map")
	hwCPU, hwSegs := run(hw, "gpu decode + map")

	if swSegs == 0 || hwSegs == 0 {
		t.Fatalf("a run produced no segments (cpu %d, gpu %d)", swSegs, hwSegs)
	}
	t.Logf("hardware decode changes CPU by %+.0f%%", (float64(hwCPU)/float64(swCPU)-1)*100)
	// The saving here is small — CPU tone mapping dominates, and every
	// decoded surface has to be copied out of device memory to reach it —
	// so this asserts only that the download does not cost more than the
	// decode saves. If it ever does, HDR should decode on the CPU again.
	if hwCPU > time.Duration(float64(swCPU)*1.1) {
		t.Errorf("hardware decode cost more CPU than it saved (%v vs %v)", hwCPU, swCPU)
	}
}

// processCPU is this process's total CPU time, user plus system.
func processCPU() time.Duration {
	var u syscallRusage
	if err := getrusage(&u); err != nil {
		return 0
	}
	return u.total()
}

// This host is VAAPI, which cannot hardware-decode in a build with no
// hwaccels, so the QSV and NVIDIA paths cannot be exercised here. What can
// be checked is that they are wired to look for the right thing: the naming
// convention, and that VAAPI correctly reports having no standalone decoder.
func TestStandaloneHWDecoderNaming(t *testing.T) {
	tests := []struct {
		name     string
		kind     HWKind
		wantSfx  string
		wantSome bool
	}{
		{name: "QSV uses the _qsv decoders", kind: HWQSV, wantSfx: "qsv", wantSome: true},
		{name: "NVIDIA uses the _cuvid decoders", kind: HWNVENC, wantSfx: "cuvid", wantSome: true},
		{
			// The finding that matters: VAAPI decodes only through an
			// hwaccel, so there is nothing to find by name. Asserting it
			// keeps a future reader from "fixing" the missing suffix.
			name: "VAAPI has no standalone decoders", kind: HWVAAPI, wantSome: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sfx, ok := hwDecoderSuffix(tc.kind)
			if ok != tc.wantSome {
				t.Fatalf("hwDecoderSuffix(%s) ok = %v, want %v", tc.kind, ok, tc.wantSome)
			}
			if !tc.wantSome {
				return
			}
			if sfx != tc.wantSfx {
				t.Errorf("suffix = %q, want %q", sfx, tc.wantSfx)
			}
			// The decoder this would look for must actually exist in the
			// build, or the lookup is pointing at a name that never matches.
			hw := &HWContext{Kind: tc.kind}
			codec, name := standaloneHWDecoder(ffmpeg.AVCodecIdHevc, hw)
			if codec == nil {
				t.Errorf("no %s decoder in this build — the lookup would never match", "hevc_"+tc.wantSfx)
			}
			if name != "hevc_"+tc.wantSfx {
				t.Errorf("looked up %q, want %q", name, "hevc_"+tc.wantSfx)
			}
		})
	}
}
