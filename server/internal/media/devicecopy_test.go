package media

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// sdrSample is a local 8-bit H.264 SDR episode, the kind of file most of a
// library is. Like hdrSample, tests that need it skip where it is absent.
const sdrSample = "/home/gary/Videos/cineva/TV Shows/Succession/Season 03/Succession - S03E01 - Secession HDTV-1080p.mkv"

// referenceLuma renders frames through reference ffmpeg, from the given
// time exactly (an accurate seek, which decodes from the keyframe before it
// and discards up to it — the frames lumaThrough starts on). Skips when
// there is no ffmpeg on the PATH.
func referenceLuma(t *testing.T, src string, from float64, vf string, w, h, n int) [][]byte {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no reference ffmpeg on the PATH")
	}
	out := filepath.Join(t.TempDir(), "ref.yuv")
	cmd := exec.Command(bin, "-v", "error", "-y",
		"-ss", strconv.FormatFloat(from, 'f', 6, 64), "-i", src,
		"-frames:v", strconv.Itoa(n), "-fps_mode", "passthrough",
		"-vf", vf, "-pix_fmt", "yuv420p", "-f", "rawvideo", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reference ffmpeg: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read reference frames: %v", err)
	}
	size := w * h * 3 / 2
	var planes [][]byte
	for i := 0; i+size <= len(data); i += size {
		planes = append(planes, data[i:i+w*h])
	}
	if len(planes) == 0 {
		t.Fatal("reference ffmpeg produced no frames")
	}
	return planes
}

// The software filter graph must produce what reference ffmpeg does with the
// same chain. It used to be built from the decoder context by the vendored
// helper, whose buffer source declares no colour space or range: the link
// then claimed neither, swscale read the source as something it was not,
// and every re-encode on the CPU came out darker — 2.49 levels on the
// untone-mapped 4K PQ sample, 1.70 on the 8-bit SDR episode (75.37 against
// 77.06 mean luma). The tone-mapped chain states its input colour outright,
// so it was off by only 0.12. Declared, all three are bit-exact with
// reference ffmpeg.
func TestCPUGraphMatchesReference(t *testing.T) {
	const w, h = 640, 360
	for _, tc := range []struct {
		name, src, hdr string
		at             int
	}{
		{"10-bit PQ, untone-mapped", hdrSample, "", 65 * 60},
		{"10-bit PQ, tone mapped", hdrSample, "pq", 65 * 60},
		{"8-bit SDR", sdrSample, "", 20 * 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := os.Stat(tc.src); err != nil {
				t.Skip("sample not present")
			}
			spec := videoFilterSpec(tc.hdr, w, h)
			g := &videoGraph{spec: spec + ",format=yuv420p"}
			defer g.Close()
			got, from := lumaThrough(t, tc.src, tc.at, nil, g)
			ref := referenceLuma(t, tc.src, from, spec, w, h, len(got))
			mad := meanAbsDiff(t, got, ref)
			bias := mean(got[0]) - mean(ref[0])
			t.Logf("from %.3fs: mean luma %.3f, reference %.3f; mean abs difference %.3f/255",
				from, mean(got[0]), mean(ref[0]), mad)
			// Bit-exact today. The margin is for a reference ffmpeg of a
			// different version from the vendored one, whose scalers may
			// round differently; an undeclared colour space shows up as
			// a shift of a level or more.
			if bias > 0.25 || bias < -0.25 {
				t.Errorf("picture is shifted by %+.2f levels against reference ffmpeg", bias)
			}
			if mad > 0.5 {
				t.Errorf("picture differs from reference ffmpeg by %.2f/255 mean per pixel", mad)
			}
		})
	}
}

// Keeping frames on the device all the way to the encoder must give the
// picture the download path does. Compared before the encoder: the device
// output is downloaded here only to be read.
//
// The reference is the path this replaces for the same frames — scaled on
// the device, copied back, converted to NV12 on the CPU. A software decode
// through the CPU graph, which matches reference ffmpeg exactly, is logged
// alongside.
func TestDeviceEncodeMatchesDownload(t *testing.T) {
	hw := DetectHW("auto")
	if hw == nil {
		t.Skip("no hardware transcoder on this host")
	}
	defer hw.Close()
	if !hw.encodesFromDevice() {
		t.Skipf("%s cannot keep frames on the device here", hw.Kind)
	}
	for _, tc := range []struct {
		name   string
		src    string
		at     int
		w, h   int
		onDev  bool // whether the frames should stay on the device
		reason string
	}{
		{"8-bit 1080p to 720p", sdrSample, 20 * 60, 1280, 720, true, ""},
		// Same size: scale_vaapi would pass the decoder's own surface
		// through; out_range makes it copy into a surface of its own.
		{"8-bit 1080p at 1080p", sdrSample, 20 * 60, 1920, 1080, true, ""},
		{"10-bit 4K to 1080p", hdrSample, 65 * 60, 1920, 1080, false, "P010 surfaces keep the download path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := os.Stat(tc.src); err != nil {
				t.Skip("sample not present")
			}
			if !hwDecodeAvailable(tc.src, hw) {
				t.Skipf("%s cannot decode this file on the device", hw.Kind)
			}
			spec := videoFilterSpec("", tc.w, tc.h) + ",format=" + encoderPixFmtName(true)
			through := func(dev *HWContext, onDevice bool) ([][]byte, *videoGraph) {
				g := &videoGraph{spec: spec, outW: tc.w, outH: tc.h, scale: true, encodeOnDevice: onDevice, kind: dev.Kind}
				t.Cleanup(g.Close)
				planes, _ := lumaThrough(t, tc.src, tc.at, dev, g)
				return planes, g
			}
			ref, refG := through(hw, false)
			got, gotG := through(hw, true)
			if refG.deviceOut || !refG.onDevice {
				t.Fatalf("reference path: scaled on device %v, left there %v — want true, false", refG.onDevice, refG.deviceOut)
			}
			if gotG.deviceOut != tc.onDev {
				t.Fatalf("frames left on the device: %v, want %v (%s)", gotG.deviceOut, tc.onDev, tc.reason)
			}
			soft := &videoGraph{spec: spec}
			defer soft.Close()
			sw, _ := lumaThrough(t, tc.src, tc.at, nil, soft)

			mad := meanAbsDiff(t, ref, got)
			bias := mean(got[0]) - mean(ref[0])
			t.Logf("mean luma: software %.3f, download path %.3f, kept on device %.3f; "+
				"kept vs download %.3f/255, vs software %.3f/255",
				mean(sw[0]), mean(ref[0]), mean(got[0]), mad, meanAbsDiff(t, sw, got))
			// The same device scaler feeds both; the only difference is
			// where the NV12 frame is made. A range or matrix mistake
			// moves every pixel by several levels.
			if mad > 1.0 {
				t.Errorf("picture kept on the device differs by %.2f/255 mean per pixel", mad)
			}
			if bias > 0.5 || bias < -0.5 {
				t.Errorf("picture kept on the device is shifted by %+.2f levels", bias)
			}
		})
	}
}

// Keeping frames on the device must be measurably no worse than copying them
// back: at least as fast, less CPU, the same picture after the encoder, and
// the bitrate asked for. Runs the two paths alternately three times each and
// compares medians, so one busy moment on a desktop does not decide it.
//
// The bitrate check guards a separate fault this found: with no frame rate
// on the encoder, h264_vaapi's rate control took 1/time_base as the frame
// rate and wrote 0.32 Mbit/s against a 6 Mbit/s target — and on this
// sample, whose time base is 1/16000, it never returned from its first
// frame at all.
func TestDeviceEncodeCost(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	if _, err := os.Stat(sdrSample); err != nil {
		t.Skip("SDR sample not present")
	}
	hw := DetectHW("auto")
	if hw == nil {
		t.Skip("no hardware transcoder on this host")
	}
	defer hw.Close()
	if !hw.encodesFromDevice() || !hwDecodeAvailable(sdrSample, hw) {
		t.Skipf("%s cannot decode, scale and encode this file on the device here", hw.Kind)
	}
	copyBack := *hw
	copyBack.noDeviceEncode = true

	const segSecs, want, rounds = 6, 8, 3
	for _, height := range []int{720, 1080} {
		t.Run(fmt.Sprintf("1080p to %dp", height), func(t *testing.T) {
			var down, kept []hlsRun
			for range rounds {
				down = append(down, runHLSFrom(t, "copied back", sdrSample, 200, &copyBack, "", height, segSecs, want))
				kept = append(kept, runHLSFrom(t, "kept on device", sdrSample, 200, hw, "", height, segSecs, want))
			}
			for _, r := range append(slices.Clone(down), kept...) {
				if r.segments == 0 {
					t.Fatalf("%s produced no segments", r.label)
				}
				if r.accel != hw.Name() {
					t.Fatalf("%s encoded with %q, want %q", r.label, r.accel, hw.Name())
				}
				// Video at 6 Mbit/s plus 192 kbit/s of audio. Rate
				// control over a few segments wanders; a missing frame
				// rate is out by a factor of twenty.
				if m := r.mbps(segSecs); m < 3 || m > 12 {
					t.Errorf("%s wrote %.2f Mbit/s against a 6 Mbit/s target", r.label, m)
				}
			}
			dSpeed, kSpeed := medianOf(down, func(r hlsRun) float64 { return r.realtime(segSecs) }),
				medianOf(kept, func(r hlsRun) float64 { return r.realtime(segSecs) })
			dCPU, kCPU := medianOf(down, func(r hlsRun) float64 { return r.cpu.Seconds() }),
				medianOf(kept, func(r hlsRun) float64 { return r.cpu.Seconds() })
			t.Logf("median: copied back %.2fx realtime, %.3fs CPU; kept on device %.2fx realtime, %.3fs CPU (%+.0f%% CPU)",
				dSpeed, dCPU, kSpeed, kCPU, (kCPU/dCPU-1)*100)
			// 10% grace on wall-clock, as TestHardwareDecodeWithSoftwareEncode.
			if kSpeed < dSpeed*0.9 {
				t.Errorf("keeping frames on the device is slower (%.2fx vs %.2fx realtime)", kSpeed, dSpeed)
			}
			if kCPU >= dCPU {
				t.Errorf("keeping frames on the device saved no CPU (%.3fs vs %.3fs)", kCPU, dCPU)
			}

			a, b := segmentLuma(t, down[0].dir), segmentLuma(t, kept[0].dir)
			mad := meanAbsDiff(t, a, b)
			t.Logf("picture after the encoder: %d vs %d frames, mean abs luma difference %.3f/255", len(a), len(b), mad)
			// As TestHardwareDecodeWithSoftwareEncode: two encodes of
			// near-identical frames differ through rate control by a level
			// or two; a wrong format or range is tens.
			if mad > 3.0 {
				t.Errorf("picture kept on the device differs after encoding by %.2f/255", mad)
			}
		})
	}
}

// medianOf is the median of f over runs.
func medianOf(runs []hlsRun, f func(hlsRun) float64) float64 {
	v := make([]float64, len(runs))
	for i, r := range runs {
		v[i] = f(r)
	}
	slices.Sort(v)
	return v[len(v)/2]
}
