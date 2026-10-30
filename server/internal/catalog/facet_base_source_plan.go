package catalog

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// FacetBasePlan is a background BASE demand, never a query descriptor/job. The
// owner captures it in one read/writer snapshot, supplies live Authority to
// Begin, then uses Producer in bounded writer Step calls. It must roll back a
// failed quantum and retire/restart a candidate whose source fence changed.
type FacetBasePlan struct {
	Scope    FacetBaseScope
	Witness  string
	Producer FacetBaseProducer
}

// CaptureSimpleFacetBasePlan is the single branch selector for the new worker.
// It mirrors SIMPLE's meaningful unrestricted/show/member/decade distinctions,
// while typed exact policy owns every shared class key. Reading/contextual/
// people dispatch stays with the existing public route before this call.
//
// The linked-decade adapter produces a fresh accepted COMPLETE basis using the
// published-class predicate; it does not claim a legacy mutable generation was
// a coherent historical snapshot. Refresh accepts this newly disclosed basis;
// existing complete base inputs survive content-only churn independently.
func CaptureSimpleFacetBasePlan(ctx context.Context, q compactcatalog.ReadQuery, libraryID int64, library, field string, r identity.ContentRestrictions) (FacetBasePlan, error) {
	plan := FacetBasePlan{Scope: FacetBaseScope{LibraryID: libraryID, Library: library, Field: field, Policy: FacetBasePolicy(r)}}
	var err error
	if !r.Active() {
		plan.Scope.Source = UnrestrictedFacetBaseSource
		plan.Producer = UnrestrictedFacetBaseProducer{}
		plan.Witness, err = CaptureUnrestrictedFacetBaseWitness(ctx, q, plan.Scope)
		return plan, err
	}
	if field == "decade" {
		if err = rejectPublishedFacetDecade(ctx, q, plan.Scope, r); errors.Is(err, ErrFacetBaseUnavailable) {
			plan.Scope.Source = LinkedDecadeFacetBaseSource
			plan.Producer = LinkedDecadeFacetBaseProducer{}
			plan.Witness, err = CaptureLinkedDecadeFacetBaseWitness(ctx, q, plan.Scope)
			return plan, err
		} else if err != nil {
			return plan, err
		}
	}
	var kind string
	if err = q.QueryRowContext(ctx, `SELECT l.kind FROM catalog_libraries c JOIN libraries l ON l.id=c.library_id WHERE c.id=? AND c.library_id=? AND c.retired=0`, libraryID, library).Scan(&kind); err != nil {
		return plan, err
	}
	plan.Scope.Source = RestrictedMemberFacetBaseSource
	if kind == "tv" || kind == "anime" {
		plan.Scope.Source = RestrictedShowFacetBaseSource
	}
	plan.Producer = RestrictedFacetBaseProducer{}
	plan.Witness, err = CaptureRestrictedFacetBaseWitness(ctx, q, plan.Scope)
	return plan, err
}
