package writer

import (
	"context"
	"math"
)

// ONE final transaction on the caller's existing protected BasicAccept turn.
// This body is unwired until complete definitions, per-payload forecasting,
// Capture and current opaque preparation composition are installed together.
func applyBasicAccept(ctx context.Context, turn *commandTurn, command *basicAcceptCommand) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || command == nil || !command.valid || command.request.purpose != BasicPurposeAccept || command.request.operation != basicAcceptFirstOrdinaryLeaf {
		return value, ErrInvalid
	}
	in := command.request
	s := in.activation.source
	required, err := basicAcceptRequiredBytes(in)
	if err != nil {
		return value, err
	}
	if err = turn.requireBasicMemory(ctx, BasicPurposeAccept, required); err != nil {
		return value, err
	}
	digest, err := canonicalRequestDigest(BasicAccept, 1, command.identity, in)
	if err != nil || digest != command.digest {
		return value, ErrConflict
	}
	facts, err := readBasicFreshFacts(ctx, turn, command.identity, in.original, s)
	if err != nil {
		return value, err
	}
	if facts.analyzed == math.MaxInt64 {
		return value, ErrCap
	}
	appliedMS, err := turn.basicTimeMS(ctx, BasicPurposeAccept, in.original.run.leaseExpiresMS)
	if err != nil {
		return value, err
	}
	if in.nfo != nil {
		if _, err = replaceBasicNFOEvidenceForAccept(ctx, turn, in.nfo, appliedMS); err != nil {
			return value, err
		}
	}
	for n := range in.artwork {
		if _, err = queueBasicLocalArtworkForAccept(ctx, turn, &in.artwork[n], appliedMS); err != nil {
			return value, err
		}
	}
	a := in.activation
	activated, err := activateBasicAsset(ctx, turn, a)
	if err != nil {
		return value, err
	}
	if activated.available != 1 {
		return value, ErrConflict
	}
	a.changeRevision = activated.changeRevision
	settled, err := settleBasicFirstLeaf(ctx, turn, basicLeafSettlement{a, in.entityID, in.duration, in.height})
	if err != nil {
		return value, err
	}
	publication := basicLeafPublication{a, in.entityID, in.duration, settled}
	if _, err = dischargeBasicFirstScalarOld(ctx, turn, publication, in.oldScalars); err != nil {
		return value, err
	}
	published, err := publishBasicFirstLeaf(ctx, turn, publication)
	if err != nil {
		return value, err
	}
	if _, err = applyBasicObjectAnalysis(ctx, turn, &in.analysis); err != nil {
		return value, err
	}
	continuation, err := readBasicControlPostState(ctx, turn, s)
	if err != nil {
		return value, err
	}
	if continuation != (BasicContinuation{AfterEntity: in.entityID}) {
		return value, ErrConflict
	}
	runRevision, err := advanceBasicAcceptedRun(ctx, turn, command.identity, in.original, facts)
	if err != nil {
		return value, err
	}
	value = BasicResult{Purpose: BasicPurposeAccept, RunID: in.original.scope.runID, AcceptedRunRevision: runRevision, AppliedMS: appliedMS, SourceID: s.sourceID, SourceIncarnation: s.incarnation, SourceRevision: s.objectRevision, ChangeID: s.changeID, ExpectedEpoch: s.expectedEpoch, Continuation: continuation, ResultCount: 1, Members: []InventoryMember{{InputOrdinal: 0, ChildKind: 0, ChildOrdinal: 0, ObjectID: s.objectID, LibraryID: s.libraryID, SubjectID: published.entityID, AssetID: published.assetID, AcceptedAssetRevision: published.infoEpoch}}}
	value.Digest, err = basicResultDigest(command.identity, command.digest, value)
	if err != nil {
		return value, err
	}
	if err = writeBasicAcceptReceipt(ctx, turn, command.identity, command.digest, value); err != nil {
		return value, err
	}
	return value, nil
}
