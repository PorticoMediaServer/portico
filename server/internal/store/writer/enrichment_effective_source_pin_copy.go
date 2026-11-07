package writer

import "context"

// Only the parent-owned, same-grant preflight reservation can enter these
// literal copies. Each slot is consumed once, on the original turn and input.
// They return native values, never a verified source or disclosure authority.

func copyEffectiveSourceEvidencePin(ctx context.Context, turn *commandTurn, input *effectiveStageBeginCommand, pre effectiveEvidencePinLengths, coverage *effectiveSourceByteCoverage) (out effectiveEvidenceBodyPin, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = effectivePinLive(ctx, turn); err != nil {
		return out, err
	}
	if input == nil || coverage == nil || pre.turn != turn || pre.tx != turn.transaction {
		return out, ErrInvalid
	}
	// A parent-owned concrete method consumes admission; no reader-issued grant.
	if err = coverage.consumeEvidencePinCopy(ctx, turn, input, pre); err != nil {
		return out, err
	}
	step, err := sealStep(turn, effectiveSourcePinEvidenceCopy)
	if err != nil {
		return out, err
	}
	rows, err := turn.query(ctx, step, pre.evidence)
	if err != nil {
		return out, err
	}
	var raw [9]any
	if err = effectivePinScanPoint(rows, effectivePinDest(raw[:])); err != nil {
		return out, err
	}
	out, err = decodeEffectiveSourceEvidencePin(pre, raw[:])
	if err != nil {
		return out, err
	}
	return out, effectivePinLive(ctx, turn)
}

func copyEffectiveSourcePublicationPin(ctx context.Context, turn *commandTurn, input *effectiveStageBeginCommand, pre effectivePublicationPinLengths, coverage *effectiveSourceByteCoverage) (out effectivePublicationBodyPin, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = effectivePinLive(ctx, turn); err != nil {
		return out, err
	}
	if input == nil || coverage == nil || pre.turn != turn || pre.tx != turn.transaction {
		return out, ErrInvalid
	}
	// A parent-owned concrete method consumes admission; no reader-issued grant.
	if err = coverage.consumePublicationPinCopy(ctx, turn, input, pre); err != nil {
		return out, err
	}
	step, err := sealStep(turn, effectiveSourcePinPublicationCopy)
	if err != nil {
		return out, err
	}
	rows, err := turn.query(ctx, step, pre.publication)
	if err != nil {
		return out, err
	}
	var raw [9]any
	if err = effectivePinScanPoint(rows, effectivePinDest(raw[:])); err != nil {
		return out, err
	}
	out, err = decodeEffectiveSourcePublicationPin(pre, raw[:])
	if err != nil {
		return out, err
	}
	return out, effectivePinLive(ctx, turn)
}

func copyEffectiveSourceReceiptPin(ctx context.Context, turn *commandTurn, input *effectiveStageBeginCommand, pre effectiveReceiptPinLengths, coverage *effectiveSourceByteCoverage) (out effectivePinnedSealReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = effectivePinLive(ctx, turn); err != nil {
		return out, err
	}
	if input == nil || coverage == nil || pre.turn != turn || pre.tx != turn.transaction {
		return out, ErrInvalid
	}
	// A parent-owned concrete method consumes admission; no reader-issued grant.
	if err = coverage.consumeReceiptPinCopy(ctx, turn, input, pre); err != nil {
		return out, err
	}
	step, err := sealStep(turn, effectiveSourcePinReceiptCopy)
	if err != nil {
		return out, err
	}
	rows, err := turn.query(ctx, step, pre.ref.owner, pre.ref.incarnation[:], pre.ref.sequence)
	if err != nil {
		return out, err
	}
	var raw [48]any
	if err = effectivePinScanPoint(rows, effectivePinDest(raw[:])); err != nil {
		return out, err
	}
	out, err = decodeEffectiveSourcePinnedReceipt(pre, raw[:])
	if err != nil {
		return out, err
	}
	return out, effectivePinLive(ctx, turn)
}
