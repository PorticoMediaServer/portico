package writer

import (
	"context"
)

// W's sole dispatcher establishes current disclosure authority in this same
// turn BEFORE calling this private entry. It owns finite composition, production
// recipe, terminal/ambiguity/physical release and public outcome mapping. No
// current source/lease/policy check may hide the ORIGINAL historical receipt.
// Production dispatch additionally waits for the actual typed source-stage/seal
// operation/receipt/frozen-request proof. The sealed source rows checked here
// fence captured native intent; flags and supplied digests do not prove the
// evidence-to-source link or a verified image. No public dispatcher exists here.
func applyEffectiveStageBegin(ctx context.Context, turn *commandTurn, input *effectiveStageBeginCommand) (out EffectiveStageBeginResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	var zero EffectiveStageBeginResult
	if turn == nil || input == nil || !input.valid || turn.transaction == nil || turn.transaction.descriptor.ID != EffectiveStage {
		return zero, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	r := input.request
	result, owner, decision, err := lookupEffectiveStageBegin(ctx, turn, r.ownerID, receiptIdentity{input.identity, input.digest})
	if err != nil {
		return zero, err
	}
	if decision == receiptReplay {
		return result, nil
	}
	if decision != receiptFresh {
		return zero, ErrResolveRequired
	}
	if input.sourceReferences != nil {
		// V2 data must never fall through the legacy effects path: its actual
		// image/receipt dependency pins need the separately verified source arm.
		return zero, &Refusal{Code: RefusalUnqualified}
	}
	if owner != r.owner {
		return zero, ErrConflict
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	if !now.Before(input.leaseExpiry) {
		return zero, ErrConflict
	}
	entity, err := effectiveBeginEntityFacts(ctx, turn, turn.steps[enrichmentEffectiveEntity].statementStep, r.entity.id)
	if err != nil {
		return zero, err
	}
	if entity != r.entity {
		return zero, ErrConflict
	}
	producer, err := effectiveBeginReadProducer(ctx, turn, r.ownerID, entity, owner.provider, r.evidenceID, r.publicationID, r.objectID)
	if err != nil {
		return zero, err
	}
	if producer != r.producer || !effectiveBeginProducerShape(producer, entity, owner, r.header) {
		return zero, ErrConflict
	}
	now, err = turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	image, err := effectiveBeginInsertHiddenHeader(ctx, turn, turn.steps[enrichmentEffectiveInsert].statementStep, r.header, input.digest, input.retirementIncarnation)
	if err != nil {
		return zero, err
	}
	if err := effectiveBeginAttachHiddenHeader(ctx, turn, turn.steps[enrichmentEffectiveAttach].statementStep, r.ownerID, owner, input.identity, image); err != nil {
		return zero, err
	}
	result = EffectiveStageBeginResult{ProviderOperationID: r.ownerID, EntityID: entity.id, ObservedCatalogRevision: entity.revision, AppliedMS: appliedMS, AfterItem: owner.afterItem, NextOrdinal: 0, ImageID: image.id, ImageRevision: image.revision}
	digest, err := effectiveBeginResultDigest(input.identity, input.digest, effectiveBeginResult{ProviderOperationID: result.ProviderOperationID, EntityID: result.EntityID, ObservedCatalogRevision: result.ObservedCatalogRevision, AppliedMS: result.AppliedMS, AfterItem: result.AfterItem, NextOrdinal: 0, ImageID: image.id, ImageRevision: image.revision, ResultCount: 0, MemberCohortLength: 0})
	if err != nil {
		return zero, err
	}
	written, err := turn.exec(ctx, turn.steps[enrichmentEffectiveRecord].statementStep, r.ownerID, input.identity.Incarnation[:], input.identity.Sequence, input.identity.OperationID[:], input.digest[:], "effective_stage", entity.revision, appliedMS, entity.id, owner.afterItem, int64(0), nil, image.id, image.revision, int64(0), digest[:])
	if err != nil {
		return zero, err
	}
	count, err := written.RowsAffected()
	if err != nil {
		return zero, err
	}
	if count != 1 {
		return zero, ErrResolveRequired
	}
	result.Digest = digest
	return result, nil
}

// Called only after W current disclosure authority. Historical resolution needs
// original identity/digest, not reconstruction/refetch of a lost fresh packet.
func lookupEffectiveStageBegin(ctx context.Context, turn *commandTurn, ownerID string, expected receiptIdentity) (out EffectiveStageBeginResult, outOwner effectiveBeginOwner, outDecision receiptDecision, err error) {
	defer func() {
		checked, finalErr := finishCommand(ctx, commandLifetime(turn), effectiveLookupCompletion{out, outOwner, outDecision}, err)
		out, outOwner, outDecision, err = checked.result, checked.owner, checked.decision, finalErr
	}()
	var zero EffectiveStageBeginResult
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != EffectiveStage {
		return zero, effectiveBeginOwner{}, 0, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	owner, err := effectiveBeginOwnerFacts(ctx, turn, turn.steps[enrichmentEffectiveOwner].statementStep, ownerID)
	if err != nil {
		return zero, owner, 0, err
	}
	saved, err := effectiveBeginReceiptFacts(ctx, turn, turn.steps[enrichmentEffectiveReceipt].statementStep, ownerID, expected.command)
	if err != nil {
		return zero, owner, 0, err
	}
	facts := receiptFacts{exists: saved.exists, identity: saved.identity, resultCount: saved.resultCount, resultDigest: saved.resultDigest}
	if saved.kind == "effective_stage" {
		facts.kind = EffectiveStage
	}
	decision, err := classifyReceipt(expected, EffectiveStage, owner.receiptOwnerFacts, facts)
	if err != nil {
		return zero, owner, decision, err
	}
	switch decision {
	case receiptFresh:
		return zero, owner, decision, nil
	case receiptRetired:
		return zero, owner, decision, nil
	case receiptResolveRequired:
		return zero, owner, decision, ErrResolveRequired
	case receiptConflict:
		return zero, owner, decision, ErrConflict
	case receiptReplay:
	default:
		return zero, owner, decision, ErrResolveRequired
	}
	if !saved.imageID.Valid || !saved.imageRevision.Valid || saved.publicationID.Valid || saved.entityID != owner.entityID {
		return zero, owner, decision, ErrResolveRequired
	}
	result := EffectiveStageBeginResult{ProviderOperationID: ownerID, EntityID: saved.entityID, ObservedCatalogRevision: saved.acceptedRevision, AppliedMS: saved.appliedMS, AfterItem: saved.afterItem, NextOrdinal: saved.nextOrdinal, ImageID: saved.imageID.Int64, ImageRevision: saved.imageRevision.Int64}
	digest, err := effectiveBeginResultDigest(expected.command, expected.digest, effectiveBeginResult{ProviderOperationID: ownerID, EntityID: result.EntityID, ObservedCatalogRevision: result.ObservedCatalogRevision, AppliedMS: result.AppliedMS, AfterItem: result.AfterItem, NextOrdinal: result.NextOrdinal, ImageID: result.ImageID, ImageRevision: result.ImageRevision, ResultCount: saved.resultCount, MemberCohortLength: 0})
	if err != nil || [32]byte(digest) != saved.resultDigest {
		return zero, owner, decision, ErrResolveRequired
	}
	result.Digest = digest
	return result, owner, decision, nil
}

type effectiveLookupCompletion struct {
	result   EffectiveStageBeginResult
	owner    effectiveBeginOwner
	decision receiptDecision
}
