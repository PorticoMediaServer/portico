package writer

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// This is a partition of the SAME unit's real MemoryBytes grant, not a second
// ledger. W/native per-unit reserve is never loaned to domain preparation. All
// zero is valid for history-only profiles and confers NO fresh memory allowance.
type inventoryMemoryBudget struct{ preparationBytes, reservedBytes uint64 }

func validateInventoryMemory(b inventoryMemoryBudget, total int64) error {
	if b == (inventoryMemoryBudget{}) {
		return nil
	}
	if b.preparationBytes == 0 || b.reservedBytes == 0 || total <= 0 || b.preparationBytes > uint64(total) || b.reservedBytes != uint64(total)-b.preparationBytes {
		return invalid("inventory_memory_partition")
	}
	return nil
}

// requiredBytes is the complete prospective peak, computed allocation-free
// from the sealed original input. This only checks coverage; it issues/resizes
// no grant and maintains/refunds no allocation ledger. Original preparation and
// retained-output lifetime custody are not transferred by this check.
func (t *commandTurn) requireInventoryMemory(ctx context.Context, requiredBytes uint64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if ctx == nil || t == nil || t.transaction == nil || requiredBytes == 0 {
		return ErrInvalid
	}
	tx := t.transaction
	if tx.ctx == nil || tx.writer == nil || tx.conn == nil || tx.quantum == nil || tx.descriptor.ID != InventoryAccept {
		return ErrInvalid
	}
	if tx.done || !tx.begun {
		return ErrClosed
	}
	defer tx.guardPanic()
	if err := t.checkLifetimes(ctx); err != nil {
		return err
	}
	c, ok := tx.custody.(*governedTurn)
	if !ok || c == nil || c.g == nil || c.a == nil || c.grant == nil {
		return &Refusal{Code: RefusalUnqualified}
	}
	if err := c.check(InventoryAccept, tx.workLimit); err != nil {
		return err
	}
	b := c.inventoryMemory
	if err := validateInventoryMemory(b, c.expected.Need[governor.MemoryBytes]); err != nil {
		return err
	}
	if b == (inventoryMemoryBudget{}) {
		return &Refusal{Code: RefusalUnqualified}
	}
	if requiredBytes > b.preparationBytes {
		return &Refusal{Code: RefusalCapacity}
	}
	return nil
}
