package store

import (
	"context"
	"fmt"
	"time"
)

// StatsSample is one persisted host sample. It mirrors stats.Sample, kept
// separate so the store does not depend on the stats package.
type StatsSample struct {
	At       time.Time
	CPUPct   float64
	MemUsed  uint64
	MemTotal uint64
	ProcRSS  uint64
	NumCPU   int
}

type StatsRepo interface {
	AppendStatsSample(ctx context.Context, s StatsSample) error
	StatsSamplesSince(ctx context.Context, since time.Time) ([]StatsSample, error)
	PruneStatsSamples(ctx context.Context, before time.Time) (int64, error)
}

var _ StatsRepo = (*Store)(nil)

// statsTimeLayout is SQLite's own datetime shape, so the text sorts
// chronologically and comparisons against datetime() work.
const statsTimeLayout = "2006-01-02 15:04:05"

func (s *Store) AppendStatsSample(ctx context.Context, smp StatsSample) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO stats_samples (at, cpu_pct, mem_used, mem_total, proc_rss, num_cpu)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(at) DO UPDATE SET
		   cpu_pct = excluded.cpu_pct, mem_used = excluded.mem_used,
		   mem_total = excluded.mem_total, proc_rss = excluded.proc_rss,
		   num_cpu = excluded.num_cpu`,
		smp.At.UTC().Format(statsTimeLayout), smp.CPUPct, smp.MemUsed,
		smp.MemTotal, smp.ProcRSS, smp.NumCPU); err != nil {
		return fmt.Errorf("append stats sample: %w", err)
	}
	return nil
}

// StatsSamplesSince returns samples at or after a time, oldest first.
func (s *Store) StatsSamplesSince(ctx context.Context, since time.Time) ([]StatsSample, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT at, cpu_pct, mem_used, mem_total, proc_rss, num_cpu
		 FROM stats_samples WHERE at >= ? ORDER BY at`,
		since.UTC().Format(statsTimeLayout))
	if err != nil {
		return nil, fmt.Errorf("stats samples: %w", err)
	}
	defer rows.Close()
	out := []StatsSample{}
	for rows.Next() {
		var smp StatsSample
		var at string
		if err := rows.Scan(&at, &smp.CPUPct, &smp.MemUsed, &smp.MemTotal, &smp.ProcRSS, &smp.NumCPU); err != nil {
			return nil, fmt.Errorf("scan stats sample: %w", err)
		}
		// Stored as UTC; parse back as UTC so a restart in a different
		// timezone does not shift the history.
		if smp.At, err = time.ParseInLocation(statsTimeLayout, at, time.UTC); err != nil {
			return nil, fmt.Errorf("parse stats sample time %q: %w", at, err)
		}
		out = append(out, smp)
	}
	return out, rows.Err()
}

func (s *Store) PruneStatsSamples(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM stats_samples WHERE at < ?`, before.UTC().Format(statsTimeLayout))
	if err != nil {
		return 0, fmt.Errorf("prune stats samples: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
