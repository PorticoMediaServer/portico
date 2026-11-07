package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// SubjectArtwork selects the complete accepted subject recipe for catalogue
// detail, including authors. The original Artwork/default browse behavior stays
// unchanged. Automatic collections require completed preparation instead.
func (r *ForegroundReader) SubjectArtwork(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in ArtworkInput) (ArtworkFact, BrowseReadResult, error) {
	if r == nil || in.Item <= 0 || in.View.Kind < 1 || in.View.Kind > 13 {
		return ArtworkFact{}, BrowseReadResult{}, ErrInput
	}
	in.View.policy = readPolicyBinding{recipe: rsDetailSubjectPolicy, subject: in.Item}
	return r.Artwork(ctx, request, budget, out, in)
}

// Artwork carries exactly the same Reading attribution/format/local-archive
// eligibility as Facts. Trusted converter configuration remains on the reader;
// it is never a request flag and art access never opens a Reading source.
func (r *RSReadingReader) Artwork(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in ArtworkInput) (ArtworkFact, BrowseReadResult, error) {
	if r == nil || in.Item <= 0 || in.View.Kind != 20 && in.View.Kind != 21 {
		return ArtworkFact{}, BrowseReadResult{}, ErrInput
	}
	in.View.policy = readPolicyBinding{recipe: rsReadingNativePolicy, subject: in.Item}
	if r.mobi {
		in.View.policy.recipe = rsReadingMobiPolicy
	}
	art := &ForegroundReader{snapshots: r.snapshots, pool: r.pool}
	return art.Artwork(ctx, request, budget, out, in)
}

// Artwork delivers one collection art fact only after complete member reduction
// proves the root eligible. The same source footprint remains charged through
// its fresh art/source publication; output then owns the copied fact.
func (p *CollectionPreparation) Artwork(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, role string, pageSlot bool) (ArtworkFact, BrowseReadResult, error) {
	if err := p.check(out, request); err != nil {
		return ArtworkFact{}, BrowseReadResult{}, err
	}
	if !p.ready {
		return ArtworkFact{}, BrowseReadResult{}, ErrInput
	}
	in := ArtworkInput{View: p.in.View, Item: p.in.Collection, Role: role, PageSlot: pageSlot}
	in.View.Now = in.View.Now.Add(time.Since(p.started))
	in.View.policy = readPolicyBinding{recipe: rsCollectionDeliveryPolicy, subject: p.in.Collection, state: p}
	if p.lease.Deadline().Before(request.Deadline) {
		request.Deadline = p.lease.Deadline()
	}
	art := &ForegroundReader{snapshots: p.reader.snapshots, pool: p.reader.pool}
	fact, cost, err := art.Artwork(ctx, request, min(budget, time.Until(p.lease.Deadline())), out, in)
	if err == nil {
		err = p.authority.HandoffPreparation(p.lease)
	}
	closeErr := p.Close()
	if err != nil {
		return ArtworkFact{}, cost, err
	}
	if closeErr != nil {
		return ArtworkFact{}, cost, closeErr
	}
	return fact, cost, nil
}
