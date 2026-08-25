// Package stats samples host CPU and memory for the admin dashboard.
// Samples live in memory only (a fine ring for the last hour and a coarse
// ring for the last day); nothing is persisted or sent anywhere.
package stats

import (
	"bufio"
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sample is one point of host + process resource usage.
type Sample struct {
	At       time.Time `json:"at"`
	CPUPct   float64   `json:"cpuPct"`   // whole-host CPU busy, 0–100
	MemUsed  uint64    `json:"memUsed"`  // bytes (MemTotal − MemAvailable)
	MemTotal uint64    `json:"memTotal"` // bytes
	ProcRSS  uint64    `json:"procRss"`  // this server's resident set, bytes
	NumCPU   int       `json:"numCpu"`
}

// Sampler collects samples on an interval and keeps two rings: every
// sample for the last hour, and one-per-minute for the last 24 hours.
type Sampler struct {
	Interval time.Duration
	OnSample func(Sample) // optional; called after each sample (event bus)

	mu                  sync.Mutex
	fine                []Sample // last hour at Interval
	coarse              []Sample // last 24h at 1/min
	lastMinute          time.Time
	prevIdle, prevTotal uint64
}

func New(interval time.Duration) *Sampler {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Sampler{Interval: interval}
}

// Run samples until ctx is cancelled. Call in its own goroutine.
func (s *Sampler) Run(ctx context.Context) {
	// Prime the CPU counters so the first real sample has a delta.
	s.prevIdle, s.prevTotal = readCPU()
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.take(now)
		}
	}
}

func (s *Sampler) take(now time.Time) {
	idle, total := readCPU()
	var pct float64
	if total > s.prevTotal {
		dTotal := float64(total - s.prevTotal)
		dIdle := float64(idle - s.prevIdle)
		pct = 100 * (1 - dIdle/dTotal)
		if pct < 0 {
			pct = 0
		}
	}
	s.prevIdle, s.prevTotal = idle, total
	used, totalMem := readMem()
	smp := Sample{
		At: now.UTC(), CPUPct: pct, MemUsed: used, MemTotal: totalMem,
		ProcRSS: readRSS(), NumCPU: numCPU(),
	}

	s.mu.Lock()
	s.fine = append(s.fine, smp)
	if cut := now.Add(-time.Hour); len(s.fine) > 0 && s.fine[0].At.Before(cut) {
		i := 0
		for i < len(s.fine) && s.fine[i].At.Before(cut) {
			i++
		}
		s.fine = s.fine[i:]
	}
	if now.Sub(s.lastMinute) >= time.Minute {
		s.lastMinute = now
		s.coarse = append(s.coarse, smp)
		if cut := now.Add(-24 * time.Hour); len(s.coarse) > 0 && s.coarse[0].At.Before(cut) {
			i := 0
			for i < len(s.coarse) && s.coarse[i].At.Before(cut) {
				i++
			}
			s.coarse = s.coarse[i:]
		}
	}
	s.mu.Unlock()

	if s.OnSample != nil {
		s.OnSample(smp)
	}
}

// History returns the last hour at full resolution, or the last day at
// one-minute resolution when day is true.
func (s *Sampler) History(day bool) []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.fine
	if day {
		src = s.coarse
	}
	out := make([]Sample, len(src))
	copy(out, src)
	return out
}

func numCPU() int { return runtime.NumCPU() }

// ---- /proc readers (Linux; zero values elsewhere) ----

func readCPU() (idle, total uint64) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0
	}
	for i, v := range fields[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	return idle, total
}

func readMem() (used, total uint64) {
	vals := procKV("/proc/meminfo", "MemTotal:", "MemAvailable:")
	total = vals["MemTotal:"] * 1024
	if avail := vals["MemAvailable:"] * 1024; total >= avail {
		used = total - avail
	}
	return used, total
}

func readRSS() uint64 {
	return procKV("/proc/self/status", "VmRSS:")["VmRSS:"] * 1024
}

// procKV pulls "Key:  12345 kB" lines out of a /proc file.
func procKV(path string, keys ...string) map[string]uint64 {
	out := map[string]uint64{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		for _, k := range keys {
			if strings.HasPrefix(line, k) {
				fields := strings.Fields(line[len(k):])
				if len(fields) > 0 {
					out[k], _ = strconv.ParseUint(fields[0], 10, 64)
				}
			}
		}
	}
	return out
}
