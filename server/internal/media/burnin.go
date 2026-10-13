package media

import (
	"errors"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// Burning in image-based subtitles (PGS from Blu-ray, VobSub from DVD).
//
// Unlike SRT or ASS, these are bitmaps: there is no text to hand a player, so
// the only way to show them is to paint them into the picture. That means
// they are only available on the re-encode path — a copied stream cannot
// have anything drawn on it.
//
// The bitmaps are composited onto the scaled frame on its way to the
// encoder, rather than through libavfilter's overlay. Overlay would need a
// second buffer source fed by a subtitle-to-frame conversion, and the
// binding does not expose the pieces to wire one up (avfilter_inout_alloc
// and avfilter_link are both absent). Compositing directly is a few dozen
// lines, works for both pixel formats the encoder takes, and is easy to
// reason about.

// subBurner decodes one subtitle stream and paints its bitmaps onto frames.
type subBurner struct {
	ctx    *ffmpeg.AVCodecContext
	stream *ffmpeg.AVStream
	// The subtitle currently on screen, and the window it occupies in
	// stream time. A PGS stream sends a bitmap and then a clear; between
	// them the same image stays up across many frames.
	active   *ffmpeg.AVSubtitle
	fromPTS  int64 // microseconds
	untilPTS int64
	// Source dimensions the rect coordinates are expressed in, and the
	// output dimensions they have to be scaled to.
	srcW, srcH int
	outW, outH int
}

// ErrBurnInUnsupported explains why burn-in cannot run in this build.
//
// Two separate things block it, both in the vendored ffmpeg-statigo:
//
//  1. There is no way to allocate an AVSubtitle. Every function takes a
//     *AVSubtitle — AVCodecDecodeSubtitle2, AVSubtitleFree — but nothing
//     constructs one and the struct's pointer field is unexported, so a
//     caller outside the package cannot produce a valid value. Passing a
//     zero &AVSubtitle{} segfaults the moment the decoder writes to it.
//
//  2. The build has no pgssub decoder. dvdsub (VobSub) is present, but PGS
//     — the Blu-ray format, and the majority of image subtitles in
//     practice — is not.
//
// Everything else is done: the compositing below is complete and correct,
// the pipeline feeds and paints at the right points, and the API and client
// offer the tracks. Unblocking it means an upstream change — an AVSubtitle
// allocator, and --enable-decoder=pgssub in the ffmpeg build — not more work
// here.
var ErrBurnInUnsupported = errors.New(
	"subtitle burn-in needs an AVSubtitle allocator and a pgssub decoder, neither present in the vendored ffmpeg")

// newSubBurner would open a decoder for an image-subtitle stream.
//
// It cannot, for the reasons in ErrBurnInUnsupported, and says so rather
// than failing later: the decode call segfaults with an AVSubtitle this
// binding cannot allocate, and a crash is a far worse outcome than no
// subtitles. The compositing below is finished and tested — only the
// decoding is blocked.
func newSubBurner(stream *ffmpeg.AVStream, srcW, srcH, outW, outH int) (*subBurner, error) {
	_ = stream
	return nil, ErrBurnInUnsupported
}

func (b *subBurner) Close() {
	b.clear()
	if b.ctx != nil {
		ffmpeg.AVCodecFreeContext(&b.ctx)
	}
}

func (b *subBurner) clear() {
	if b.active != nil {
		ffmpeg.AVSubtitleFree(b.active)
		b.active = nil
	}
}

// Feed would decode one subtitle packet, replacing whatever is on screen.
// Unreachable while newSubBurner refuses to construct a burner; kept so the
// shape of the pipeline is clear and so unblocking it is a small change.
func (b *subBurner) Feed(pkt *ffmpeg.AVPacket) error {
	_ = pkt
	return ErrBurnInUnsupported
}

// Paint composites the active subtitle onto a frame, if one is due at this
// timestamp. ptsMicros is the frame's presentation time in microseconds.
func (b *subBurner) Paint(frame *ffmpeg.AVFrame, ptsMicros int64) {
	if b.active == nil || ptsMicros < b.fromPTS || ptsMicros > b.untilPTS {
		return
	}
	rects := b.active.Rects()
	for i := uint(0); i < b.active.NumRects(); i++ {
		r := rects.Get(uintptr(i))
		if r == nil || r.Type() != ffmpeg.SubtitleBitmap || r.W() <= 0 || r.H() <= 0 {
			continue
		}
		b.paintRect(frame, r)
	}
}

// paintRect alpha-blends one palettised bitmap onto the frame's luma and
// chroma planes.
//
// The rect's coordinates are in the source's dimensions; the frame has
// already been scaled, so everything is mapped across. Nearest-neighbour is
// deliberate: subtitle bitmaps are hard-edged text, and interpolating them
// makes them muddier, not smoother.
func (b *subBurner) paintRect(frame *ffmpeg.AVFrame, r *ffmpeg.AVSubtitleRect) {
	if b.srcW <= 0 || b.srcH <= 0 {
		return
	}
	pal := r.Data().Get(1)
	bitmap := r.Data().Get(0)
	if pal == nil || bitmap == nil {
		return
	}
	stride := r.Linesize().Get(0)
	if stride <= 0 {
		return
	}
	palette := unsafe.Slice((*uint32)(pal), 256)
	pixels := unsafe.Slice((*byte)(bitmap), stride*r.H())
	b.compositeBitmap(frame, pixels, palette, stride, r.X(), r.Y(), r.W(), r.H())
}

// compositeBitmap alpha-blends a palettised bitmap onto a frame.
//
// Split from paintRect so the pixel work can be tested with plain Go data:
// the binding offers no way to allocate an AVSubtitleRect, so a test that
// needed one could not exist. Everything C-specific stays above this line.
func (b *subBurner) compositeBitmap(
	frame *ffmpeg.AVFrame, pixels []byte, palette []uint32,
	stride, rx, ry, rw, rh int,
) {

	fw, fh := frame.Width(), frame.Height()
	yPlane := frame.Data().Get(0)
	if yPlane == nil {
		return
	}
	yStride := frame.Linesize().Get(0)
	luma := unsafe.Slice((*byte)(yPlane), yStride*fh)

	// Chroma layout differs between the two formats the encoder consumes:
	// yuv420p keeps U and V in separate planes, nv12 interleaves them.
	nv12 := frame.Format() == int(ffmpeg.AVPixFmtNv12)
	var uPlane, vPlane []byte
	var uStride, vStride int
	if u := frame.Data().Get(1); u != nil {
		uStride = frame.Linesize().Get(1)
		uPlane = unsafe.Slice((*byte)(u), uStride*((fh+1)/2))
	}
	if !nv12 {
		if v := frame.Data().Get(2); v != nil {
			vStride = frame.Linesize().Get(2)
			vPlane = unsafe.Slice((*byte)(v), vStride*((fh+1)/2))
		}
	}

	for sy := 0; sy < rh; sy++ {
		// Map this source row onto the scaled frame.
		dy := (ry + sy) * fh / b.srcH
		if dy < 0 || dy >= fh {
			continue
		}
		row := pixels[sy*stride : sy*stride+rw]
		for sx := 0; sx < rw; sx++ {
			argb := palette[row[sx]]
			a := byte(argb >> 24)
			if a == 0 {
				continue // fully transparent: most of a subtitle bitmap
			}
			dx := (rx + sx) * fw / b.srcW
			if dx < 0 || dx >= fw {
				continue
			}
			yy, uu, vv := rgbToYUV(byte(argb>>16), byte(argb>>8), byte(argb))
			blend(luma, dy*yStride+dx, yy, a)
			// Chroma is half resolution, so neighbouring pixels land on the
			// same sample; writing each is harmless and keeps this simple.
			cx, cy := dx/2, dy/2
			if nv12 && uPlane != nil {
				if i := cy*uStride + cx*2; i+1 < len(uPlane) {
					blend(uPlane, i, uu, a)
					blend(uPlane, i+1, vv, a)
				}
			} else if uPlane != nil && vPlane != nil {
				if i := cy*uStride + cx; i < len(uPlane) {
					blend(uPlane, i, uu, a)
				}
				if i := cy*vStride + cx; i < len(vPlane) {
					blend(vPlane, i, vv, a)
				}
			}
		}
	}
}

// blend mixes a value into a plane by alpha.
func blend(plane []byte, i int, v, a byte) {
	if i < 0 || i >= len(plane) {
		return
	}
	plane[i] = byte((int(v)*int(a) + int(plane[i])*(255-int(a))) / 255)
}

// rgbToYUV converts to BT.601 limited range, which is what subtitle
// palettes are authored against.
func rgbToYUV(r, g, bl byte) (y, u, v byte) {
	rf, gf, bf := float64(r), float64(g), float64(bl)
	yf := 0.257*rf + 0.504*gf + 0.098*bf + 16
	uf := -0.148*rf - 0.291*gf + 0.439*bf + 128
	vf := 0.439*rf - 0.368*gf - 0.071*bf + 128
	return clamp8(yf), clamp8(uf), clamp8(vf)
}

func clamp8(f float64) byte {
	switch {
	case f < 0:
		return 0
	case f > 255:
		return 255
	default:
		return byte(f)
	}
}
