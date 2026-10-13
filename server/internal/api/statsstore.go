package api

import (
	"context"
	"time"

	"github.com/garybowers/cineva/server/internal/stats"
	"github.com/garybowers/cineva/server/internal/store"
)

// statsStore lets the sampler persist through the repository.
//
// The two packages carry the same six fields under different types on
// purpose: the store must not depend on the sampler, nor the sampler on the
// store, so the conversion lives here where both are already in scope.
type statsStore struct{ repo store.StatsRepo }

var _ stats.Store = statsStore{}

func (a statsStore) AppendStatsSample(ctx context.Context, s stats.StoredSample) error {
	return a.repo.AppendStatsSample(ctx, store.StatsSample{
		At: s.At, CPUPct: s.CPUPct, MemUsed: s.MemUsed,
		MemTotal: s.MemTotal, ProcRSS: s.ProcRSS, NumCPU: s.NumCPU,
	})
}

func (a statsStore) StatsSamplesSince(ctx context.Context, since time.Time) ([]stats.StoredSample, error) {
	rows, err := a.repo.StatsSamplesSince(ctx, since)
	if err != nil {
		return nil, err
	}
	out := make([]stats.StoredSample, 0, len(rows))
	for _, r := range rows {
		out = append(out, stats.StoredSample{
			At: r.At, CPUPct: r.CPUPct, MemUsed: r.MemUsed,
			MemTotal: r.MemTotal, ProcRSS: r.ProcRSS, NumCPU: r.NumCPU,
		})
	}
	return out, nil
}

func (a statsStore) PruneStatsSamples(ctx context.Context, before time.Time) (int64, error) {
	return a.repo.PruneStatsSamples(ctx, before)
}
