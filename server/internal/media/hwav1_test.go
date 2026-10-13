package media

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

// writeAV1Clip encodes a short synthetic clip to AV1 with librav1e, so the
// AV1 decode paths can be exercised without shipping a sample file. The
// picture is a moving gradient: flat colour would decode identically from
// almost any bitstream and prove nothing. Skips when the build has no
// software AV1 encoder.
func writeAV1Clip(t *testing.T, path string, w, h, frames int) {
	t.Helper()
	name := ffmpeg.ToCStr("librav1e")
	codec := ffmpeg.AVCodecFindEncoderByName(name)
	name.Free()
	if codec == nil {
		t.Skip("no librav1e encoder in this build")
	}
	out, err := av.CreateOutput(path)
	if err != nil {
		t.Fatalf("create output: %v", err)
	}
	defer out.Close()
	enc, err := av.NewEncoder(codec, func(cc *ffmpeg.AVCodecContext) {
		cc.SetWidth(w)
		cc.SetHeight(h)
		cc.SetPixFmt(ffmpeg.AVPixFmtYuv420P)
		cc.SetTimeBase(ffmpeg.AVMakeQ(1, 24))
		cc.SetGopSize(12)
		cc.SetBitRate(2_000_000)
		cc.SetFlags(cc.Flags() | ffmpeg.AVCodecFlagGlobalHeader)
		setPrivOpt(cc, "speed", "10")
	})
	if err != nil {
		t.Fatalf("open librav1e: %v", err)
	}
	defer enc.Close()
	st, err := out.AddStream(enc)
	if err != nil {
		t.Fatalf("add stream: %v", err)
	}
	if err := out.WriteHeader(); err != nil {
		t.Fatalf("write header: %v", err)
	}
	write := func(pkt *ffmpeg.AVPacket) error {
		ffmpeg.AVPacketRescaleTs(pkt, enc.Raw().TimeBase(), st.TimeBase())
		pkt.SetStreamIndex(st.Index())
		return out.WritePacket(pkt)
	}

	frame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&frame)
	frame.SetWidth(w)
	frame.SetHeight(h)
	frame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))
	if _, err := ffmpeg.AVFrameGetBuffer(frame, 0); err != nil {
		t.Fatalf("frame buffer: %v", err)
	}
	for i := 0; i < frames; i++ {
		if _, err := ffmpeg.AVFrameMakeWritable(frame); err != nil {
			t.Fatalf("make writable: %v", err)
		}
		for p := 0; p < 3; p++ {
			pw, ph := w, h
			if p > 0 {
				pw, ph = w/2, h/2
			}
			stride := frame.Linesize().Get(uintptr(p))
			base := frame.Data().Get(uintptr(p))
			for y := 0; y < ph; y++ {
				row := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(base)+uintptr(y*stride))), pw)
				for x := range row {
					row[x] = byte((x + 2*y + 4*i + 60*p) & 0xff)
				}
			}
		}
		frame.SetPts(int64(i))
		if err := enc.Encode(frame, write); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	if err := enc.Flush(write); err != nil {
		t.Fatalf("flush encoder: %v", err)
	}
	if err := out.WriteTrailer(); err != nil {
		t.Fatalf("write trailer: %v", err)
	}
}

// decodeLuma runs every packet of a file's video through a decoder and
// returns each frame's luma plane, downloading device surfaces first.
func decodeLuma(t *testing.T, path string, open func(*ffmpeg.AVStream) (*threadedDecoder, error)) ([][]byte, *threadedDecoder) {
	t.Helper()
	in, err := av.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { in.Close() })
	vs, err := in.BestStream(ffmpeg.AVMediaTypeVideo)
	if err != nil {
		t.Fatalf("video stream: %v", err)
	}
	dec, err := open(vs)
	if err != nil {
		t.Fatalf("open decoder: %v", err)
	}
	t.Cleanup(func() { _ = dec.Close() })

	var sw *ffmpeg.AVFrame
	defer func() {
		if sw != nil {
			ffmpeg.AVFrameFree(&sw)
		}
	}()
	var planes [][]byte
	collect := func(f *ffmpeg.AVFrame) error {
		if f.HwFramesCtx() != nil {
			var err error
			if f, err = downloadFrame(&sw, f); err != nil {
				return err
			}
		}
		w, h := f.Width(), f.Height()
		stride := f.Linesize().Get(0)
		base := f.Data().Get(0)
		plane := make([]byte, 0, w*h)
		for y := 0; y < h; y++ {
			plane = append(plane, unsafe.Slice((*byte)(unsafe.Pointer(uintptr(base)+uintptr(y*stride))), w)...)
		}
		planes = append(planes, plane)
		return nil
	}
	pkt := ffmpeg.AVPacketAlloc()
	defer ffmpeg.AVPacketFree(&pkt)
	for {
		err := in.ReadPacket(pkt)
		if errors.Is(err, ffmpeg.AVErrorEOF) {
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if pkt.StreamIndex() == vs.Index() {
			if err := dec.Decode(pkt, collect); err != nil {
				t.Fatalf("decode: %v", err)
			}
		}
		ffmpeg.AVPacketUnref(pkt)
	}
	if err := dec.Flush(collect); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return planes, dec
}

// The point of picking decoders by capability: AV1's default decoder is
// libdav1d, which has no hardware configurations, so asking only the default
// left AV1 on the CPU even where the device and the build both support it.
func TestAV1PicksTheDecoderThatCarriesTheHWAccel(t *testing.T) {
	hw := DetectHW("vaapi")
	if hw == nil {
		t.Skip("no VAAPI device on this host")
	}
	defer hw.Close()

	def := ffmpeg.AVCodecFindDecoder(ffmpeg.AVCodecIdAV1)
	if def == nil {
		t.Skip("no AV1 decoder in this build")
	}
	if _, ok := hwDecodePixFmt(def, hw); ok {
		t.Logf("default AV1 decoder %s carries a VAAPI config itself", def.Name().String())
	}
	codec, surface, ok := hwaccelDecoder(ffmpeg.AVCodecIdAV1, hw)
	if !ok {
		t.Skip("this build has no AV1 hwaccel for VAAPI")
	}
	t.Logf("default decoder %s, hardware decoder %s (surface %d)", def.Name().String(), codec.Name().String(), surface)
	if surface != hw.PixFmt {
		t.Errorf("surface = %d, want the device's %d", surface, hw.PixFmt)
	}
	// h264 must keep its default decoder: the native one carries the
	// hwaccel and is what has always been used.
	h264, _, ok := hwaccelDecoder(ffmpeg.AVCodecIdH264, hw)
	if ok && h264.Name().String() != "h264" {
		t.Errorf("h264 hardware decoder = %s, want the default h264", h264.Name().String())
	}
}

// Decodes a synthetic AV1 clip on the GPU and compares it with libdav1d.
// AV1 decoding is exact, so the two must agree pixel for pixel; a device
// that cannot decode AV1 must still produce the same frames, through the
// software fallback.
func TestAV1HardwareDecodeMatchesSoftware(t *testing.T) {
	hw := DetectHW("vaapi")
	if hw == nil {
		t.Skip("no VAAPI device on this host")
	}
	defer hw.Close()
	if _, _, ok := hwaccelDecoder(ffmpeg.AVCodecIdAV1, hw); !ok {
		t.Skip("this build has no AV1 hwaccel for VAAPI")
	}
	clip := filepath.Join(t.TempDir(), "av1.mkv")
	const frames = 48
	writeAV1Clip(t, clip, 640, 360, frames)

	soft, _ := decodeLuma(t, clip, newThreadedDecoder)
	hard, dec := decodeLuma(t, clip, func(st *ffmpeg.AVStream) (*threadedDecoder, error) { return newDecoder(st, hw) })
	onGPU := dec.HardwarePixFmt() != ffmpeg.AVPixFmtNone
	t.Logf("software %d frames, hardware path %d frames, decoded on the GPU: %v (%s)",
		len(soft), len(hard), onGPU, dec.Raw().Codec().Name().String())
	if len(soft) != frames || len(hard) != frames {
		t.Fatalf("frame counts: software %d, hardware path %d, want %d", len(soft), len(hard), frames)
	}
	if mad := meanAbsDiff(t, soft, hard); mad != 0 {
		t.Errorf("hardware decode differs from libdav1d by %.4f/255 mean per pixel", mad)
	}
	if !onGPU {
		t.Log("device did not decode AV1; the software fallback produced the frames")
	}
}

// A hardware decoder that fails before its first frame must hand over to
// software without losing a frame. Forced here with the native av1 decoder
// and no device: it has no software path at all and fails on the first
// sequence header, exactly as it does on a device without AV1 decode. Needs
// no GPU, so this runs everywhere the build has librav1e.
func TestDecoderFallsBackWhenHardwareFailsBeforeFirstFrame(t *testing.T) {
	name := ffmpeg.ToCStr("av1")
	native := ffmpeg.AVCodecFindDecoderByName(name)
	name.Free()
	if native == nil {
		t.Skip("no native av1 decoder in this build")
	}
	clip := filepath.Join(t.TempDir(), "av1.mkv")
	const frames = 24
	writeAV1Clip(t, clip, 320, 180, frames)

	soft, _ := decodeLuma(t, clip, newThreadedDecoder)
	got, dec := decodeLuma(t, clip, func(st *ffmpeg.AVStream) (*threadedDecoder, error) {
		ctx := ffmpeg.AVCodecAllocContext3(native)
		if ctx == nil {
			return nil, errors.New("alloc context")
		}
		d := &threadedDecoder{ctx: ctx, stream: st, hwPixFmt: ffmpeg.AVPixFmtVaapi, probation: true}
		if _, err := ffmpeg.AVCodecParametersToContext(ctx, st.Codecpar()); err != nil {
			_ = d.Close()
			return nil, err
		}
		if _, err := ffmpeg.AVCodecOpen2(ctx, native, nil); err != nil {
			_ = d.Close()
			return nil, err
		}
		d.frame = ffmpeg.AVFrameAlloc()
		return d, nil
	})
	if dec.probation || len(dec.pending) != 0 {
		t.Errorf("still on probation after decoding (%d packets kept)", len(dec.pending))
	}
	if dec.HardwarePixFmt() != ffmpeg.AVPixFmtNone {
		t.Errorf("reports a hardware surface after falling back to %s", dec.Raw().Codec().Name().String())
	}
	if len(got) != frames {
		t.Fatalf("fallback produced %d frames, want %d", len(got), frames)
	}
	if mad := meanAbsDiff(t, soft, got); mad != 0 {
		t.Errorf("fallback frames differ from a plain software decode by %.4f/255", mad)
	}
	t.Logf("fell back to %s and decoded all %d frames", dec.Raw().Codec().Name().String(), len(got))
}

// What the chip reports it can decode decides whether the hwaccel route is
// tried at all, so a part without an AV1 block decodes AV1 in software from
// the start rather than failing its first frame on the device.
func TestChipDecodesGatesTheHWAccelRoute(t *testing.T) {
	tests := []struct {
		name    string
		decodes map[ffmpeg.AVCodecID]bool
		id      ffmpeg.AVCodecID
		want    bool
	}{
		{"unknown driver: trust the build", nil, ffmpeg.AVCodecIdAV1, true},
		{"chip decodes it", map[ffmpeg.AVCodecID]bool{ffmpeg.AVCodecIdAV1: true}, ffmpeg.AVCodecIdAV1, true},
		{"chip lacks it", map[ffmpeg.AVCodecID]bool{ffmpeg.AVCodecIdAV1: false}, ffmpeg.AVCodecIdAV1, false},
		{"codec not asked about", map[ffmpeg.AVCodecID]bool{ffmpeg.AVCodecIdAV1: false}, ffmpeg.AVCodecIdVc1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hw := &HWContext{Kind: HWVAAPI, DevType: ffmpeg.AVHWDeviceTypeVaapi, PixFmt: ffmpeg.AVPixFmtVaapi, decodes: tc.decodes}
			if got := hw.chipDecodes(tc.id); got != tc.want {
				t.Errorf("chipDecodes = %v, want %v", got, tc.want)
			}
			if !tc.want {
				if _, _, ok := hwaccelDecoder(tc.id, hw); ok {
					t.Error("hwaccelDecoder offered a decoder for a codec the chip lacks")
				}
			}
		})
	}
	if got := cpuDecodedCodecs([]string{"h264", "hevc", "vp9", "mpeg2"}); !slices.Equal(got, []string{"av1"}) {
		t.Errorf("cpuDecodedCodecs = %v, want [av1]", got)
	}
}

// The device on this host, asked directly: which codecs its driver lists
// with a decode entrypoint. Logged rather than asserted beyond h264, which
// every VAAPI decoder has; the point is that the answer comes from the chip.
func TestVAAPIDecodeProfilesFromDriver(t *testing.T) {
	hw := DetectHW("vaapi")
	if hw == nil {
		t.Skip("no VAAPI device on this host")
	}
	defer hw.Close()
	if hw.decodes == nil {
		t.Fatal("the driver could not be asked for its decode profiles")
	}
	var have, lack []string
	for _, c := range commonVideoCodecs {
		if hw.decodes[c.id] {
			have = append(have, c.name)
		} else {
			lack = append(lack, c.name)
		}
	}
	t.Logf("driver decodes %v, not %v; GPU decode list %v", have, lack, hwDecodableCodecs(hw))
	if !hw.decodes[ffmpeg.AVCodecIdH264] {
		t.Error("driver reports no H.264 decode")
	}
}
