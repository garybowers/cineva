package media

import (
	"errors"
	"testing"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

func TestBurnInIsRefusedNotCrashed(t *testing.T) {
	// The decode path cannot work in this build, and must say so rather
	// than segfault when the decoder writes into an AVSubtitle that was
	// never allocated. See ErrBurnInUnsupported.
	b, err := newSubBurner(nil, 1920, 1080, 1280, 720)
	if b != nil {
		t.Error("a burner was returned despite the decoder being unavailable")
	}
	if !errors.Is(err, ErrBurnInUnsupported) {
		t.Errorf("err = %v, want ErrBurnInUnsupported", err)
	}
}

func TestRGBToYUV(t *testing.T) {
	tests := []struct {
		name      string
		r, g, b   byte
		wantY     byte
		tolerance int
	}{
		// Subtitle palettes are almost entirely white text with a black or
		// transparent outline, so these two matter most.
		{name: "white is near peak luma", r: 255, g: 255, b: 255, wantY: 235, tolerance: 2},
		{name: "black is the limited-range floor", r: 0, g: 0, b: 0, wantY: 16, tolerance: 1},
		{name: "mid grey", r: 128, g: 128, b: 128, wantY: 126, tolerance: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			y, u, v := rgbToYUV(tc.r, tc.g, tc.b)
			if diff := int(y) - int(tc.wantY); diff > tc.tolerance || diff < -tc.tolerance {
				t.Errorf("Y = %d, want %d±%d", y, tc.wantY, tc.tolerance)
			}
			// A neutral colour must not tint: both chroma channels sit at
			// the 128 midpoint.
			if tc.r == tc.g && tc.g == tc.b {
				if u < 127 || u > 129 || v < 127 || v > 129 {
					t.Errorf("neutral colour tinted: U=%d V=%d, want ~128", u, v)
				}
			}
		})
	}
}

func TestBlendRespectsAlpha(t *testing.T) {
	tests := []struct {
		name  string
		start byte
		value byte
		alpha byte
		want  byte
	}{
		{name: "opaque overwrites", start: 10, value: 200, alpha: 255, want: 200},
		{name: "transparent leaves it alone", start: 10, value: 200, alpha: 0, want: 10},
		{name: "half mixes evenly", start: 0, value: 200, alpha: 128, want: 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plane := []byte{tc.start}
			blend(plane, 0, tc.value, tc.alpha)
			if diff := int(plane[0]) - int(tc.want); diff > 1 || diff < -1 {
				t.Errorf("blended to %d, want %d±1", plane[0], tc.want)
			}
		})
	}
}

func TestBlendIgnoresOutOfRange(t *testing.T) {
	plane := []byte{1, 2, 3}
	// A subtitle rect positioned partly off the frame would otherwise write
	// past the plane and corrupt the next one.
	blend(plane, -1, 255, 255)
	blend(plane, 3, 255, 255)
	blend(plane, 99, 255, 255)
	if plane[0] != 1 || plane[1] != 2 || plane[2] != 3 {
		t.Errorf("out-of-range writes touched the plane: %v", plane)
	}
}

// Paints a synthetic rect onto a real frame and checks the pixels moved —
// the compositing is the part that will work unchanged once the decode path
// is unblocked, so it is worth testing on its own.
func TestPaintRectCompositesOntoAFrame(t *testing.T) {
	const w, h = 64, 32
	frame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&frame)
	frame.SetWidth(w)
	frame.SetHeight(h)
	frame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))
	if _, err := ffmpeg.AVFrameGetBuffer(frame, 32); err != nil {
		t.Fatalf("frame buffer: %v", err)
	}
	yStride := frame.Linesize().Get(0)
	luma := unsafe.Slice((*byte)(frame.Data().Get(0)), yStride*h)
	for i := range luma {
		luma[i] = 16 // black
	}

	b := &subBurner{srcW: w, srcH: h, outW: w, outH: h}
	// An 8x8 opaque white square at (4,4): palette entry 1 is opaque white,
	// 0 is fully transparent, which is how a real subtitle bitmap is built.
	palette := make([]uint32, 256)
	palette[1] = 0xFFFFFFFF
	const rw, rh = 8, 8
	pixels := make([]byte, rw*rh)
	for i := range pixels {
		pixels[i] = 1
	}
	b.compositeBitmap(frame, pixels, palette, rw, 4, 4, rw, rh)

	// Inside the square is now bright; outside is untouched.
	if got := luma[6*yStride+6]; got < 200 {
		t.Errorf("pixel inside the rect = %d, want bright (~235)", got)
	}
	if got := luma[20*yStride+40]; got != 16 {
		t.Errorf("pixel outside the rect = %d, want it untouched (16)", got)
	}
}
