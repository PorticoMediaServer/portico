package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// A private one-bucket reduction keeps whole eligible counts distinct from the
// public year/decade facet API and from its nonmissing-value memberships.
const rbExactEligibleCount ExactNumericField = 3

type EligibleCountPreparation struct{ numeric *NumericFacetPreparation }
type EligibleCount struct{ Count int64 }

// BeginEligibleCount prepares the exact eligible-entity count for one current
// default library/kind scope. It accepts no filter or group recipe and makes no
// new universal count promise for browse. Missing-year entities still count.
// It shares the existing governor lease, fixed cache, quantum and fresh-handoff
// protocol; no total is returned until genuine raw EOF and authorization.
func (r *ExactFacetReader) BeginEligibleCount(ctx context.Context, owner governor.OwnerID, bytes int64, in BrowseInput) (*EligibleCountPreparation, error) {
	if in.Descending || in.KeepArticles {
		return nil, ErrInput
	}
	p, err := r.beginNumericPreparation(ctx, owner, bytes, in, rbExactEligibleCount)
	if err != nil {
		return nil, err
	}
	return &EligibleCountPreparation{numeric: p}, nil
}

func (p *EligibleCountPreparation) Advance(ctx context.Context, request governor.Request, budget time.Duration) (FacetAdvance, error) {
	if p == nil || p.numeric == nil {
		return FacetAdvance{}, ErrInput
	}
	return p.numeric.Advance(ctx, request, budget)
}

// Count requires LIVE/output custody and a fresh matching viewer/library fence.
// A not-ready, stale, denied or expired preparation returns zero plus an error.
func (p *EligibleCountPreparation) Count(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput) (EligibleCount, BrowseReadResult, error) {
	if p == nil || p.numeric == nil {
		return EligibleCount{}, BrowseReadResult{}, ErrInput
	}
	page, cost, err := p.numeric.Page(ctx, request, budget, output, current, nil, 1)
	if err != nil {
		return EligibleCount{}, cost, err
	}
	return EligibleCount{Count: page.ValueMemberships}, cost, nil
}

func (p *EligibleCountPreparation) Close() error {
	if p == nil || p.numeric == nil {
		return ErrInput
	}
	return p.numeric.Close()
}
