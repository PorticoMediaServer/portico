package writer

import "context"

// Purpose-specific resource checks and the sole result codec are reused,
// with no local stubs. The sole dispatcher calls these bodies only AFTER
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
	digest, err := canonicalRequestDigest(BasicStage, basicStageRequestVersion, command.identity, command.request)
	if err != nil || digest != command.digest {
		return value, ErrConflict
	}
	var facts basicFreshFacts
	if command.request.operation == basicStageRetireInput {
		facts, err = readBasicRetireLiveFacts(ctx, turn, command.identity, original, source)
	} else {
		facts, err = readBasicFreshFacts(ctx, turn, command.identity, original, source)
	}
	if err != nil {
		return value, err
	}
	appliedMS, err := turn.basicTimeMS(ctx, BasicPurposeStage, original.run.leaseExpiresMS)
	if err != nil {
		return value, err
	}
	changeRevision := command.request.changeRevision
	expectedContinuation := BasicContinuation{}
	switch command.request.operation {
	case basicStageRetireInput:
		if command.request.rows.family != 0 {
			if err = applyBasicRetirePrefix(ctx, turn, command.request); err != nil {
				return value, err
			}
			return completeBasicControl(ctx, turn, command.identity, command.digest, BasicPurposeStage, original, source, facts, appliedMS, BasicContinuation{AfterEntity: command.request.retire.entityID})
		}
		retired, err := retireBasicFirstInput(ctx, turn, command.request.retire)
		if err != nil {
			return value, err
		}
		return completeBasicRetireControl(ctx, turn, command, facts, appliedMS, retired)
	case basicStageCopyInput:
		var position basicCopyPosition
		position, err = applyBasicCopy(ctx, turn, command.request.copy, appliedMS)
		expectedContinuation = basicCopyContinuation(position)
	case basicStageBeginInput:
		var begun basicBegunInput
		begun, err = beginBasicInput(ctx, turn, command.request.begin, appliedMS)
		source = begun.source // actual INSERT RETURNING ID/input revision
		changeRevision = begun.changeRevision
	case basicStageFactInput:
		if err = checkBasicControlChange(ctx, turn, source, command.request.changeRevision); err == nil {
			_, err = stageBasicStreamFact(ctx, turn, source)
		}
	case basicStagePinShapeInput:
		changeRevision = command.request.probe.changeRevision
		if err = checkBasicControlChange(ctx, turn, source, changeRevision); err == nil {
			err = checkBasicPinManifest(ctx, turn, source)
		}
		if err == nil {
			var pinned basicShapeTarget
			pinned, err = pinBasicProbeShape(ctx, turn, command.request.probe)
			changeRevision = pinned.changeRevision
			expectedContinuation.NextFamily = 1
		}
	case basicStageProbeInput:
		changeRevision = command.request.probe.changeRevision
		if err = checkBasicControlChange(ctx, turn, source, changeRevision); err == nil {
			_, err = applyBasicProbeScalars(ctx, turn, command.request.probe)
		}
	case basicStageMediaInput:
		if err = checkBasicControlChange(ctx, turn, source, command.request.changeRevision); err != nil {
			break
		}
		var family *basicMediaFamily
		family, err = prepareBasicRowsRequest(source, command.request.rows)
		if err == nil {
			_, err = stageBasicMediaRows(ctx, turn, family, command.request.start, command.request.end)
		}
	default:
		err = ErrInvalid
	}
	if err != nil {
		return value, err
	}
	// BEGIN's actual allocated revision and every unchanged staging revision
	// are proved before the native continuation can be read into the result.
	if command.request.operation != basicStagePinShapeInput && command.request.operation != basicStageCopyInput {
		if err = checkBasicControlChange(ctx, turn, source, changeRevision); err != nil {
			return value, err
		}
	}
	return completeBasicControl(ctx, turn, command.identity, command.digest, BasicPurposeStage, original, source, facts, appliedMS, expectedContinuation)
}

func applyBasicSeal(ctx context.Context, turn *commandTurn, command *basicSealCommand) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || command == nil || !command.valid || command.request.purpose != BasicPurposeSeal || (command.request.operation != basicSealStreamInput && command.request.operation != basicSealMediaInput) {
		return value, ErrInvalid
	}
	source, original := command.request.source, command.request.original
	required, err := basicSealRequiredBytes(command.request)
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
	if command.request.operation == basicSealStreamInput {
		_, err = sealBasicStreamFact(ctx, turn, source)
	} else {
		var family *basicMediaFamily
		family, err = prepareBasicRowsRequest(source, command.request.rows)
		if err == nil {
			_, err = sealBasicMediaFamily(ctx, turn, family)
		}
	}
	if err != nil {
		return value, err
	}
	return completeBasicControl(ctx, turn, command.identity, command.digest, BasicPurposeSeal, original, source, facts, appliedMS, BasicContinuation{})
}

// No physical finish, COMMIT or Applied assignment here. The sole dispatcher
// rechecks the exact purpose's terminal lease and current authority.
func completeBasicControl(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, purpose BasicPurpose, original basicOriginalScope, source basicStreamFactStage, facts basicFreshFacts, appliedMS int64, expectedContinuation BasicContinuation) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	continuation, err := readBasicControlPostState(ctx, turn, source)
	if err != nil {
		return value, err
	}
	if continuation != expectedContinuation {
		return value, ErrConflict
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
	if in.operation == basicStageCopyInput {
		bytes, err := basicCopyRequestBytes(in.copy)
		if err != nil {
			return 0, err
		}
		if (base-65536)/8+bytes > maxCanonicalRequestBytes {
			return 0, ErrCap
		}
		return base + 8*bytes + 32*uint64(in.copy.expectedRows), nil
	}
	if in.operation == basicStageProbeInput || in.operation == basicStagePinShapeInput {
		remaining := maxCanonicalRequestBytes
		for _, v := range [...]string{in.probe.assetPath, in.probe.before.container, in.probe.before.videoCodec, in.probe.before.audioCodec, in.probe.after.container, in.probe.after.videoCodec, in.probe.after.audioCodec} {
			if len(v) > remaining {
				return 0, ErrCap
			}
			remaining -= len(v)
		}
		extra := uint64(maxCanonicalRequestBytes - remaining)
		if (base-65536)/8+extra > maxCanonicalRequestBytes {
			return 0, ErrCap
		}
		return base + 8*extra, nil
	}
	if in.operation == basicStageRetireInput && in.rows.family != 0 {
		n, err := basicRetirePrefixBytes(source, in.rows, in.retireOrdinals, in.retire.counts[in.rows.family-1])
		if err != nil {
			return 0, err
		}
		if (base-65536)/8+n > maxCanonicalRequestBytes {
			return 0, ErrCap
		}
		return base + 8*n + 256*uint64(len(in.retireOrdinals)), nil
	}
	if in.operation == basicStageMediaInput {
		rowsBytes, err := basicRowsRequestBytes(source, in.rows)
		if err != nil {
			return 0, err
		}
		if (base-65536)/8+rowsBytes > maxCanonicalRequestBytes {
			return 0, ErrCap
		}
		return base + 8*rowsBytes + 256*uint64(basicRowsCount(in.rows)), nil
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

func basicSealRequiredBytes(in basicSealRequest) (uint64, error) {
	base, err := basicControlRequiredBytes(in.original, in.source)
	if err != nil {
		return 0, err
	}
	if in.operation == basicSealMediaInput {
		rows, err := basicRowsRequestBytes(in.source, in.rows)
		if err != nil {
			return 0, err
		}
		if (base-65536)/8+rows > maxCanonicalRequestBytes {
			return 0, ErrCap
		}
		return base + 8*rows + 256*uint64(basicRowsCount(in.rows)), nil
	}
	return base, nil
}
