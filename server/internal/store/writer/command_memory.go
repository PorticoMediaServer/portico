package writer

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// A private partition of ONE exact grant; never a second allocation ledger.
// Closed domain wrappers select their own installed partition and purpose.
type commandMemoryBudget struct{ preparationBytes, reservedBytes uint64 }

func validateMemoryPartition(b commandMemoryBudget, total int64, field string) error {
	if b == (commandMemoryBudget{}) {
		return nil
	}
	if b.preparationBytes == 0 || b.reservedBytes == 0 || total <= 0 || b.preparationBytes > uint64(total) || b.reservedBytes != uint64(total)-b.preparationBytes {
		return invalid(field)
	}
	return nil
}

func (t *commandTurn) requireCommandMemory(ctx context.Context, purpose CommandID, requiredBytes uint64, partitionField string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if purpose != InventoryAccept && purpose != SearchImageStage && purpose != InventoryCreate && purpose != InventoryClaim && purpose != BasicStage && purpose != BasicSeal && purpose != BasicAccept && !enrichmentPurpose(purpose) {
		return ErrInvalid
	}
	if ctx == nil || t == nil || t.transaction == nil || requiredBytes == 0 {
		return ErrInvalid
	}
	tx := t.transaction
	if tx.ctx == nil || tx.writer == nil || tx.conn == nil || tx.quantum == nil || tx.descriptor.ID != purpose {
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
	if err := c.check(purpose, tx.workLimit); err != nil {
		return err
	}
	b := c.inventoryMemory
	if purpose == SearchImageStage {
		b = c.searchImageMemory
	}
	if purpose == InventoryCreate || purpose == InventoryClaim {
		b = c.scanLifecycleMemory
	}
	if purpose == BasicStage || purpose == BasicSeal || purpose == BasicAccept {
		b = c.basicMemory
	}
	if enrichmentPurpose(purpose) {
		b = c.enrichmentMemory
	}
	if err := validateMemoryPartition(b, c.expected.Need[governor.MemoryBytes], partitionField); err != nil {
		return err
	}
	if b == (commandMemoryBudget{}) {
		return &Refusal{Code: RefusalUnqualified}
	}
	if requiredBytes > b.preparationBytes {
		return &Refusal{Code: RefusalCapacity}
	}
	return nil
}
