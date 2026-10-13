package media

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/cmplx"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

// Audio fingerprints, for finding the stretch of sound that episodes of a
// series share — the title sequence.
//
// The scheme is the Philips one (Haitsma and Kalker, 2002), which is what
// the well-known fingerprinters descend from: the spectrum is split into 33
// bands between 300 Hz and 2 kHz, and each of the 32 bits of a frame's hash
// says whether the energy difference between two neighbouring bands rose or
// fell since the previous frame. Comparing differences of differences makes
// the hash indifferent to volume and to the broad colour of an encode, so
// the same theme music hashes nearly the same in two episodes mastered or
// compressed differently. Two frames are "the same sound" when their hashes
// differ in only a few bits.
//
// Written here rather than taken from Chromaprint, which our ffmpeg build
// does not include: it is a few dozen lines on top of audio this package
// already decodes, against another C library to build and link.
const (
	// FingerprintRate is hashes per second of audio. A tenth of a second is
	// finer than anyone can judge where an intro starts, and keeps a ten
	// minute window to 6000 hashes.
	FingerprintRate = 10

	fpSampleRate = 11025 // all the 300 Hz-2 kHz bands need, and cheap to transform
	fpFrameSize  = 4096  // 0.37 s, as in the paper: long frames tolerate misalignment
	fpHop        = fpSampleRate / FingerprintRate
	fpBands      = 33
	fpLowHz      = 300.0
	fpHighHz     = 2000.0
	// Frames quieter than this (RMS of samples in [-1, 1]) are marked
	// silent. Digital silence hashes to all zeros in every episode alike, so
	// without this every quiet gap would look like a shared intro.
	fpSilenceRMS = 1e-3
)

// Fingerprint is a sequence of frame hashes at FingerprintRate per second,
// starting at Start seconds into the file.
type Fingerprint struct {
	Start  float64
	Hashes []uint32
	// Silent marks frames too quiet to say anything; they never match.
	Silent []bool
}

// FingerprintFile decodes up to dur seconds of a file's main audio track,
// starting at start, and fingerprints it.
func FingerprintFile(ctx context.Context, path string, start, dur float64) (Fingerprint, error) {
	samples, err := decodeMono(ctx, path, start, dur)
	if err != nil {
		return Fingerprint{}, err
	}
	fp := FingerprintSamples(samples)
	fp.Start = start
	return fp, nil
}

// FingerprintSamples fingerprints mono audio at 11025 Hz.
func FingerprintSamples(samples []float32) Fingerprint {
	var fp Fingerprint
	if len(samples) < fpFrameSize {
		return fp
	}
	window := make([]float64, fpFrameSize)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fpFrameSize-1))
	}
	edges := bandEdges()
	buf := make([]complex128, fpFrameSize)
	prev := make([]float64, fpBands)
	cur := make([]float64, fpBands)
	first := true
	for off := 0; off+fpFrameSize <= len(samples); off += fpHop {
		var sq float64
		for i := 0; i < fpFrameSize; i++ {
			s := float64(samples[off+i])
			sq += s * s
			buf[i] = complex(s*window[i], 0)
		}
		fft(buf)
		for b := 0; b < fpBands; b++ {
			var e float64
			for k := edges[b]; k < edges[b+1]; k++ {
				m := cmplx.Abs(buf[k])
				e += m * m
			}
			cur[b] = e
		}
		if !first {
			var h uint32
			for b := 0; b < 32; b++ {
				if (cur[b]-cur[b+1])-(prev[b]-prev[b+1]) > 0 {
					h |= 1 << b
				}
			}
			fp.Hashes = append(fp.Hashes, h)
			fp.Silent = append(fp.Silent, math.Sqrt(sq/fpFrameSize) < fpSilenceRMS)
		}
		first = false
		prev, cur = cur, prev
	}
	return fp
}

// bandEdges returns the FFT bin boundaries of the fpBands logarithmically
// spaced bands: band b covers bins [edges[b], edges[b+1]).
func bandEdges() []int {
	edges := make([]int, fpBands+1)
	binHz := float64(fpSampleRate) / fpFrameSize
	for b := 0; b <= fpBands; b++ {
		hz := fpLowHz * math.Pow(fpHighHz/fpLowHz, float64(b)/fpBands)
		edges[b] = int(math.Round(hz / binHz))
	}
	return edges
}

// fft is an in-place radix-2 transform; len(a) must be a power of two.
func fft(a []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		step := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u, v := a[start+k], a[start+k+size/2]*w
				a[start+k], a[start+k+size/2] = u+v, u-v
				w *= step
			}
		}
	}
}

// decodeMono decodes dur seconds of the best audio stream from start,
// downmixed to mono float samples at fpSampleRate.
func decodeMono(ctx context.Context, path string, start, dur float64) ([]float32, error) {
	in, err := av.Open(path)
	if err != nil {
		return nil, fmt.Errorf("fingerprint open: %w", err)
	}
	defer in.Close()
	st, err := in.BestStream(ffmpeg.AVMediaTypeAudio)
	if err != nil {
		return nil, fmt.Errorf("fingerprint: no audio stream: %w", err)
	}
	if start > 0 {
		ts := int64(start * float64(ffmpeg.AVTimeBase))
		if _, err := ffmpeg.AVSeekFrame(in.Raw(), -1, ts, ffmpeg.AVSeekFlagBackward); err != nil {
			return nil, fmt.Errorf("fingerprint seek: %w", err)
		}
	}
	dec, err := av.NewDecoder(st)
	if err != nil {
		return nil, fmt.Errorf("fingerprint decoder: %w", err)
	}
	defer dec.Close()
	filter, err := av.NewAudioFilterGraphFromContext(dec.Raw(),
		fmt.Sprintf("aformat=sample_fmts=flt:sample_rates=%d:channel_layouts=mono", fpSampleRate),
		ffmpeg.AVSampleFmtFlt, fpSampleRate, nil)
	if err != nil {
		return nil, fmt.Errorf("fingerprint filter: %w", err)
	}
	defer filter.Close()

	tb := st.TimeBase()
	secs := func(pts int64) float64 { return float64(pts) * float64(tb.Num()) / float64(tb.Den()) }
	want := int(dur * fpSampleRate)
	out := make([]float32, 0, want)
	// After a backward seek the decoder starts a little before the
	// requested point; those samples are dropped so the fingerprint really
	// begins at start.
	skip := -1
	collect := func(frame *ffmpeg.AVFrame) error {
		n := frame.NbSamples()
		if n <= 0 {
			return nil
		}
		s := unsafe.Slice((*float32)(frame.Data().Get(0)), n)
		if skip > 0 {
			drop := min(skip, len(s))
			s, skip = s[drop:], skip-drop
		}
		out = append(out, s[:min(len(s), want-len(out))]...)
		return nil
	}
	decoded := func(frame *ffmpeg.AVFrame) error {
		pts := frame.BestEffortTimestamp()
		if skip < 0 {
			skip = 0
			if pts != ffmpeg.AVNoptsValue && start > 0 {
				skip = max(0, int((start-secs(pts))*fpSampleRate))
			}
		}
		frame.SetPts(pts)
		if err := filter.Push(frame); err != nil {
			return err
		}
		return filter.Pull(collect)
	}

	pkt := ffmpeg.AVPacketAlloc()
	defer ffmpeg.AVPacketFree(&pkt)
	for len(out) < want {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := in.ReadPacket(pkt)
		if errors.Is(err, ffmpeg.AVErrorEOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("fingerprint read: %w", err)
		}
		if pkt.StreamIndex() == st.Index() {
			if err := dec.Decode(pkt, decoded); err != nil {
				ffmpeg.AVPacketUnref(pkt)
				return nil, fmt.Errorf("fingerprint decode: %w", err)
			}
		}
		ffmpeg.AVPacketUnref(pkt)
	}
	return out, nil
}
