package writer

import "context"

// This proves only the frozen original's actual E/S seal lineage, on this exact
// live turn. It grants no current disclosure, lease or fresh effect permission,
// and it cannot be retained as a stage witness on a later turn or replacement.
// Those common gates remain W's dispatcher's responsibility.
type effectiveVerifiedSource struct {
	self     *effectiveVerifiedSource
	turn     *commandTurn
	tx       *transaction
	input    *effectiveStageBeginCommand
	digest   RequestDigest
	evidence effectivePinnedSealReceipt
	source   effectivePinnedSealReceipt
}

// No fixture permit, supplied digest or saved Capture tuple can construct this
// private proof. Both native sealed body pins and both immutable original seal
// receipts are read through the protected frame and the ONE saved-result codec.
// ACKed pinned dependencies are private proof data, never resurrected Applied.
func verifyEffectiveSource(ctx context.Context, turn *commandTurn, input *effectiveStageBeginCommand) (out *effectiveVerifiedSource, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != EffectiveStage {
		return nil, ErrInvalid
	}
	return verifyNativeEffectiveSource(ctx, turn, input)
}

// One native reader/verifier, shared only by the two closed purpose wrappers.
// It proves immutable source lineage, not a fresh effect or current authority.
func verifyNativeEffectiveSource(ctx context.Context, turn *commandTurn, input *effectiveStageBeginCommand) (out *effectiveVerifiedSource, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = requireEffectiveSourceGrantKey(turn, input); err != nil {
		return nil, err
	}
	coverage, err := preflightEffectiveSourcePins(ctx, turn, input)
	if err != nil {
		return nil, err
	}
	eb, err := copyEffectiveSourceEvidencePin(ctx, turn, input, coverage.evidence, coverage)
	if err != nil {
		return nil, err
	}
	sb, err := copyEffectiveSourcePublicationPin(ctx, turn, input, coverage.publication, coverage)
	if err != nil {
		return nil, err
	}
	e, err := copyEffectiveSourceReceiptPin(ctx, turn, input, coverage.receipts[0], coverage)
	if err != nil {
		return nil, err
	}
	s, err := copyEffectiveSourceReceiptPin(ctx, turn, input, coverage.receipts[1], coverage)
	if err != nil {
		return nil, err
	}
	if coverage.next != 4 || coverage.remaining != turn.transaction.quantum.Remaining() {
		return nil, ErrConflict
	}
	if err = effectiveSourceLineageMatches(input, eb, sb, e, s); err != nil {
		return nil, err
	}
	if err = checkEffectiveSourceControlPoints(ctx, turn, input, e, s); err != nil {
		return nil, err
	}
	out = &effectiveVerifiedSource{turn: turn, tx: turn.transaction, input: input, digest: input.digest, evidence: e, source: s}
	out.self = out
	return out, nil
}

func checkEffectiveVerifiedSource(ctx context.Context, turn *commandTurn, input *effectiveStageBeginCommand, proof *effectiveVerifiedSource) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != EffectiveStage {
		return ErrInvalid
	}
	return checkNativeEffectiveSource(ctx, turn, input, proof)
}

func checkNativeEffectiveSource(ctx context.Context, turn *commandTurn, input *effectiveStageBeginCommand, proof *effectiveVerifiedSource) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if input == nil || proof == nil || proof.self != proof || proof.turn != turn || turn == nil || turn.transaction == nil || !effectiveSourceProofPurpose(turn.transaction.descriptor.ID) || proof.tx != turn.transaction || proof.input != input || proof.digest != input.digest {
		return ErrInvalid
	}
	if err = requireEffectiveSourceGrantKey(turn, input); err != nil {
		return err
	}
	return effectivePinLive(ctx, turn)
}

// The governed grant this turn was admitted under must be the SAME job as the
// command whose lineage is read: domain = the turn's closed purpose and job ID
// = the original provider operation. A proof can't be minted or checked
// for another operation's command on a grant admitted for a different job.
func requireEffectiveSourceGrantKey(turn *commandTurn, input *effectiveStageBeginCommand) error {
	if turn == nil || turn.transaction == nil || input == nil || input.request.ownerID == "" {
		return ErrInvalid
	}
	c, ok := turn.transaction.custody.(*governedTurn)
	if !ok || c == nil || c.purpose != turn.transaction.descriptor.ID || c.expected.Key.Domain != uint16(turn.transaction.descriptor.ID) || c.expected.Key.ID != input.request.ownerID {
		return ErrInvalid
	}
	return nil
}
