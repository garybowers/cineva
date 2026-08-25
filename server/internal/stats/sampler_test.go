package stats

import (
	"testing"
	"time"
)

func TestSamplerRingsTrim(t *testing.T) {
	s := New(time.Second)
	s.prevIdle, s.prevTotal = readCPU()
	base := time.Now()
	// 90 minutes of one-second samples: the fine ring keeps the last hour,
	// the coarse ring one per minute.
	for i := 0; i < 90*60; i += 20 {
		s.take(base.Add(time.Duration(i) * time.Second))
	}
	fine := s.History(false)
	if n := len(fine); n < 170 || n > 181 {
		t.Fatalf("fine ring holds %d samples, want ~180 (last hour at 20s)", n)
	}
	if got := fine[0].At; got.Before(base.Add(29 * time.Minute)) {
		t.Fatalf("fine ring oldest sample %v is older than an hour", got)
	}
	coarse := s.History(true)
	if n := len(coarse); n < 85 || n > 91 {
		t.Fatalf("coarse ring holds %d samples, want ~90 (one per minute)", n)
	}
	if coarse[len(coarse)-1].MemTotal == 0 {
		t.Fatal("MemTotal is zero — /proc/meminfo not read")
	}
}
