package media

import (
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

// Seek-preview thumbnails ("trickplay"): the strip of small frames a player
// shows when you drag the scrub bar.
//
// One sprite sheet and one WebVTT file per item, rather than a file per
// thumbnail. A two-hour film at ten-second intervals is 720 thumbnails; as
// separate images that is 720 requests for one drag of the scrub bar, which
// is worse for the server and worse for the player. The sheet is a single
// fetch and the VTT addresses regions of it with the standard #xywh anchor.
const (
	trickInterval = 10  // seconds between thumbnails
	trickWidth    = 160 // thumbnail width; height follows the aspect ratio
	trickCols     = 10  // sprite grid width, so the sheet stays squarish
	trickQuality  = 70  // JPEG quality: these are 160px wide and scrubbed past
	// A ceiling on how many thumbnails one file gets. Each costs a seek and
	// a decode, so a long 4K film at a fixed ten seconds would be several
	// minutes of work for granularity nobody scrubs at. 400 gives roughly
	// one every twenty seconds across a two-and-a-half-hour film.
	trickMaxThumbs = 400
)

// TrickplayPaths returns where an item's sheet and VTT live.
func TrickplayPaths(dir, itemID string) (sheet, vtt string) {
	return filepath.Join(dir, itemID+".jpg"), filepath.Join(dir, itemID+".vtt")
}

// GenerateTrickplay builds the sprite sheet and VTT for one file.
//
// Expensive but done once: it seeks through the file decoding one frame per
// interval. Callers run it in the background and serve the result when it
// lands; until then a player simply shows no preview, which is how it
// behaves for any file without one.
func GenerateTrickplay(path, outDir, itemID string, runtimeSecs int) error {
	if runtimeSecs <= 0 {
		return fmt.Errorf("trickplay: unknown duration")
	}
	interval := trickInterval
	if n := runtimeSecs / interval; n > trickMaxThumbs {
		// Stretch the interval rather than truncate the film: a preview
		// every 20 seconds across the whole thing beats one every 10 that
		// stops two thirds of the way in.
		interval = (runtimeSecs + trickMaxThumbs - 1) / trickMaxThumbs
	}
	count := runtimeSecs/interval + 1
	started := time.Now()

	in, err := av.Open(path)
	if err != nil {
		return fmt.Errorf("trickplay open: %w", err)
	}
	defer in.Close()
	vs, err := in.BestStream(ffmpeg.AVMediaTypeVideo)
	if err != nil || vs == nil {
		return fmt.Errorf("trickplay: no video stream")
	}
	dec, err := newThreadedDecoder(vs)
	if err != nil {
		return fmt.Errorf("trickplay decoder: %w", err)
	}
	defer dec.Close()

	srcW, srcH := vs.Codecpar().Width(), vs.Codecpar().Height()
	if srcW <= 0 || srcH <= 0 {
		return fmt.Errorf("trickplay: no frame size")
	}
	thumbH := srcH * trickWidth / srcW
	if thumbH < 1 {
		thumbH = 1
	}
	thumbH += thumbH % 2 // keep it even, which swscale prefers

	rows := (count + trickCols - 1) / trickCols
	sheet := image.NewRGBA(image.Rect(0, 0, trickCols*trickWidth, rows*thumbH))

	sws := ffmpeg.SwsGetContext(srcW, srcH, ffmpeg.AVPixelFormat(vs.Codecpar().Format()),
		trickWidth, thumbH, ffmpeg.AVPixFmtRgb24, int(ffmpeg.SwsBilinear), nil, nil, nil)
	if sws == nil {
		return fmt.Errorf("trickplay: cannot build scaler")
	}
	defer ffmpeg.SwsFreeContext(&sws)

	// The destination lives in C memory, not a Go slice. cgo forbids passing
	// a Go pointer that itself contains Go pointers, which a []unsafe.Pointer
	// of Go buffers is — it panics with "Go pointer to unpinned Go pointer".
	dstPlanes, dstLines, _, err := ffmpeg.AVImageAlloc(trickWidth, thumbH, ffmpeg.AVPixFmtRgb24, 1)
	if err != nil {
		return fmt.Errorf("trickplay buffer: %w", err)
	}
	defer ffmpeg.AVImageFreePlanes(dstPlanes)

	pkt := ffmpeg.AVPacketAlloc()
	defer ffmpeg.AVPacketFree(&pkt)

	var cues strings.Builder
	cues.WriteString("WEBVTT\n\n")
	made := 0
	for i := 0; i < count; i++ {
		at := int64(i * interval)
		// Seek to just before the mark and take the first frame decoded —
		// landing on the nearest keyframe is close enough for a preview and
		// far cheaper than decoding everything in between.
		ts := at * int64(ffmpeg.AVTimeBase)
		if _, err := ffmpeg.AVSeekFrame(in.Raw(), -1, ts, ffmpeg.AVSeekFlagBackward); err != nil {
			break
		}
		// Drop anything buffered from the previous seek, so the first frame
		// decoded belongs to this mark and not the last one. Reset, not
		// Flush: Flush drains and leaves the decoder at end-of-stream,
		// after which nothing more can be decoded at all.
		dec.Reset()
		got := false
		for !got {
			if err := in.ReadPacket(pkt); err != nil {
				break
			}
			if pkt.StreamIndex() != vs.Index() {
				continue
			}
			err := dec.Decode(pkt, func(frame *ffmpeg.AVFrame) error {
				if got {
					return nil
				}
				if err := scaleIntoRGB(sws, frame, dstPlanes, dstLines); err != nil {
					return err
				}
				col, row := i%trickCols, i/trickCols
				blitRGB(sheet, dstPlanes[0], dstLines[0], col*trickWidth, row*thumbH, trickWidth, thumbH)
				got = true
				return nil
			})
			if err != nil {
				break
			}
		}
		if !got {
			break // ran out of file
		}
		writeCue(&cues, itemID, at, int64(interval), i, thumbH)
		made++
	}
	if made == 0 {
		return fmt.Errorf("trickplay: no frames decoded")
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("trickplay dir: %w", err)
	}
	sheetPath, vttPath := TrickplayPaths(outDir, itemID)
	if err := writeJPEG(sheetPath, sheet); err != nil {
		return err
	}
	// The VTT is written last, so its presence means the sheet it points at
	// is complete — a half-written sheet is never served.
	if err := os.WriteFile(vttPath, []byte(cues.String()), 0o644); err != nil {
		return fmt.Errorf("trickplay vtt: %w", err)
	}
	log.Printf("trickplay %s: %d previews every %ds in %s", itemID, made, interval,
		time.Since(started).Round(time.Second))
	return nil
}

// writeCue appends one WebVTT cue addressing a region of the sheet.
func writeCue(b *strings.Builder, itemID string, at, interval int64, idx, thumbH int) {
	col, row := idx%trickCols, idx/trickCols
	fmt.Fprintf(b, "%s --> %s\n%s.jpg#xywh=%d,%d,%d,%d\n\n",
		vttTime(at), vttTime(at+interval), itemID,
		col*trickWidth, row*thumbH, trickWidth, thumbH)
}

func vttTime(secs int64) string {
	return fmt.Sprintf("%02d:%02d:%02d.000", secs/3600, (secs%3600)/60, secs%60)
}

// scaleIntoRGB scales one decoded frame into a C-allocated RGB24 buffer.
func scaleIntoRGB(sws *ffmpeg.SwsContext, frame *ffmpeg.AVFrame, dstPlanes []unsafe.Pointer, dstLines []int) error {
	srcPlanes := make([]unsafe.Pointer, 4)
	srcLines := make([]int, 4)
	for i := 0; i < 4; i++ {
		srcPlanes[i] = frame.Data().Get(uintptr(i))
		srcLines[i] = frame.Linesize().Get(uintptr(i))
	}
	if _, err := ffmpeg.SwsScale(sws, srcPlanes, srcLines, 0, frame.Height(), dstPlanes, dstLines); err != nil {
		return fmt.Errorf("scale thumbnail: %w", err)
	}
	return nil
}

// blitRGB copies a packed RGB24 image out of C memory into the sheet at
// (x, y), adding the opaque alpha channel the RGBA sheet wants.
func blitRGB(sheet *image.RGBA, src unsafe.Pointer, stride, x, y, w, h int) {
	row := unsafe.Slice((*byte)(src), stride*h)
	tile := &image.RGBA{
		Pix:    make([]uint8, w*h*4),
		Stride: w * 4,
		Rect:   image.Rect(0, 0, w, h),
	}
	for yy := 0; yy < h; yy++ {
		si, di := yy*stride, yy*w*4
		for xx := 0; xx < w; xx++ {
			tile.Pix[di] = row[si]
			tile.Pix[di+1] = row[si+1]
			tile.Pix[di+2] = row[si+2]
			tile.Pix[di+3] = 0xFF
			si, di = si+3, di+4
		}
	}
	draw.Draw(sheet, image.Rect(x, y, x+w, y+h), tile, image.Point{}, draw.Src)
}

func writeJPEG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("trickplay sheet: %w", err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: trickQuality}); err != nil {
		return fmt.Errorf("encode trickplay sheet: %w", err)
	}
	return nil
}
