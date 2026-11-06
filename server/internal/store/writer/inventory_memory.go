package writer

import "context"

// This is a partition of the SAME unit's real MemoryBytes grant, not a second
// ledger. W/native per-unit reserve is never loaned to domain preparation. All
// zero is valid for history-only profiles and confers NO fresh memory allowance.
type inventoryMemoryBudget = commandMemoryBudget

func validateInventoryMemory(b inventoryMemoryBudget, total int64) error {
	return validateMemoryPartition(b, total, "inventory_memory_partition")
}

// requiredBytes is the complete prospective peak, computed allocation-free
// from the sealed original input. This only checks coverage; it issues/resizes
// no grant and maintains/refunds no allocation ledger. Original preparation and
// retained-output lifetime custody are not transferred by this check.
func (t *commandTurn) requireInventoryMemory(ctx context.Context, requiredBytes uint64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	return t.requireCommandMemory(ctx, InventoryAccept, requiredBytes, "inventory_memory_partition")
}
