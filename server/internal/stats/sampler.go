// Package stats samples host CPU and memory for the admin dashboard.
//
// Two rings: a fine one at the sample interval for the last hour, and a
// coarse one at one a minute. The coarse ring is persisted through an
// optional Store, so the charts survive a restart — the question an
// operator actually has is "was it struggling while I was out?", which an
// in-memory-only history can never answer. The fine ring stays in memory:
// smoothing the last hour is not worth a write every few seconds.
// Nothing is sent anywhere.
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

	"github.com/garybowers/cineva/server/internal/logging"
)

var logger = logging.For("stats")

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
// Store persists the coarse series. Optional: a nil Store keeps the old
// in-memory-only behaviour, which is what the tests and `scan` want.
type Store interface {
	AppendStatsSample(ctx context.Context, s StoredSample) error
	StatsSamplesSince(ctx context.Context, since time.Time) ([]StoredSample, error)
	PruneStatsSamples(ctx context.Context, before time.Time) (int64, error)
}

// StoredSample is the persisted shape. It mirrors Sample; the indirection
// keeps this package free of a store dependency and the store free of this
// one.
type StoredSample struct {
	At       time.Time
	CPUPct   float64
	MemUsed  uint64
	MemTotal uint64
	ProcRSS  uint64
	NumCPU   int
}

type Sampler struct {
	Interval time.Duration
	OnSample func(Sample) // optional; called after each sample (event bus)

	// Store, when set, persists coarse samples. Retain is how far back to
	// keep them; zero means 24 hours.
	Store  Store
	Retain time.Duration

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
	s.restore(ctx)
	// Prime the CPU counters so the first real sample has a delta.
	s.prevIdle, s.prevTotal = readCPU()
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	// Pruning once an hour is often enough for a table that gains a row a
	// minute, and keeps it off the sampling path.
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.take(now)
		case now := <-prune.C:
			if s.Store != nil {
				if _, err := s.Store.PruneStatsSamples(ctx, now.Add(-s.retain())); err != nil {
					logger.Warn("pruning samples failed", "err", err)
				}
			}
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
	coarseTick := now.Sub(s.lastMinute) >= time.Minute
	if coarseTick {
		s.lastMinute = now
		s.coarse = append(s.coarse, smp)
		if cut := now.Add(-s.retain()); len(s.coarse) > 0 && s.coarse[0].At.Before(cut) {
			i := 0
			for i < len(s.coarse) && s.coarse[i].At.Before(cut) {
				i++
			}
			s.coarse = s.coarse[i:]
		}
	}
	s.mu.Unlock()

	// Persisted outside the lock: a database write must not hold up a
	// reader of the rings. A failure here loses one minute of chart, which
	// is not worth stopping sampling for, so it is logged and dropped.
	if coarseTick && s.Store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.Store.AppendStatsSample(ctx, stored(smp)); err != nil {
			logger.Warn("persisting sample failed", "err", err)
		}
		cancel()
	}

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

// retain is how far back coarse samples are kept, defaulting to a day.
func (s *Sampler) retain() time.Duration {
	if s.Retain > 0 {
		return s.Retain
	}
	return 24 * time.Hour
}

func stored(s Sample) StoredSample {
	return StoredSample{
		At: s.At, CPUPct: s.CPUPct, MemUsed: s.MemUsed,
		MemTotal: s.MemTotal, ProcRSS: s.ProcRSS, NumCPU: s.NumCPU,
	}
}

// restore seeds the coarse ring from the database so the dashboard shows
// the history either side of a restart rather than starting from nothing.
//
// The fine ring is deliberately left empty: it covers the last hour at
// sampling resolution and will refill on its own within the hour, whereas
// pretending minute-resolution rows are fine samples would draw a chart
// whose shape lies about what was measured.
func (s *Sampler) restore(ctx context.Context) {
	if s.Store == nil {
		return
	}
	rows, err := s.Store.StatsSamplesSince(ctx, time.Now().Add(-s.retain()))
	if err != nil {
		logger.Warn("restoring history failed", "err", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	s.mu.Lock()
	for _, r := range rows {
		s.coarse = append(s.coarse, Sample{
			At: r.At, CPUPct: r.CPUPct, MemUsed: r.MemUsed,
			MemTotal: r.MemTotal, ProcRSS: r.ProcRSS, NumCPU: r.NumCPU,
		})
	}
	// Treat the newest restored row as the last coarse tick, so sampling
	// resumes on the minute boundary rather than immediately duplicating it.
	s.lastMinute = rows[len(rows)-1].At
	s.mu.Unlock()
	logger.Debug("restored history", "count", len(rows),
		"covering", time.Since(rows[0].At).Round(time.Minute))
}
