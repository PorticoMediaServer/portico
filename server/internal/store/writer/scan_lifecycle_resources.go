package writer

import (
	"context"
	"time"
)

// Creation is deliberately unleased. Its timestamp is still the SAME owned
// frame clock/high-water and ORIGINAL protected deadline, never a caller time.
func (t *commandTurn) scanCreateTimeMS(ctx context.Context) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(t), value, err) }()
	return t.commandTimeMS(ctx, InventoryCreate, 0)
}

// One sample selects/checks the closed claim mode. Initial and expired reclaim
// do not require an old lease to cover admission. LIVE renewal must cover BOTH
// old and new deadlines, and cannot lower the old expiry. Physical old-work
// exclusion and source authority remain the issuer's separate prerequisites.
func (t *commandTurn) scanClaimTimeMS(ctx context.Context, mode scanRunClaimMode, oldExpiry, newExpiry int64) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(t), value, err) }()
	value, err = t.commandTimeMS(ctx, InventoryClaim, newExpiry)
	if err != nil {
		return 0, err
	}
	switch mode {
	case scanRunInitialClaim:
		if oldExpiry != 0 {
			return 0, ErrConflict
		}
	case scanRunExpiredReclaim:
		if oldExpiry <= 0 || oldExpiry > value {
			return 0, ErrConflict
		}
	case scanRunLiveRenew:
		deadline, ok := t.transaction.ctx.Deadline()
		if !ok || oldExpiry <= value || newExpiry <= oldExpiry || deadline.After(time.UnixMilli(oldExpiry)) {
			return 0, ErrConflict
		}
	default:
		return 0, invalid("scan_lifecycle_claim_mode")
	}
	return value, nil
}

// A partition of the SAME exact-purpose grant. No quota, second ledger, resize
// or refund is introduced; an uninstalled/zero partition remains unqualified.
func (t *commandTurn) requireScanLifecycleMemory(ctx context.Context, bytes uint64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if t == nil || t.transaction == nil {
		return ErrInvalid
	}
	purpose := t.transaction.descriptor.ID
	if purpose != InventoryCreate && purpose != InventoryClaim {
		return ErrInvalid
	}
	return t.requireCommandMemory(ctx, purpose, bytes, "scan_lifecycle_memory_partition")
}
