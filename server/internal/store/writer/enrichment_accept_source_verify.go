package writer

import "context"

// A FINAL-command source dependency, never BEGIN authority. The proof retains
// only references to already funded frozen input and the same-turn native
// lineage result. Current owner/source checks and the complete materialized
// image verification remain the accepting domain's responsibility.
type enrichAcceptVerifiedSource struct {
	self     *enrichAcceptVerifiedSource
	input    *enrichAcceptCommand
	identity CommandIdentity
	digest   RequestDigest
	native   *effectiveVerifiedSource
}

func validateEnrichAcceptSourceInput(input *enrichAcceptCommand) error {
	if input == nil || !input.valid || input.digest == (RequestDigest{}) || input.identity.Sequence <= 0 || input.originalBegin == nil || !input.originalBegin.valid {
		return ErrInvalid
	}
	begin, final := input.originalBegin, input.request
	// The FINAL command is a distinct request: equal digests would alias BEGIN.
	if input.digest == begin.digest {
		return ErrConflict
	}
	if begin.sourceReferences == nil || final.beginIdentity != begin.identity || final.beginRequest != begin.digest ||
		input.identity.Incarnation != begin.identity.Incarnation || input.identity.Sequence <= begin.identity.Sequence ||
		final.owner.incarnation != input.identity.Incarnation || final.owner.entityID != begin.request.entity.id ||
		final.owner.provider != begin.request.owner.provider || final.entity.id != begin.request.entity.id ||
		final.entity.incarnation != begin.request.entity.incarnation || final.producer.Evidence != begin.request.producer.Evidence {
		return ErrConflict
	}
	return nil
}

// Re-read the ORIGINAL immutable E/S lineage under the FINAL EnrichAccept grant.
// No descriptor mutation, synthetic BEGIN turn, callback or second verifier.
func verifyEnrichAcceptSource(ctx context.Context, turn *commandTurn, input *enrichAcceptCommand) (out *enrichAcceptVerifiedSource, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != EnrichAccept {
		return nil, ErrInvalid
	}
	if err = validateEnrichAcceptSourceInput(input); err != nil {
		return nil, err
	}
	native, err := verifyNativeEffectiveSource(ctx, turn, input.originalBegin)
	if err != nil {
		return nil, err
	}
	out = &enrichAcceptVerifiedSource{input: input, identity: input.identity, digest: input.digest, native: native}
	out.self = out
	return out, nil
}

func checkEnrichAcceptVerifiedSource(ctx context.Context, turn *commandTurn, input *enrichAcceptCommand, proof *enrichAcceptVerifiedSource) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != EnrichAccept || proof == nil || proof.self != proof || proof.input != input || input == nil || proof.identity != input.identity || proof.digest != input.digest {
		return ErrInvalid
	}
	if err = validateEnrichAcceptSourceInput(input); err != nil {
		return err
	}
	return checkNativeEffectiveSource(ctx, turn, input.originalBegin, proof.native)
}
