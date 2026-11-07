package writer

import "context"

// W-owned resource/codec symbols are draft dependencies, deliberately not
// replaced with local stubs. The sole dispatcher calls these bodies only AFTER
// current disclosure and original historical resolution. Outcome stays zero.
func applyBasicStage(ctx context.Context, turn *commandTurn, command *basicStageCommand) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || command == nil || !command.valid {
		return value, ErrInvalid
	}
	source, err := basicStageSource(command.request)
	if err != nil {
		return value, err
	}
	original := command.request.original
	required, err := basicStageRequiredBytes(command.request)
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
	changeRevision := command.request.changeRevision
	switch command.request.operation {
	case basicStageBeginInput:
		var begun basicBegunInput
		begun, err = beginBasicInput(ctx, turn, command.request.begin, appliedMS)
		source = begun.source // actual INSERT RETURNING ID/input revision
		changeRevision = begun.changeRevision
	case basicStageFactInput:
		if err = checkBasicControlChange(ctx, turn, source, command.request.changeRevision); err == nil {
			_, err = stageBasicStreamFact(ctx, turn, source)
		}
	default:
		err = ErrInvalid
	}
	if err != nil {
		return value, err
	}
	// BEGIN's actual allocated revision and every unchanged staging revision
	// are proved before the native continuation can be read into the result.
	if err = checkBasicControlChange(ctx, turn, source, changeRevision); err != nil {
		return value, err
	}
	return completeBasicControl(ctx, turn, command.identity, command.digest, BasicPurposeStage, original, source, facts, appliedMS)
}

func applyBasicSeal(ctx context.Context, turn *commandTurn, command *basicSealCommand) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || command == nil || !command.valid || command.request.purpose != BasicPurposeSeal || command.request.operation != basicSealStreamInput {
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

// No physical finish, COMMIT or Applied assignment here. W rechecks the exact
// purpose's terminal lease/current authority and owns the physical terminal.
func completeBasicControl(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, purpose BasicPurpose, original basicOriginalScope, source basicStreamFactStage, facts basicFreshFacts, appliedMS int64) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	continuation, err := readBasicControlPostState(ctx, turn, source)
	if err != nil {
		return value, err
	}
	runRevision, err := advanceBasicControlRun(ctx, turn, identity, original, facts)
	if err != nil {
		return value, err
	}
	value = BasicResult{Purpose: purpose, RunID: original.scope.runID, AcceptedRunRevision: runRevision, AppliedMS: appliedMS,
		SourceID: source.sourceID, SourceIncarnation: source.incarnation, SourceRevision: source.objectRevision,
		ChangeID: source.changeID, ExpectedEpoch: source.expectedEpoch, Continuation: continuation, Members: []InventoryMember{}}
	value.Digest, err = basicResultDigest(identity, request, value)
	if err != nil {
		return value, err
	}
	if err = writeBasicControlReceipt(ctx, turn, identity, request, value); err != nil {
		return value, err
	}
	return value, nil
}

// Conservative preparation envelope for these three scalar/header-only
// operations: original/frozen values, repeated native binds/echoes and codec
// scratch. This is not a selected grant/quota or a physical-fit measurement.
// Other payload families require their own complete prospective envelope.
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

// Include the complete frozen manifest and its bounded validation maps/slices;
// the source-only envelope is insufficient for BEGIN's authored union/streams.
func basicStageRequiredBytes(in basicStageRequest) (uint64, error) {
	source, err := basicStageSource(in)
	if err != nil {
		return 0, err
	}
	base, err := basicControlRequiredBytes(in.original, source)
	if err != nil {
		return 0, err
	}
	if in.operation != basicStageBeginInput {
		return base, nil
	}
	s := in.begin.shape
	if len(s.CUEOrdinals) > MaxCUEChildren || len(s.EpisodeIDs) > MaxEpisodes || len(s.VersionAssetIDs) > MaxVersions || len(s.Streams) > 3*MaxStreamsPerClass || len(s.AuthoredParentIDs) > MaxAuthoredParents {
		return 0, ErrCap
	}
	// 128 bytes per member covers simultaneously retained value, native
	// projection, validation-map entry and codec framing; no native reserve
	// is borrowed. This prospective model still needs W profile admission.
	return base + 128*uint64(len(s.CUEOrdinals)+len(s.EpisodeIDs)+len(s.VersionAssetIDs)+len(s.Streams)+len(s.AuthoredParentIDs)), nil
}
