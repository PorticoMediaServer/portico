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
	if input == nil || proof == nil || proof.self != proof || proof.turn != turn || turn == nil || proof.tx != turn.transaction || proof.input != input || proof.digest != input.digest {
		return ErrInvalid
	}
	return effectivePinLive(ctx, turn)
}
