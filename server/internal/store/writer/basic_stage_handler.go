package writer

import (
	"context"
	"math"
)

// ONE Stage or Seal effect on the caller's existing protected Basic turn. The
// result is a header-only control receipt: ResultCount 0, empty non-nil
// Members and the actual native continuation; no readiness or activation.
func applyBasicStage(ctx context.Context, turn *commandTurn, command *basicStageCommand) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if command == nil || !command.valid {
		return value, ErrInvalid
	}
	return applyBasicControl(ctx, turn, BasicStage, command.identity, command.digest, command.request)
}

func applyBasicSeal(ctx context.Context, turn *commandTurn, command *basicSealCommand) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if command == nil || !command.valid {
		return value, ErrInvalid
	}
	return applyBasicControl(ctx, turn, BasicSeal, command.identity, command.digest, command.request)
}

func applyBasicControl(ctx context.Context, turn *commandTurn, kind CommandID, identity CommandIdentity, digest RequestDigest, in basicControlRequest) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	purpose, err := basicPurposeForCommand(kind)
	if err != nil || ctx == nil || turn == nil || turn.transaction == nil || (kind != BasicStage && kind != BasicSeal) || turn.transaction.descriptor.ID != kind {
		return value, ErrInvalid
	}
	if err = validateBasicControlRequest(identity, in, purpose); err != nil {
		return value, err
	}
	required, err := basicControlRequiredBytes(in)
	if err != nil {
		return value, err
	}
	if err = turn.requireBasicMemory(ctx, purpose, required); err != nil {
		return value, err
	}
	again, err := canonicalRequestDigest(kind, 1, identity, in)
	if err != nil || again != digest {
		return value, ErrConflict
	}
	s := in.source
	facts, err := readBasicFreshFacts(ctx, turn, identity, in.original, s)
	if err != nil {
		return value, err
	}
	appliedMS, err := turn.basicTimeMS(ctx, purpose, in.original.run.leaseExpiresMS)
	if err != nil {
		return value, err
	}
	removed := false
	if in.operation == basicControlAbandon {
		removed, err = abandonBasicInput(ctx, turn, in)
	} else if in.operation == basicControlResume {
		err = resumeBasicCopy(ctx, turn, in, appliedMS)
	} else {
		err = runBasicControlBody(ctx, turn, purpose, in)
	}
	if err != nil {
		return value, err
	}
	// An abandon reports only "call again" or, once the header is gone, the zero
	// continuation: the marked header's raw cursor has no result encoding.
	var continuation BasicContinuation
	switch {
	case removed:
	case in.operation == basicControlAbandon:
		continuation = BasicContinuation{NextFamily: 1}
	default:
		if continuation, err = readBasicControlPostState(ctx, turn, s); err != nil {
			return value, err
		}
	}
	if facts.revision == math.MaxInt64 {
		return value, ErrCap
	}
	runRevision, err := advanceBasicControlRun(ctx, turn, identity, in.original, facts)
	if err != nil {
		return value, err
	}
	value = BasicResult{Purpose: purpose, RunID: in.original.scope.runID, AcceptedRunRevision: runRevision, AppliedMS: appliedMS,
		SourceID: s.sourceID, SourceIncarnation: s.incarnation, SourceRevision: s.objectRevision, ChangeID: s.changeID,
		ExpectedEpoch: s.expectedEpoch, Continuation: continuation, ResultCount: 0, Members: []InventoryMember{}}
	value.Digest, err = basicResultDigest(identity, digest, value)
	if err != nil {
		return value, err
	}
	if err = writeBasicControlReceipt(ctx, turn, identity, digest, value); err != nil {
		return value, err
	}
	return value, nil
}

// The existing bounded family bodies; each verifies its own exact native rows.
func runBasicControlBody(ctx context.Context, turn *commandTurn, purpose BasicPurpose, in basicControlRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	switch {
	case in.operation == basicControlStreamFact && purpose == BasicPurposeStage:
		_, err = stageBasicStreamFact(ctx, turn, in.source)
	case in.operation == basicControlStreamFact && purpose == BasicPurposeSeal:
		_, err = sealBasicStreamFact(ctx, turn, in.source)
	case in.operation == basicControlMediaRows:
		family, e := in.mediaFamily()
		if e != nil {
			return e
		}
		if purpose == BasicPurposeStage {
			_, err = stageBasicMediaRows(ctx, turn, family, in.start, in.end)
		} else {
			_, err = sealBasicMediaFamily(ctx, turn, family)
		}
	case in.operation == basicControlProbeScalars:
		_, err = applyBasicProbeScalars(ctx, turn, *in.probe)
	case in.operation == basicControlPinShape:
		_, err = pinBasicProbeShape(ctx, turn, *in.probe)
	case in.operation == basicControlFreeze:
		err = freezeBasicFirstInput(ctx, turn, in)
	case in.operation == basicControlSelectEntity:
		err = selectBasicFirstEntity(ctx, turn, in)
	case in.operation == basicControlCopyFamily:
		err = copyBasicFamily(ctx, turn, in)
	default:
		err = ErrInvalid
	}
	return err
}

// One copy turn of exactly one family. Each family registers its body from
// its own file (registerBasicCopyFamily); an unregistered family refuses.
func copyBasicFamily(ctx context.Context, turn *commandTurn, in basicControlRequest) error {
	spec, ok := basicCopyFamilies[in.family]
	if !ok {
		return &Refusal{Code: RefusalUnqualified}
	}
	return spec.copy(ctx, turn, basicCopyTurn{family: in.family, source: in.source, changeRevision: in.changeRevision, entityID: in.entityID, shapeID: in.shapeID, entry: in.copy})
}
