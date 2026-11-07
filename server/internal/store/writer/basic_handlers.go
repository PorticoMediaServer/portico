package writer

import "context"

// Private one-fact effects behind the sole dispatcher. Original history and
// current disclosure precede this function; the body never publishes Outcome.
func applyBasicStage(ctx context.Context, turn *commandTurn, command *basicStageCommand) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || command == nil || !command.valid {
		return value, ErrInvalid
	}
	source, err := basicStageSource(command.request)
	if err != nil {
		return value, err
	}
	original := command.request.original
	required, err := basicControlRequiredBytes(original, source)
	if err != nil {
		return value, err
	}
	if err = turn.requireBasicMemory(ctx, BasicPurposeStage, required); err != nil {
		return value, err
	}
	digest, err := canonicalRequestDigest(BasicStage, 1, command.identity, command.request)
	if err != nil || digest != command.digest {
		return value, ErrConflict
	}
	facts, err := readBasicFreshFacts(ctx, turn, command.identity, original, source)
	if err != nil {
		return value, err
	}
	appliedMS, err := turn.basicTimeMS(ctx, BasicPurposeStage, original.run.leaseExpiresMS)
	if err != nil {
		return value, err
	}
	if err = checkBasicControlChange(ctx, turn, source, command.request.changeRevision); err != nil {
		return value, err
	}
	if _, err = stageBasicStreamFact(ctx, turn, source); err != nil {
		return value, err
	}
	if err = checkBasicControlChange(ctx, turn, source, command.request.changeRevision); err != nil {
		return value, err
	}
	return completeBasicControl(ctx, turn, command.identity, command.digest, BasicPurposeStage, original, source, facts, appliedMS)
}
func applyBasicSeal(ctx context.Context, turn *commandTurn, command *basicSealCommand) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || command == nil || !command.valid || command.request.purpose != BasicPurposeSeal || command.request.operation != basicSealStreamInput {
		return value, ErrInvalid
	}
	source, original := command.request.source, command.request.original
	required, err := basicControlRequiredBytes(original, source)
	if err != nil {
		return value, err
	}
	if err = turn.requireBasicMemory(ctx, BasicPurposeSeal, required); err != nil {
		return value, err
	}
	digest, err := canonicalRequestDigest(BasicSeal, 1, command.identity, command.request)
	if err != nil || digest != command.digest {
		return value, ErrConflict
	}
	facts, err := readBasicFreshFacts(ctx, turn, command.identity, original, source)
	if err != nil {
		return value, err
	}
	appliedMS, err := turn.basicTimeMS(ctx, BasicPurposeSeal, original.run.leaseExpiresMS)
	if err != nil {
		return value, err
	}
	if err = checkBasicControlChange(ctx, turn, source, command.request.changeRevision); err != nil {
		return value, err
	}
	if _, err = sealBasicStreamFact(ctx, turn, source); err != nil {
		return value, err
	}
	return completeBasicControl(ctx, turn, command.identity, command.digest, BasicPurposeSeal, original, source, facts, appliedMS)
}
func completeBasicControl(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, purpose BasicPurpose, original basicOriginalScope, source basicStreamFactStage, facts basicFreshFacts, appliedMS int64) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	continuation, err := readBasicControlPostState(ctx, turn, source)
	if err != nil {
		return value, err
	}
	if continuation != (BasicContinuation{}) {
		return value, ErrConflict
	}
	revision, err := advanceBasicControlRun(ctx, turn, identity, original, facts)
	if err != nil {
		return value, err
	}
	value = BasicResult{Purpose: purpose, RunID: original.scope.runID, AcceptedRunRevision: revision, AppliedMS: appliedMS, SourceID: source.sourceID, SourceIncarnation: source.incarnation, SourceRevision: source.objectRevision, ChangeID: source.changeID, ExpectedEpoch: source.expectedEpoch, Continuation: continuation, Members: []InventoryMember{}}
	value.Digest, err = basicResultDigest(identity, request, value)
	if err != nil {
		return value, err
	}
	if err = writeBasicControlReceipt(ctx, turn, identity, request, value); err != nil {
		return value, err
	}
	return value, nil
}
func basicControlRequiredBytes(original basicOriginalScope, source basicStreamFactStage) (uint64, error) {
	payload := uint64(len(original.source.resolvedRoot))
	if payload > maxCanonicalRequestBytes {
		return 0, ErrCap
	}
	for _, text := range [...]string{original.scope.runID, original.scope.libraryKey, original.source.id, original.source.incarnation, original.source.rootIdentity,
		original.run.phase, original.run.scanMode, source.assetToken, source.runID, source.sourceID, source.incarnation, source.objectID, source.objectRevision, source.fingerprint} {
		if uint64(len(text)) > maxCanonicalRequestBytes-payload {
			return 0, ErrCap
		}
		payload += uint64(len(text))
	}
	return 65536 + 8*payload, nil
}
