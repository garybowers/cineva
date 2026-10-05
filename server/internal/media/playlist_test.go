package media

import (
	"math"
	"strings"
	"testing"
)

func TestRealSegmentSecs(t *testing.T) {
	tests := []struct {
		name       string
		segSecs    int
		num, den   int
		wantFrames int
		wantSecs   float64
	}{
		{
			// The case this exists for: 24000/1001 is the common film rate,
			// and calling a 144-frame segment "6.0 seconds" loses 6ms each
			// time — about seven seconds across a two-hour film.
			name: "23.976 fps", segSecs: 6, num: 24000, den: 1001,
			wantFrames: 144, wantSecs: 144.0 / (24000.0 / 1001.0),
		},
		{name: "25 fps is exact", segSecs: 6, num: 25, den: 1, wantFrames: 150, wantSecs: 6},
		{name: "29.97 fps", segSecs: 6, num: 30000, den: 1001, wantFrames: 180, wantSecs: 180.0 / (30000.0 / 1001.0)},
		{name: "59.94 fps", segSecs: 6, num: 60000, den: 1001, wantFrames: 360, wantSecs: 360.0 / (60000.0 / 1001.0)},
		{name: "unknown rate falls back to nominal", segSecs: 6, num: 0, den: 0, wantFrames: 0, wantSecs: 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := gopFrames(tc.segSecs, tc.num, tc.den); got != tc.wantFrames {
				t.Errorf("gopFrames = %d, want %d", got, tc.wantFrames)
			}
			got := realSegmentSecs(tc.segSecs, tc.num, tc.den)
			if math.Abs(got-tc.wantSecs) > 1e-9 {
				t.Errorf("realSegmentSecs = %.9f, want %.9f", got, tc.wantSecs)
			}
		})
	}
}

func TestPlaylistDriftOverALongFilm(t *testing.T) {
	// Two hours at 23.976 fps in six-second segments.
	const segSecs, num, den = 6, 24000, 1001
	s := &Session{SegmentSecs: segSecs, NumSegments: 1200}
	s.segSecsReal = realSegmentSecs(segSecs, num, den)

	nominalTotal := float64(segSecs * s.NumSegments)
	realTotal := s.segSecsReal * float64(s.NumSegments)
	drift := realTotal - nominalTotal
	t.Logf("1200 segments: nominal %.1fs, real %.1fs, drift %.1fs", nominalTotal, realTotal, drift)
	if drift < 5 {
		t.Fatalf("expected the nominal playlist to drift by seconds, got %.2fs — the test is not exercising the bug", drift)
	}

	// The playlist must state the real duration, not the nominal one.
	pl := s.Playlist()
	if strings.Contains(pl, "#EXTINF:6.000000") {
		t.Error("playlist still states the nominal 6.0s duration")
	}
	if !strings.Contains(pl, "#EXTINF:6.006") {
		t.Errorf("playlist does not state the real duration; first lines:\n%s", strings.Join(strings.Split(pl, "\n")[:8], "\n"))
	}
}

func TestPlaylistFallsBackWithoutAFrameRate(t *testing.T) {
	s := &Session{SegmentSecs: 6, NumSegments: 3}
	// segSecsReal unset, as for a session started without probe data.
	if !strings.Contains(s.Playlist(), "#EXTINF:6.000000") {
		t.Error("without a frame rate the playlist should fall back to the nominal duration")
	}
}
