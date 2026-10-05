package media

import (
	"testing"
	"time"
)

func TestAheadSecsCollapsesWhenAbandoned(t *testing.T) {
	tests := []struct {
		name       string
		lastReq    int
		sinceTouch time.Duration
		want       int64
	}{
		{
			name:    "an active viewer gets the full look-ahead",
			lastReq: 10, sinceTouch: time.Second,
			want: 10*6 + throttleAheadSecs,
		},
		{
			name:    "still active just under the idle threshold",
			lastReq: 10, sinceTouch: idleAheadAfter - time.Second,
			want: 10*6 + throttleAheadSecs,
		},
		{
			// The case this exists for: a closed tab should stop costing CPU
			// within seconds, not keep transcoding two minutes ahead until
			// the janitor reaps the session minutes later.
			name:    "an abandoned session collapses to a small margin",
			lastReq: 10, sinceTouch: idleAheadAfter + time.Second,
			want: 10*6 + idleAheadSecs,
		},
		{
			name:    "long abandoned stays collapsed",
			lastReq: 100, sinceTouch: 10 * time.Minute,
			want: 100*6 + idleAheadSecs,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{SegmentSecs: 6}
			s.lastReq = tc.lastReq
			s.lastAccess = time.Now().Add(-tc.sinceTouch)
			if got := s.aheadSecs(); got != tc.want {
				t.Errorf("aheadSecs() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestTouchRestoresFullLookAhead(t *testing.T) {
	s := &Session{SegmentSecs: 6}
	s.lastReq = 10
	s.lastAccess = time.Now().Add(-time.Hour)
	collapsed := s.aheadSecs()

	// A viewer coming back from a pause must get their buffer depth back
	// rather than being stuck on the collapsed window.
	s.touch()
	if restored := s.aheadSecs(); restored <= collapsed {
		t.Errorf("after touch aheadSecs() = %d, want more than the collapsed %d", restored, collapsed)
	}
}
