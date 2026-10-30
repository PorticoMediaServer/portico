package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
)

var ErrFacetProducerChanged = errors.New("facet producer source image changed")
var ErrFacetProducerFailed = errors.New("facet source maintenance has failed")

// FacetProducerFence identifies one quiescent accepted legacy contribution
// image. SourceEpoch advances at accepting source writes, BEFORE shared fanout;
// ProjectionEpoch advances with bounded reconciliation. Neither is a hash.
// A class adapter must additionally bind its exact policy and published-class
// witness (restricted decade), and recheck live authority independently.
type FacetProducerFence struct {
	SourceEpoch        int64 `json:"sourceEpoch"`
	ProjectionEpoch    int64 `json:"projectionEpoch"`
	ServingGeneration  int64 `json:"servingGeneration"`
	Version            int   `json:"version"`
	VisibilitySequence int64 `json:"visibilitySequence"`
}

// CaptureFacetProducerFence must share the caller's read snapshot. It is a
// real family barrier, including persisted failures and interrupted stages.
// P2-52 screen/artwork queues invalidate target work, not accepted facet source
// facts. Their asynchronous counts/leases do not gate an immutable API3 image;
// actual later catalogue writes advance this source fence in their own turn.
// Live policy/root authority is checked separately before/after composition.
// This does not bless legacy restriction buckets as an exact policy oracle.
func CaptureFacetProducerFence(ctx context.Context, q ReadQuery) (FacetProducerFence, error) {
	var f FacetProducerFence
	var pending, failed bool
	err := q.QueryRowContext(ctx, `SELECT s.source_epoch,s.projection_epoch,g.generation,CASE WHEN g.retaining=1 THEN g.visibility_sequence ELSE COALESCE((SELECT first_pending-1 FROM compact_visibility_source_jobs WHERE first_pending>=0 ORDER BY first_pending LIMIT 1),(SELECT sequence FROM compact_visibility_source_state WHERE id=1)) END,COALESCE((SELECT version FROM catalog_derivations WHERE domain=33),0),
 g.retaining=1
 OR NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=33 AND version=7)
 OR NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=36 AND version=1)
 OR NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=37 AND version=1)
 OR NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=38 AND version=1)
 OR NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=39 AND version=1)
 OR NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=40 AND version=1)
 OR EXISTS(SELECT 1 FROM catalog_derivations WHERE domain IN(18,19,20,25,26,28,31,33,35,36,37,38,39,40,41,42,43,44) AND rebuilding=1)
 OR EXISTS(SELECT 1 FROM catalog_dirty WHERE domain IN(18,19,20,25,26,28,31,33,35,36,37,38,39,40,41,42,43,44))
 OR EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain IN(18,19,20,25,26,28,31,33,35,36,37,38,39,40,41,42,43,44))
 OR EXISTS(SELECT 1 FROM catalog_upstream_sources WHERE domain>=-9223372036854775808)
 OR EXISTS(SELECT 1 FROM catalog_facet_source_fanouts WHERE domain>=-9223372036854775808)
 OR EXISTS(SELECT 1 FROM catalog_facet_work WHERE entity_id>=-9223372036854775808)
 OR EXISTS(SELECT 1 FROM catalog_facet_work_places WHERE entity_id>=-9223372036854775808)
 OR EXISTS(SELECT 1 FROM catalog_facet_stage WHERE entity_id>=-9223372036854775808)
 OR EXISTS(SELECT 1 FROM compact_visibility_source_jobs WHERE first_pending<=(CASE WHEN g.retaining=1 THEN g.visibility_sequence ELSE COALESCE((SELECT first_pending-1 FROM compact_visibility_source_jobs WHERE first_pending>=0 ORDER BY first_pending LIMIT 1),(SELECT sequence FROM compact_visibility_source_state WHERE id=1)) END))
 OR EXISTS(SELECT 1 FROM catalog_browse_row_work WHERE domain IN(18,35))
 OR EXISTS(SELECT 1 FROM catalog_browse_row_keys WHERE domain IN(18,35))
 OR EXISTS(SELECT 1 FROM catalog_item_metric_jobs WHERE item_id>=-9223372036854775808)
 OR EXISTS(SELECT 1 FROM catalog_browse_counter_repairs WHERE entity_id>=-9223372036854775808),
 EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain IN(18,19,20,25,26,28,31,33,35,36,37,38,39,40,41,42,43,44))
 FROM catalog_facet_source_state s CROSS JOIN catalog_generations g WHERE s.singleton=1 AND g.singleton=1`).
		Scan(&f.SourceEpoch, &f.ProjectionEpoch, &f.ServingGeneration, &f.VisibilitySequence, &f.Version, &pending, &failed)
	if err != nil {
		return f, err
	}
	if failed {
		return f, ErrFacetProducerFailed
	}
	if pending {
		return f, ErrBuilding
	}
	return f, nil
}

// ValidateFacetProducerFence runs in the SAME writer transaction as base
// activation. An edit/merge between any two class quanta invalidates the
// candidate; unchanged count totals do not conceal source/classification edits.
func ValidateFacetProducerFence(ctx context.Context, tx *sql.Tx, expected FacetProducerFence) error {
	current, err := CaptureFacetProducerFence(ctx, tx)
	if err != nil {
		return err
	}
	if current != expected {
		return ErrFacetProducerChanged
	}
	return nil
}

// CheckNormalizedFacetReadiness is only the shared producer-family portion of
// the base adapter's barrier. The adapter remains disabled until its distinct
// unrestricted/restricted show/member/decade oracle is supplied by its owner.
// Legacy CheckReadiness and current public readers are unchanged.
func CheckNormalizedFacetReadiness(ctx context.Context, q ReadQuery) error {
	_, err := CaptureFacetProducerFence(ctx, q)
	return err
}
