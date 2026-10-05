package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

// Real transcode of an HDR source through the tone-mapping chain. Skipped
// when the sample is absent so the suite still runs on a bare checkout.
// hdrSample is a local 4K HDR file used by the tone-mapping tests. These
// tests need real HDR footage — a synthetic clip would not exercise the PQ
// curve — so they skip rather than fail where it is absent.
const hdrSample = "/home/gary/Videos/cineva/Movies/Project Hail Mary (2026)/Project Hail Mary 2026 WEBDL-2160p.mkv"

func TestHDRToneMapTranscode(t *testing.T) {
	src := hdrSample
	if _, err := os.Stat(src); err != nil {
		t.Skip("HDR sample not present")
	}
	info, err := Probe(src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	t.Logf("source: %dx%d %s hdr=%q dv=%d/%d", info.Width, info.Height, info.VideoCodec, info.HDRFormat, info.DVProfile, info.DVBLCompat)
	if info.HDRFormat == "" {
		t.Skip("sample is not HDR")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	job := HLSJob{
		InputPath: src, OutDir: dir, SegmentSecs: 4, StartSegment: 0,
		VideoHeight: 720, VideoBitrateKbps: 3000,
		HDR:        info.HDRFormat,
		AudioTrack: -1,
		// Stop after a few segments; we only need to prove the graph builds
		// and produces frames.
		Throttle: func(ctx context.Context, pos int64) error {
			if pos > 12 {
				cancel()
			}
			return nil
		},
	}
	err = RunHLS(ctx, job)
	segs, _ := filepath.Glob(filepath.Join(dir, "*.m4s"))
	t.Logf("RunHLS err=%v segments=%d", err, len(segs))
	if len(segs) == 0 {
		t.Fatalf("tone-mapping chain produced no segments (err=%v)", err)
	}
	var total int64
	for _, s := range segs {
		st, _ := os.Stat(s)
		total += st.Size()
	}
	t.Logf("wrote %d segments, %d bytes", len(segs), total)
	if total == 0 {
		t.Fatal("segments are empty")
	}
}

// meanLuma decodes the first few frames of an HDR source through the given
// filter spec and returns the average Y value. Tone mapping has to change
// this: PQ pixels shown as SDR sit far too dark, so a correctly mapped
// picture is measurably brighter.
func lumaPlanes(t *testing.T, src, hdr string) [][]byte {
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
	// Films open on black, and black maps to black whatever the transfer
	// curve, so sample well past the fade-in or the comparison is vacuous.
	const sampleAtSecs = 65 * 60
	if _, err := ffmpeg.AVSeekFrame(in.Raw(), -1, int64(sampleAtSecs)*int64(ffmpeg.AVTimeBase), ffmpeg.AVSeekFlagBackward); err != nil {
		t.Fatalf("seek: %v", err)
	}
	dec, err := newThreadedDecoder(vs)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	defer dec.Close()
	g, err := av.NewVideoFilterGraphFromContext(dec.Raw(), videoFilterSpec(hdr, 640, 360), ffmpeg.AVPixFmtYuv420P)
	if err != nil {
		t.Fatalf("graph (%s): %v", hdr, err)
	}
	defer g.Close()

	var planes [][]byte
	const want = 8
	framesSeen := 0
	pkt := ffmpeg.AVPacketAlloc()
	defer ffmpeg.AVPacketFree(&pkt)
	for framesSeen < want {
		rerr := in.ReadPacket(pkt)
		if rerr != nil {
			break
		}
		if pkt.StreamIndex() != vs.Index() {
			continue
		}
		derr := dec.Decode(pkt, func(f *ffmpeg.AVFrame) error {
			f.SetPts(f.BestEffortTimestamp())
			if err := g.Push(f); err != nil {
				return err
			}
			return g.Pull(func(out *ffmpeg.AVFrame) error {
				w, h := out.Width(), out.Height()
				stride := out.Linesize().Get(0)
				base := out.Data().Get(0)
				plane := make([]byte, 0, w*h)
				for y := 0; y < h; y++ {
					row := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(base)+uintptr(y*stride))), w)
					plane = append(plane, row...)
				}
				planes = append(planes, plane)
				framesSeen++
				return nil
			})
		})
		if derr != nil {
			t.Fatalf("decode (%s): %v", hdr, derr)
		}
	}
	if len(planes) == 0 {
		t.Fatalf("no frames decoded for hdr=%q", hdr)
	}
	return planes
}

// meanAbsDiff is the mean absolute per-pixel difference between two runs.
// This is the direct question — is the tone map altering the picture — where
// mean luma is not: tone mapping lifts midtones while compressing
// highlights, so the two can cancel out in the average.
func meanAbsDiff(t *testing.T, x, y [][]byte) float64 {
	t.Helper()
	n := min(len(x), len(y))
	var total float64
	for i := 0; i < n; i++ {
		a, b := x[i], y[i]
		if len(a) != len(b) {
			t.Fatalf("frame %d size mismatch: %d vs %d", i, len(a), len(b))
		}
		var mad float64
		for j := range a {
			d := float64(a[j]) - float64(b[j])
			if d < 0 {
				d = -d
			}
			mad += d
		}
		total += mad / float64(len(a))
	}
	return total / float64(n)
}

func mean(p []byte) float64 {
	var s float64
	for _, v := range p {
		s += float64(v)
	}
	return s / float64(len(p))
}

func TestToneMapChangesPicture(t *testing.T) {
	src := hdrSample
	if _, err := os.Stat(src); err != nil {
		t.Skip("HDR sample not present")
	}
	info, err := Probe(src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if info.HDRFormat == "" {
		t.Skip("sample is not HDR")
	}
	raw := lumaPlanes(t, src, "")
	control := lumaPlanes(t, src, "") // same spec twice: the no-op baseline
	mapped := lumaPlanes(t, src, info.HDRFormat)
	if got := meanAbsDiff(t, raw, control); got != 0 {
		t.Fatalf("control run differs from itself by %.3f — the measurement is not deterministic", got)
	}
	avgMAD := meanAbsDiff(t, raw, mapped)
	t.Logf("hdr=%s  mean luma %.1f -> %.1f  mean abs per-pixel difference %.2f/255",
		info.HDRFormat, mean(raw[0]), mean(mapped[0]), avgMAD)
	// The control above pins a no-op at exactly 0. Cross-checked against
	// reference ffmpeg running the same chain on this file, which scores
	// 28-30 dB PSNR between the two pipelines (RMSE ~10/255); a mean
	// absolute difference around 5 is what that implies, so 2.0 separates
	// "working" from "silently doing nothing" with room to spare.
	if avgMAD < 2 {
		t.Fatalf("tone mapping barely changed the picture (mean abs diff %.2f/255) — the chain is not doing its job", avgMAD)
	}
}
