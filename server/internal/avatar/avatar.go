// Package avatar turns an uploaded picture into a user's avatar: a small
// square JPEG, upright, whatever shape and orientation the original was.
package avatar

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // decoders, registered for image.Decode
	"image/jpeg"
	_ "image/png"
	"io"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	// Size is the avatar's width and height in pixels: sharp at the largest
	// it is drawn (the profile picker's tiles) on a high-density screen.
	Size = 256
	// MaxBytes bounds an upload; a phone photo is a few megabytes.
	MaxBytes = 15 << 20
	// maxPixels refuses images whose decoded size would be out of all
	// proportion to their file — a few kilobytes can claim gigapixels.
	maxPixels = 50_000_000
)

// ErrNotImage is returned for anything that does not decode as a picture.
var ErrNotImage = errors.New("not a JPEG, PNG, GIF or WebP image")

// Process reads an uploaded image and returns the avatar as JPEG bytes:
// turned upright by its EXIF orientation, cropped to the centre square and
// scaled to Size.
func Process(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	if len(raw) > MaxBytes {
		return nil, fmt.Errorf("image is larger than %d MB", MaxBytes>>20)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrNotImage
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("image is too large (%d×%d)", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrNotImage
	}
	// Crop and scale first, then turn upright: the centre square of a
	// turned picture is the turned centre square, and turning 256 pixels
	// square is far cheaper than turning the 12-megapixel original.
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side).Add(image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2))
	square := image.NewRGBA(image.Rect(0, 0, Size, Size))
	draw.CatmullRom.Scale(square, square.Bounds(), img, crop, draw.Src, nil)
	out := upright(square, orientation(raw))

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 88}); err != nil {
		return nil, fmt.Errorf("encode avatar: %w", err)
	}
	return buf.Bytes(), nil
}

// orientation reads a JPEG's EXIF orientation (1–8), or 1 when it has
// none. Cameras store the sensor's pixels as shot and record which way up
// they belong here; decoders ignore it, so without this a portrait phone
// photo arrives on its side.
func orientation(jpg []byte) int {
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(jpg); {
		if jpg[i] != 0xFF {
			return 1
		}
		marker := jpg[i+1]
		size := int(binary.BigEndian.Uint16(jpg[i+2:]))
		if marker == 0xDA || size < 2 || i+2+size > len(jpg) { // start of scan: no EXIF ahead
			return 1
		}
		seg := jpg[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return exifOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

// exifOrientation finds tag 0x0112 in a TIFF block's first directory.
func exifOrientation(tiff []byte) int {
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int(order.Uint32(tiff[4:]))
	if ifd+2 > len(tiff) {
		return 1
	}
	n := int(order.Uint16(tiff[ifd:]))
	for e := 0; e < n; e++ {
		at := ifd + 2 + e*12
		if at+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[at:]) == 0x0112 {
			if v := int(order.Uint16(tiff[at+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// upright applies an EXIF orientation: 2–8 are the mirrorings and quarter
// turns that undo the way the camera was held.
func upright(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if o >= 5 { // 5–8 swap the axes
		w, h = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = b.Dx()-1-x, y
			case 3:
				dx, dy = b.Dx()-1-x, b.Dy()-1-y
			case 4:
				dx, dy = x, b.Dy()-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = b.Dy()-1-y, x
			case 7:
				dx, dy = b.Dy()-1-y, b.Dx()-1-x
			case 8:
				dx, dy = y, b.Dx()-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
