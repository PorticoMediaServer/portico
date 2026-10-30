package catalog

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A cold-hold slice may read a larger raw prefix while retaining the ordinary
// eight-fact/256KiB output and writer plans. This budget exists for one writer
// transaction only, and never retains source rows or query state between turns.
type facetColdSourceBudget struct {
	raw, bytes, used int
	started          time.Time
	tx               *sql.Tx
	fence            compactcatalog.FacetProducerFence
}
type facetColdSourceKey struct{}

func facetColdSource(ctx context.Context) *facetColdSourceBudget {
	b, _ := ctx.Value(facetColdSourceKey{}).(*facetColdSourceBudget)
	return b
}
func (b *facetColdSourceBudget) room() bool {
	return b.raw > 0 && b.bytes > 0 && (b.used == 0 || time.Since(b.started) < 2*time.Millisecond && dbwork.WriteGate().Admission().UserQueued == 0)
}
func chargeFacetColdSource(ctx context.Context, width int) bool {
	b := facetColdSource(ctx)
	if b == nil {
		return true
	}
	if b.raw <= 0 || b.bytes <= 0 {
		return false
	}
	if b.used > 0 && width > b.bytes {
		return false
	}
	b.raw--
	b.used++
	b.bytes = max(0, b.bytes-width)
	return true
}
func validateMemberFacetFence(ctx context.Context, tx *sql.Tx, f compactcatalog.FacetProducerFence) error {
	// The owner checked this exact fence at the start of this same writer
	// transaction. Only BASE writes follow; they cannot change source inputs.
	if b := facetColdSource(ctx); b != nil && b.tx == tx && b.fence == f {
		return nil
	}
	return compactcatalog.ValidateFacetProducerFence(ctx, tx, f)
}
