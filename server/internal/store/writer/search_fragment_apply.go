package writer

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
)

// Exact draft W-private method contract. Only the actual *commandTurn is ever
// asserted to this interface: no caller adapter/callback/constructor or budget
// enters. Missing W implementation refuses fresh work; it cannot become an
// image-purpose fallback. The sole public dispatcher remains W-owned.
type searchPublisherTurnResources interface {
	searchPublisherEngine(context.Context, CommandID) (searchpolicy.Engine, error)
	searchPublisherTimeMS(context.Context, CommandID, int64) (int64, error)
	requireSearchPublisherMemory(context.Context, CommandID, uint64) error
}

func applySearchFragmentStage(ctx context.Context, turn *commandTurn, c *searchFragmentStageCommand) (out searchFragmentStageResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchFragmentStage || c == nil || !c.valid {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	s := c.request.snapshot
	q := PublisherReceiptRequest{JobID: s.job.id, LibraryID: s.source.libraryID, EntityID: s.source.entityID, Receipt: ReceiptReference{c.identity, c.digest}}
	saved, decision, err := lookupSearchFragmentStage(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if decision == receiptReplay {
		return saved, nil // ORIGINAL saved result, no fresh compatibility checks.
	}
	if decision != receiptFresh {
		return out, ErrResolveRequired
	}
	resources, ok := any(turn).(searchPublisherTurnResources)
	if !ok {
		return out, ErrNotImplemented // W draft not landed; ZERO fresh effects.
	}
	required, err := searchFragmentStagePreparationBytes(c.request)
	if err != nil {
		return out, err
	}
	if err := resources.requireSearchPublisherMemory(ctx, SearchFragmentStage, required); err != nil {
		return out, err
	}
	digest, err := canonicalRequestDigest(SearchFragmentStage, 1, c.identity, c.request)
	if err != nil || digest != c.digest {
		return out, ErrConflict
	}
	native, err := searchFragmentStageFreshFacts(ctx, turn, s)
	if err != nil {
		return out, err
	}
	engine, err := resources.searchPublisherEngine(ctx, SearchFragmentStage)
	if err != nil {
		return out, err
	}
	if searchpolicy.Compare(native.bank.profile, engine).State != searchpolicy.Current {
		return out, ErrConflict
	}
	appliedMS, err := resources.searchPublisherTimeMS(ctx, SearchFragmentStage, native.job.leaseExpiresMS)
	if err != nil {
		return out, err
	}
	out, err = writeSearchFragmentStage(ctx, turn, c, native, appliedMS)
	if err != nil {
		return out, err
	}
	// Same W high-water/whole-deadline lease check at the body boundary. W's
	// central checkSearchPublisherTerminal remains an independent terminal
	// requirement; this never replaces the saved acceptance-time sample.
	if _, err := resources.searchPublisherTimeMS(ctx, SearchFragmentStage, native.job.leaseExpiresMS); err != nil {
		return out, err
	}
	return out, nil // W alone unwraps/commits/projects physical Outcome.
}
