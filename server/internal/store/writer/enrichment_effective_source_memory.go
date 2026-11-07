package writer

import "context"

// Only the exact EffectiveStage grant may fund the complete prospective
// source-proof workspace. Other domains' partitions and an unpartitioned
// legacy Effective fixture are never a substitute for this installed budget.
func (t *commandTurn) requireEffectiveSourceMemory(ctx context.Context, required uint64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	return t.requireCommandMemory(ctx, EffectiveStage, required, "effective_source_memory_partition")
}
