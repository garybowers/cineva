package media

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

// decodeOnly is a view of a device that can decode but not encode: the same
// device, with no encoder. NewFrames refuses it, so RunHLS encodes with
// libx264 while the decoder still uses the device — the case a GPU with
// decode blocks and no usable encoder presents.
func decodeOnly(hw *HWContext) *HWContext {
	return &HWContext{
		Kind: hw.Kind, Device: hw.Device, PixFmt: hw.PixFmt,
		DevType: hw.DevType, ref: hw.ref, decodes: hw.decodes,
		noDeviceScale: hw.noDeviceScale,
	}
}

// scaledOnCPU is the same device with scaling kept on the CPU after the
// download: the hardware-decode path as it was before device scaling.
func scaledOnCPU(hw *HWContext) *HWContext {
	c := *hw
	c.noDeviceScale = true
	return &c
}

type hlsRun struct {
	label    string
	cpu      time.Duration
	wall     time.Duration
	segments int
	dir      string
	accel    string
}

// realtime is seconds of video produced per second of wall clock.
func (r hlsRun) realtime(segSecs int) float64 {
	return float64(r.segments*segSecs) / r.wall.Seconds()
}

// runHLSSlice transcodes about want segments from 20 minutes in and reports
// what it cost.
func runHLSSlice(t *testing.T, label string, hw *HWContext, hdr string, segSecs, want int) hlsRun {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const start = 200
	r := hlsRun{label: label, dir: dir}
	before := processCPU()
	began := time.Now()
	job := HLSJob{
		InputPath: hdrSample, OutDir: dir, SegmentSecs: segSecs,
		StartSegment: start, VideoHeight: 1080, VideoBitrateKbps: 6000,
		AudioTrack: -1, HW: hw, HDR: hdr,
		OnAccel: func(name string) { r.accel = name },
		Throttle: func(_ context.Context, pos int64) error {
			if pos > int64(start*segSecs+want*segSecs) {
				cancel()
			}
			return nil
		},
	}
	if err := RunHLS(ctx, job); err != nil && ctx.Err() == nil {
		t.Fatalf("%s: RunHLS: %v", label, err)
	}
	r.wall = time.Since(began)
	r.cpu = processCPU() - before
	files, _ := filepath.Glob(filepath.Join(dir, "seg*.m4s"))
	r.segments = len(files)
	t.Logf("%-28s encoder %-6s cpu %-9v wall %-8v %d segments = %.2fx realtime",
		label, r.accel, r.cpu.Round(time.Millisecond), r.wall.Round(time.Millisecond),
		r.segments, r.realtime(segSecs))
	return r
}

// segmentLuma decodes the first produced segment of a run back to luma
// planes, by joining it to its init segment.
func segmentLuma(t *testing.T, dir string) [][]byte {
	t.Helper()
	segs, _ := filepath.Glob(filepath.Join(dir, "seg*.m4s"))
	if len(segs) == 0 {
		t.Fatalf("no segments in %s", dir)
	}
	sort.Strings(segs)
	initSeg, err := os.ReadFile(filepath.Join(dir, "init.mp4"))
	if err != nil {
		t.Fatalf("init segment: %v", err)
	}
	body, err := os.ReadFile(segs[0])
	if err != nil {
		t.Fatalf("segment: %v", err)
	}
	joined := filepath.Join(dir, "joined.mp4")
	if err := os.WriteFile(joined, append(initSeg, body...), 0o600); err != nil {
		t.Fatalf("join: %v", err)
	}
	planes, _ := decodeLuma(t, joined, newThreadedDecoder)
	return planes
}

// A device that decodes but cannot encode now decodes on the GPU and
// encodes with libx264, instead of decoding on the CPU as well. This
// measures both against the all-CPU path it replaces, and checks the picture
// is the same: HEVC decoding is exact, so a downloaded surface must scale
// and encode to what a software decode does.
//
// It is a timing test, meaningful only on an otherwise idle machine. The
// guard is the one TestTranscodeKeepsUpWithRealtime uses — a playback that
// cannot sustain 1.2x realtime stalls — plus a check that the GPU path is
// not slower than the CPU path it replaces, so "never worse" is measured
// rather than assumed.
func TestHardwareDecodeWithSoftwareEncode(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
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

	const segSecs, want = 6, 4
	for _, tc := range []struct {
		name, hdr string
	}{
		{"4K SDR treatment -> 1080p", ""},
		{"4K HDR tone mapped -> 1080p", "pq"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Not measured here, but the reason the gate existed: with
			// every surface downloaded at 4K, GPU decode into libx264 was
			// slower than the all-CPU path (0.68x against 1.05x realtime
			// on SDR, measured side by side under load). Scaling on the
			// device is what makes lifting the gate safe.
			cpu := runHLSSlice(t, "cpu decode, libx264", nil, tc.hdr, segSecs, want)
			gpu := runHLSSlice(t, "gpu decode+scale, libx264", decodeOnly(hw), tc.hdr, segSecs, want)
			if cpu.segments == 0 || gpu.segments == 0 {
				t.Fatalf("a run produced no segments (cpu %d, gpu %d)", cpu.segments, gpu.segments)
			}
			if gpu.accel != string(HWNone) {
				t.Errorf("decode-only device reported encoder %q, want %q", gpu.accel, HWNone)
			}
			t.Logf("gpu decode: CPU %+.0f%%, speed %.2fx -> %.2fx realtime",
				(float64(gpu.cpu)/float64(cpu.cpu)-1)*100, cpu.realtime(segSecs), gpu.realtime(segSecs))
			// The absolute floor (1.2x) is TestTranscodeKeepsUpWithRealtime's
			// to enforce; a decode-only device is a different machine with a
			// different CPU. What this asserts is the principle: the GPU
			// path is never worse than the CPU path it replaces.
			//
			// 10% grace: wall-clock on a desktop wobbles by about that
			// between identical runs.
			if gpu.realtime(segSecs) < cpu.realtime(segSecs)*0.9 {
				t.Errorf("gpu decode is slower than the CPU path it replaces (%.2fx vs %.2fx)",
					gpu.realtime(segSecs), cpu.realtime(segSecs))
			}
			if gpu.cpu >= cpu.cpu {
				t.Errorf("gpu decode saved no CPU (%v vs %v)", gpu.cpu, cpu.cpu)
			}

			a, b := segmentLuma(t, cpu.dir), segmentLuma(t, gpu.dir)
			mad := meanAbsDiff(t, a, b)
			t.Logf("picture: %d vs %d frames, mean abs luma difference %.3f/255", len(a), len(b), mad)
			// Two libx264 encodes of near-identical frames differ through
			// rate control: even the old full-size download, whose pixels
			// match a software decode exactly before the encoder, scored
			// 1.9 here. A wrong pixel format or a lost colour property shows
			// up as tens of levels. TestDeviceScaleMatchesCPU checks the
			// picture before the encoder, where the bar can be tight.
			if mad > 3.0 {
				t.Errorf("gpu-decoded picture differs from the software one by %.2f/255", mad)
			}
		})
	}
}

// Scaling on the device must produce the picture the CPU chain does. Checked
// before the encoder, so the comparison is of the pipelines alone.
//
// The reference is the hardware decode as it shipped before device scaling:
// the same surfaces, downloaded at full size and scaled by swscale. That
// isolates the one thing that changed. A software decode is compared as well
// and logged; with tone mapping it agrees as closely, but untone-mapped a
// 10-bit PQ source sits 1.7-1.9 levels darker through the software graph
// than through either hardware path or reference ffmpeg. The likely cause is
// that newParsedGraph declares the source colour space and range on its
// buffer source and the av helper's does not; not confirmed, it predates
// this, and it is not what is being tested here.
func TestDeviceScaleMatchesCPU(t *testing.T) {
	if _, err := os.Stat(hdrSample); err != nil {
		t.Skip("4K sample not present")
	}
	hw := DetectHW("auto")
	if hw == nil {
		t.Skip("no hardware transcoder on this host")
	}
	defer hw.Close()
	if !hw.scalesOnDevice() || !hwDecodeAvailable(hdrSample, hw) {
		t.Skipf("%s cannot decode and scale this file on the device here", hw.Kind)
	}
	for _, hdr := range []string{"", "pq"} {
		t.Run("hdr="+hdr, func(t *testing.T) {
			const w, h = 1280, 720
			ref, refOnDevice := lumaPlanesGPU(t, hdrSample, scaledOnCPU(hw), hdr, w, h)
			got, onDevice := lumaPlanesGPU(t, hdrSample, hw, hdr, w, h)
			if refOnDevice || !onDevice {
				t.Fatalf("scaled on device: reference %v, under test %v — want false, true", refOnDevice, onDevice)
			}
			soft := lumaPlanesSpec(t, hdrSample, videoFilterSpec(hdr, w, h))
			mad := meanAbsDiff(t, ref, got)
			bias := mean(got[0]) - mean(ref[0])
			t.Logf("mean luma: software %.2f, gpu + cpu scale %.2f, gpu scale %.2f; "+
				"gpu scale vs cpu scale %.3f/255, vs software %.3f/255",
				mean(soft[0]), mean(ref[0]), mean(got[0]), mad, meanAbsDiff(t, soft, got))
			// A range or transfer mistake moves every pixel by several
			// levels; two scaling kernels differ by a fraction of one.
			if mad > 1.0 {
				t.Errorf("device-scaled picture differs by %.2f/255 mean per pixel", mad)
			}
			if bias > 0.5 || bias < -0.5 {
				t.Errorf("device-scaled picture is shifted by %+.2f levels", bias)
			}
		})
	}

	// Most of a library is 8-bit H.264, whose surfaces are NV12 rather
	// than P010. There is no such sample on disk, so one is made from the
	// 4K file with libx264, a few seconds from 65 minutes in.
	t.Run("8-bit h264", func(t *testing.T) {
		const segSecs = 2
		dir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		start := 65 * 60 / segSecs
		job := HLSJob{
			InputPath: hdrSample, OutDir: dir, SegmentSecs: segSecs, StartSegment: start,
			VideoHeight: 1080, VideoBitrateKbps: 8000, AudioTrack: -1,
			Throttle: func(_ context.Context, pos int64) error {
				if pos > int64((start+5)*segSecs) {
					cancel()
				}
				return nil
			},
		}
		if err := RunHLS(ctx, job); err != nil && ctx.Err() == nil {
			t.Fatalf("make h264 clip: %v", err)
		}
		segmentLuma(t, dir) // joins init and first segment into joined.mp4
		clip := filepath.Join(dir, "joined.mp4")
		const w, h = 1280, 720
		ref, _ := lumaPlanesGPUAt(t, clip, 0, scaledOnCPU(hw), "", w, h)
		got, onDevice := lumaPlanesGPUAt(t, clip, 0, hw, "", w, h)
		if !onDevice {
			t.Fatal("the device scaler was not used")
		}
		mad := meanAbsDiff(t, ref, got)
		bias := mean(got[0]) - mean(ref[0])
		t.Logf("mean luma: gpu + cpu scale %.2f, gpu scale %.2f; difference %.3f/255", mean(ref[0]), mean(got[0]), mad)
		if mad > 1.0 {
			t.Errorf("device-scaled picture differs by %.2f/255 mean per pixel", mad)
		}
		if bias > 0.5 || bias < -0.5 {
			t.Errorf("device-scaled picture is shifted by %+.2f levels", bias)
		}
	})
}

// lumaPlanesGPU is lumaPlanesSpec through the hardware path: decoded on the
// device and run through gpuFrames, as RunHLS does.
func lumaPlanesGPU(t *testing.T, src string, hw *HWContext, hdr string, w, h int) ([][]byte, bool) {
	return lumaPlanesGPUAt(t, src, 65*60, hw, hdr, w, h) // as lumaPlanesSpec: well past the fade-in
}

func lumaPlanesGPUAt(t *testing.T, src string, at int, hw *HWContext, hdr string, w, h int) ([][]byte, bool) {
	t.Helper()
	in, err := av.Open(src)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer in.Close()
	vs, err := in.BestStream(ffmpeg.AVMediaTypeVideo)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if at > 0 {
		if _, err := ffmpeg.AVSeekFrame(in.Raw(), -1, int64(at)*int64(ffmpeg.AVTimeBase), ffmpeg.AVSeekFlagBackward); err != nil {
			t.Fatalf("seek: %v", err)
		}
	}
	dec, err := newDecoder(vs, hw)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	defer dec.Close()
	if dec.HardwarePixFmt() == ffmpeg.AVPixFmtNone {
		t.Fatal("decoder did not open on the device")
	}
	g := &gpuFrames{
		spec: videoFilterSpec(hdr, w, h) + ",format=yuv420p",
		outW: w, outH: h, timeBase: vs.TimeBase(), scale: hw.scalesOnDevice(), kind: hw.Kind,
	}
	defer g.Close()

	var planes [][]byte
	const want = 8
	pkt := ffmpeg.AVPacketAlloc()
	defer ffmpeg.AVPacketFree(&pkt)
	for len(planes) < want {
		if err := in.ReadPacket(pkt); err != nil {
			break
		}
		if pkt.StreamIndex() != vs.Index() {
			ffmpeg.AVPacketUnref(pkt)
			continue
		}
		err := dec.Decode(pkt, func(f *ffmpeg.AVFrame) error {
			f.SetPts(f.BestEffortTimestamp())
			return g.Push(f, func(out *ffmpeg.AVFrame) error {
				if len(planes) >= want {
					return nil
				}
				stride := out.Linesize().Get(0)
				base := out.Data().Get(0)
				plane := make([]byte, 0, out.Width()*out.Height())
				for y := 0; y < out.Height(); y++ {
					plane = append(plane, unsafe.Slice((*byte)(unsafe.Pointer(uintptr(base)+uintptr(y*stride))), out.Width())...)
				}
				planes = append(planes, plane)
				return nil
			})
		})
		ffmpeg.AVPacketUnref(pkt)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	if len(planes) == 0 {
		t.Fatal("no frames through the device path")
	}
	return planes, g.onDevice
}
