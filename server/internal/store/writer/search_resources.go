package writer

import "context"

type searchImageMemoryBudget = commandMemoryBudget

func validateSearchImageMemory(b searchImageMemoryBudget, total int64) error {
	return validateMemoryPartition(b, total, "search_image_memory_partition")
}

// The original publisher lease must cover the WHOLE protected turn, including
// terminal work. This samples W time, never a value from the caller's packet.
func (t *commandTurn) searchImageTimeMS(ctx context.Context, leaseExpiresMS int64) (nowMS int64, err error) {
	defer func() { nowMS, err = finishCommand(ctx, commandLifetime(t), nowMS, err) }()
	return t.commandTimeMS(ctx, SearchImageStage, leaseExpiresMS)
}

// requiredBytes is the complete allocation-free prospective peak. The SAME
// real exact-purpose grant covers it; native/common reserve cannot be borrowed.
// This check neither grants, resizes nor refunds memory.
func (t *commandTurn) requireSearchImageMemory(ctx context.Context, requiredBytes uint64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	return t.requireCommandMemory(ctx, SearchImageStage, requiredBytes)
}

func checkSearchImageTerminal(ctx context.Context, turn *commandTurn, input *searchImageStageCommand) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if input == nil || !input.valid {
		return ErrInvalid
	}
	_, err = turn.searchImageTimeMS(ctx, input.request.owner.leaseExpiresMS)
	return err
}
