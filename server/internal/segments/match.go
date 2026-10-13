// Package segments finds the parts of an episode that are not the episode —
// the title sequence, for now — so the player can offer to skip them.
//
// Nothing marks an intro in the file. What gives one away is repetition:
// the same theme plays near the start of every episode of a season, while
// everything around it differs. So each episode's opening is fingerprinted
// (media.FingerprintFile) and compared with its neighbours', and the
// stretch of sound they share is the intro. This is how Plex and Jellyfin's
// Intro Skipper do it too.
package segments

import (
	"math/bits"
	"sort"

	"github.com/garybowers/cineva/server/internal/media"
)

// Match is a stretch of sound two fingerprints share, in seconds into each
// file.
type Match struct {
	AStart, AEnd float64
	BStart, BEnd float64
}

func (m Match) Len() float64 { return m.AEnd - m.AStart }

const (
	// Two frames are the same sound when their 32-bit hashes differ in at
	// most this many bits. Unrelated audio differs in about 16; the same
	// passage through two different encodes, in a handful.
	maxBitErrors = 8
	// A run survives gaps this long (in frames) without a match: a sound
	// effect or a line of dialogue laid over the theme breaks the match for
	// a moment without ending the intro.
	maxGapFrames = 2 * media.FingerprintRate
	// And at least this share of a run's frames must actually match, so a
	// scatter of chance matches strung together by the gap allowance does
	// not pass for one.
	minMatchShare = 0.5
)

// LongestShared finds the longest stretch of sound present in both
// fingerprints, between minSecs and maxSecs long, by trying every
// alignment of one against the other.
//
// Trying every alignment is quadratic, but cheap at this scale: two ten
// minute openings are 6000 hashes each, so 36 million XOR-and-popcounts,
// which takes tens of milliseconds. An index of exact hash values would be
// faster, but exact matches are what a re-encode erodes first, and this
// needs to find the theme in two episodes encoded by different people.
func LongestShared(a, b media.Fingerprint, minSecs, maxSecs float64) (Match, bool) {
	minLen := int(minSecs * media.FingerprintRate)
	maxLen := int(maxSecs * media.FingerprintRate)
	na, nb := len(a.Hashes), len(b.Hashes)
	bestLen, bestA, bestB := 0, 0, 0
	for shift := -(nb - 1); shift < na; shift++ {
		lo, hi := max(0, shift), min(na, nb+shift)
		start, last, matched := -1, 0, 0
		closeRun := func() {
			if start < 0 {
				return
			}
			n := last - start + 1
			if n >= minLen && n <= maxLen && n > bestLen &&
				float64(matched) >= minMatchShare*float64(n) {
				bestLen, bestA, bestB = n, start, start-shift
			}
			start = -1
		}
		for i := lo; i < hi; i++ {
			j := i - shift
			if !a.Silent[i] && !b.Silent[j] && bits.OnesCount32(a.Hashes[i]^b.Hashes[j]) <= maxBitErrors {
				if start < 0 {
					start, matched = i, 0
				}
				last = i
				matched++
				continue
			}
			if start >= 0 && i-last > maxGapFrames {
				closeRun()
			}
		}
		closeRun()
	}
	if bestLen == 0 {
		return Match{}, false
	}
	at := func(fp media.Fingerprint, i int) float64 { return fp.Start + float64(i)/media.FingerprintRate }
	return Match{
		AStart: at(a, bestA), AEnd: at(a, bestA+bestLen),
		BStart: at(b, bestB), BEnd: at(b, bestB+bestLen),
	}, true
}

// agreeSecs is how close two candidates' boundaries must be to count as the
// same intro.
const agreeSecs = 3.0

// pick chooses an episode's intro from what comparisons with several other
// episodes found, as start and end seconds in this episode.
//
// The candidate most of the others agree with wins, then the longest.
// Agreement guards against the one comparison that found something else
// shared — a recap of the previous episode, say, which the previous episode
// itself also contains. The boundaries are then the median of the
// candidates that agree: each comparison places the edges a little
// differently, depending on what the other episode has either side of its
// intro, and the median is the placement most of them support.
//
// minAgree is how many candidates, the winner included, must agree for
// there to be an answer at all.
func pick(cands []Match, minAgree int) (Match, bool) {
	best, bestScore := -1, -1
	agrees := func(c, o Match) bool {
		return abs(c.AStart-o.AStart) <= agreeSecs && abs(c.AEnd-o.AEnd) <= agreeSecs
	}
	for i, c := range cands {
		score := 0
		for j, o := range cands {
			if i != j && agrees(c, o) {
				score++
			}
		}
		if score > bestScore || (score == bestScore && c.Len() > cands[best].Len()) {
			best, bestScore = i, score
		}
	}
	if best < 0 || bestScore+1 < minAgree {
		return Match{}, false
	}
	var starts, ends []float64
	for _, o := range cands {
		if agrees(cands[best], o) {
			starts, ends = append(starts, o.AStart), append(ends, o.AEnd)
		}
	}
	m := cands[best]
	m.AStart, m.AEnd = median(starts), median(ends)
	return m, true
}

func median(xs []float64) float64 {
	sort.Float64s(xs)
	n := len(xs)
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
