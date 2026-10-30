package catalog

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// The accepting producers in this package return an explicit receipt instead of
// claiming canonical facts are applied. Two shapes share that contract:
//
//   - acceptInventoryInfo/AcceptInventoryAnalysis/AcceptInventoryLocalRefresh
//     stage one file's technical input. Their naming, artwork and queue
//     acknowledgement finalizer runs only when the receipt is already applied;
//     accepted-pending returns the receipt untouched, and the scan resumes it
//     later without pretending NEW is visible.
//   - AcceptInventoryDiscProbe and AcceptDiscAlternate stage one disc
//     observation's normalized families (10..18). They have no naming or queue
//     finalizer of their own, so their receipt is the whole acknowledgement:
//     durable accepted-pending, then applied by the sole source publication.
//
// No producer in this file writes a canonical technical child row, walks a
// holder or turns an accepted-pending receipt into success.

// AcceptInventoryAnalysis accepts immutable technical inputs before naming and
// queue acknowledgement. Accepted-pending is durable success, not an assertion
// that canonical rows or item summaries are already applied. The scan retries
// its exact observation while source41 performs the bounded publication.
func (s *Service) AcceptInventoryAnalysis(ctx context.Context, job, object, revision string, policyRevision int64, facts assets.Facts) (compactcatalog.InfoAcceptReceipt, error) {
	receipt, err := s.acceptInventoryInfo(ctx, job, object, revision, policyRevision, facts, false)
	if err != nil || receipt.State != compactcatalog.InfoInputApplied {
		return receipt, err
	}
	return receipt, s.commitInventoryAnalysisApplied(ctx, job, object, revision, policyRevision, facts)
}

// AcceptInventoryLocalRefresh has the same explicit receipt contract while
// retaining the refresh's original analysis/source/policy fences.
func (s *Service) AcceptInventoryLocalRefresh(ctx context.Context, job, object, revision string, policyRevision int64, facts assets.Facts) (compactcatalog.InfoAcceptReceipt, error) {
	receipt, err := s.acceptInventoryInfo(ctx, job, object, revision, policyRevision, facts, true)
	if err != nil || receipt.State != compactcatalog.InfoInputApplied {
		return receipt, err
	}
	return receipt, s.commitInventoryLocalRefreshApplied(ctx, job, object, revision, policyRevision, facts)
}
