package writer

import "context"

// Only the exact EffectiveStage or EnrichAccept grant may fund the complete prospective
// source-proof workspace. Other domains' partitions and an unpartitioned
// legacy Effective fixture are never a substitute for this installed budget.
func (t *commandTurn) requireEffectiveSourceMemory(ctx context.Context, required uint64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if t == nil || t.transaction == nil || !effectiveSourceProofPurpose(t.transaction.descriptor.ID) {
		return ErrInvalid
	}
	return t.requireCommandMemory(ctx, t.transaction.descriptor.ID, required, "effective_source_memory_partition")
}

// The common verifier never coerces an Accept descriptor into BEGIN authority.
func effectiveSourceProofPurpose(p CommandID) bool { return p == EffectiveStage || p == EnrichAccept }
