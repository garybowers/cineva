package avatar

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestProcess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   []byte
		wantErr error
	}{
		{"landscape", encodePNG(t, 600, 200), nil},
		{"portrait", encodePNG(t, 120, 900), nil},
		{"tiny", encodePNG(t, 8, 8), nil},
		{"not an image", []byte("hello, world"), ErrNotImage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Process(bytes.NewReader(tc.input))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			img, err := jpeg.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("output is not a JPEG: %v", err)
			}
			if b := img.Bounds(); b.Dx() != Size || b.Dy() != Size {
				t.Fatalf("avatar is %dx%d, want %dx%d", b.Dx(), b.Dy(), Size, Size)
			}
		})
	}
	if _, err := Process(strings.NewReader(strings.Repeat("x", MaxBytes+1))); err == nil {
		t.Fatal("accepted an upload over the size limit")
	}
}

// exifJPEG is the start of a JPEG carrying only an EXIF orientation, in
// the byte order given — enough for orientation to read.
func exifJPEG(bigEndian bool, o byte) []byte {
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 0x01, 3, 0, 1, 0, 0, 0, o, 0, 0, 0, 0, 0, 0, 0}
	if bigEndian {
		tiff = []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, o, 0, 0, 0, 0, 0, 0}
	}
	seg := append([]byte("Exif\x00\x00"), tiff...)
	n := len(seg) + 2
	return append(append([]byte{0xFF, 0xD8, 0xFF, 0xE1, byte(n >> 8), byte(n)}, seg...), 0xFF, 0xDA, 0, 2)
}

func TestOrientation(t *testing.T) {
	for _, tc := range []struct {
		name string
		jpg  []byte
		want int
	}{
		{"little-endian 6", exifJPEG(false, 6), 6},
		{"big-endian 8", exifJPEG(true, 8), 8},
		{"out of range", exifJPEG(false, 9), 1},
		{"no EXIF", []byte{0xFF, 0xD8, 0xFF, 0xDA, 0, 2}, 1},
		{"not a JPEG", []byte("\x89PNG"), 1},
		{"truncated", exifJPEG(false, 6)[:12], 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := orientation(tc.jpg); got != tc.want {
				t.Fatalf("orientation = %d, want %d", got, tc.want)
			}
		})
	}
}

// A row [red, blue] shot with the camera turned (orientation 6) is shown
// as a column, red on top.
func TestUpright(t *testing.T) {
	red, blue := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, red)
	src.Set(1, 0, blue)
	for _, tc := range []struct {
		o          int
		w, h       int
		top, other image.Point
	}{
		{6, 1, 2, image.Pt(0, 0), image.Pt(0, 1)},
		{8, 1, 2, image.Pt(0, 1), image.Pt(0, 0)},
		{3, 2, 1, image.Pt(1, 0), image.Pt(0, 0)},
		{1, 2, 1, image.Pt(0, 0), image.Pt(1, 0)},
	} {
		got := upright(src, tc.o)
		if b := got.Bounds(); b.Dx() != tc.w || b.Dy() != tc.h {
			t.Fatalf("orientation %d: size %dx%d, want %dx%d", tc.o, b.Dx(), b.Dy(), tc.w, tc.h)
		}
		if got.At(tc.top.X, tc.top.Y) != red || got.At(tc.other.X, tc.other.Y) != blue {
			t.Fatalf("orientation %d: red/blue not where expected", tc.o)
		}
	}
}
