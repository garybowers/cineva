package segments

import (
	"math"
	"math/rand"
	"testing"

	"github.com/garybowers/cineva/server/internal/media"
)

const rate = 11025

// theme is a stand-in for a title sequence: a melody of tones over a little
// noise, deterministic for a seed, so two episodes can carry the same one.
func theme(seed int64, secs float64) []float32 {
	r := rand.New(rand.NewSource(seed))
	n := int(secs * rate)
	out := make([]float32, n)
	note := 0.0
	for i := range out {
		if i%(rate/4) == 0 { // a new note every quarter second
			note = 220 * math.Pow(2, float64(r.Intn(24))/12)
		}
		t := float64(i) / rate
		out[i] = float32(0.3*math.Sin(2*math.Pi*note*t) + 0.15*math.Sin(2*math.Pi*1.5*note*t) + 0.02*(r.Float64()-0.5))
	}
	return out
}

// episode is an opening of total seconds with the theme starting at at,
// everything else being material of its own.
func episode(seed int64, total, at float64, th []float32) []float32 {
	out := theme(seed, total)
	copy(out[int(at*rate):], th)
	return out
}

// quieter scales samples, standing in for a different mix of the same audio.
func quieter(s []float32, k float32) []float32 {
	out := make([]float32, len(s))
	for i, v := range s {
		out[i] = v * k
	}
	return out
}

func TestLongestSharedFindsTheIntro(t *testing.T) {
	th := theme(1, 40)
	tests := []struct {
		name   string
		a, b   []float32
		aStart float64
		bStart float64
		found  bool
	}{
		{"same position", episode(2, 180, 30, th), episode(3, 180, 30, th), 30, 30, true},
		// A cold open of a different length moves the intro.
		{"different cold opens", episode(2, 180, 10, th), episode(3, 180, 95, th), 10, 95, true},
		// Offsets that fall between frame boundaries, as real ones do.
		{"misaligned frames", episode(2, 180, 10.04, th), episode(3, 180, 95.07, th), 10.04, 95.07, true},
		// Volume and mix differences must not matter.
		{"different level", episode(2, 180, 20, th), quieter(episode(3, 180, 50, th), 0.4), 20, 50, true},
		{"nothing shared", theme(4, 180), theme(5, 180), 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, ok := LongestShared(media.FingerprintSamples(tc.a), media.FingerprintSamples(tc.b), 15, 120)
			if ok != tc.found {
				t.Fatalf("found = %v, want %v (%+v)", ok, tc.found, m)
			}
			if !ok {
				return
			}
			// Within a second either side, and about the theme's length.
			if math.Abs(m.AStart-tc.aStart) > 1 || math.Abs(m.BStart-tc.bStart) > 1 || math.Abs(m.Len()-40) > 2 {
				t.Errorf("match %+v, want A from %v and B from %v, 40s long", m, tc.aStart, tc.bStart)
			}
		})
	}
}

// Digital silence hashes identically everywhere; it must not look shared.
func TestLongestSharedIgnoresSilence(t *testing.T) {
	a := theme(6, 120)
	b := theme(7, 120)
	for i := 30 * rate; i < 70*rate; i++ {
		a[i], b[i] = 0, 0
	}
	if m, ok := LongestShared(media.FingerprintSamples(a), media.FingerprintSamples(b), 15, 120); ok {
		t.Fatalf("silence matched as %+v", m)
	}
}

func TestPickPrefersAgreement(t *testing.T) {
	intro := Match{AStart: 60, AEnd: 110}
	recap := Match{AStart: 0, AEnd: 90} // longer, but only one comparison saw it
	got, ok := pick([]Match{recap, intro, {AStart: 61, AEnd: 109}, {AStart: 60, AEnd: 111}}, 1)
	// The median of the three that agree.
	if !ok || got.AStart != 60 || got.AEnd != 110 {
		t.Fatalf("picked %+v, want the intro at 60-110", got)
	}
}

// A lone candidate is enough when one agreeing comparison is asked for, and
// not when two are.
func TestPickMinAgree(t *testing.T) {
	lone := []Match{{AStart: 3600, AEnd: 3633}}
	pair := []Match{{AStart: 3500, AEnd: 3530}, {AStart: 3501, AEnd: 3531}}
	tests := []struct {
		name     string
		cands    []Match
		minAgree int
		want     bool
	}{
		{"lone, one needed", lone, 1, true},
		{"lone, two needed", lone, 2, false},
		{"pair, two needed", pair, 2, true},
		{"none", nil, 1, false},
	}
	for _, tc := range tests {
		if _, ok := pick(tc.cands, tc.minAgree); ok != tc.want {
			t.Errorf("%s: found = %v, want %v", tc.name, ok, tc.want)
		}
	}
}
