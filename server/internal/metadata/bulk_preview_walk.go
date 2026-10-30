package metadata

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// bulkPreviewBatchTime is the cooperative slice for draining one emitted match
// window. It mirrors catalog's unexported selectionBatchTime (10ms) and never
// widens it: the walker and this reducer share the same budgeted quantum, and
// the slice is observed only between whole targets.
const bulkPreviewBatchTime = 10 * time.Millisecond

// stepWalk advances the existing exact predicate walker, then reduces the
// walker's already bounded emitted match window in one short snapshot. The fixed
// match window is not a query membership cache, and an id is released only after
// its window slice has passed every fence.
func (w *bulkPreviewComputation) stepWalk(ctx context.Context, budget *readprep.Budget) error {
	r := w.state
	if r.exhausted {
		return r.step(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia))
	}
	if w.matchNext < w.matchCount {
		return w.drainMatchWindow(ctx)
	}
	if w.walk.Complete() {
		r.exhausted = true
		return r.step(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia))
	}
	// The previous state remains live while the shared walker clones it. The
	// native scalar evaluator reserves its complete peak before touching text.
	clone := w.walk.Bytes()
	w.state.transient = clone + catalog.SelectionBatchMaximumBytes + 65536
	if err := budget.Resize(w.AccountedBytes()); err != nil {
		return err
	}
	witness := func(read *catalog.Service) (string, error) {
		_, _, err := r.read(read.Context(), true)
		if err != nil {
			return "", err
		}
		// Source already checked the complete held basis, authority and metadata
		// revisions. The walk additionally compares its original selection proof.
		return w.walk.Basis(), nil
	}
	next, work, err := w.walk.Step(ctx, witness, func(n int64) error {
		w.state.transient = clone + max(int64(catalog.SelectionBatchMaximumBytes), n) + 65536
		return budget.Resize(w.AccountedBytes())
	})
	if err != nil {
		return err
	}
	w.walk, w.matches, w.matchCount, w.matchNext = next, work.Matches, work.MatchCount, 0
	return nil
}

func (w *bulkPreviewComputation) drainMatchWindow(ctx context.Context) error {
	return w.drainMatchWindowUntil(ctx, time.Now().Add(bulkPreviewBatchTime))
}

// drainMatchWindowUntil reduces pending matches inside one ordinary background
// snapshot, which the shared lifetime already bounds (500ms). The id set is the
// walker's fixed emitted window: at most the existing 64-candidate matcher row
// ceiling, never a fresh source query or an unbounded scan. The one pre-fence and
// one post-fence read of the complete basis replace a fence per target, so the
// live scope/authority re-resolution cost no longer scales with the match count.
//
// Exactly one ephemeral page exists at a time. Its retained charge is reserved
// through addPage before the reducer grows, no page or result slice is retained,
// and the window position is committed only after the snapshot and the outer
// authority generation check pass. Any refusal returns an error with no step
// payload; readprep terminalises that computation without delivering the staged
// reducer state, so a late refusal can never surface as ready or partial data.
func (w *bulkPreviewComputation) drainMatchWindowUntil(ctx context.Context, deadline time.Time) error {
	r := w.state
	next := w.matchNext
	err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), r.s.db, func(inner context.Context) error {
		if _, _, err := r.read(inner, true); err != nil {
			return err
		}
		for next < w.matchCount {
			// One target per iteration: the page and its observations die with
			// this loop body and are never accumulated into a slice.
			id := w.matches[next]
			page, err := r.s.bulkPreviewPageTx(inner, dbwork.Snapshot(inner), catalog.BulkMetadataSelection{SQL: "SELECT ? AS id", Args: []any{id}, Revision: r.revision})
			if err != nil {
				return err
			}
			if page.preview.Total != 1 {
				return catalog.ErrStaleContinuation
			}
			if err = r.addPage(inner, page); err != nil {
				return err
			}
			next++
			// Cancellation and the cooperative slice are checked only once a
			// target is indivisibly reduced, so neither can split a target or
			// starve the window.
			if err = inner.Err(); err != nil {
				return err
			}
			if !time.Now().Before(deadline) {
				break
			}
		}
		_, _, err := r.read(inner, true)
		return err
	})
	if err != nil {
		return err
	}
	// The commit point: a post-snapshot authority change refuses the slice and
	// leaves the window exactly as it was.
	if dbwork.AuthorityGeneration() != r.authority {
		return catalog.ErrStaleContinuation
	}
	for i := w.matchNext; i < next; i++ {
		w.matches[i] = 0
	}
	w.matchNext = next
	return nil
}
