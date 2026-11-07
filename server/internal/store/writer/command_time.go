package writer

import (
	"context"
	"time"
)

// Only closed domain wrappers select purpose. All admission/effect-value clock
// samples share the SAME transaction high-water and original protected deadline.
// This factors existing policy; it does not implement clock follow-up1118.
func (t *commandTurn) commandTimeMS(ctx context.Context, purpose CommandID, leaseExpiresMS int64) (nowMS int64, err error) {
	defer func() { nowMS, err = finishCommand(ctx, commandLifetime(t), nowMS, err) }()
	if purpose != InventoryAccept && purpose != SearchImageStage && purpose != InventoryCreate && purpose != InventoryClaim && purpose != BasicStage && purpose != BasicSeal && purpose != BasicAccept && purpose != ProgressAccept && purpose != SecurityAccept {
		return 0, ErrInvalid
	}
	if ctx == nil || t == nil || t.transaction == nil ||
		((purpose == InventoryCreate || purpose == ProgressAccept || purpose == SecurityAccept) && leaseExpiresMS != 0) || (purpose != InventoryCreate && purpose != ProgressAccept && purpose != SecurityAccept && leaseExpiresMS <= 0) {
		return 0, ErrInvalid
	}
	tx := t.transaction
	if tx.writer == nil || tx.writer.now == nil || tx.ctx == nil || tx.conn == nil || tx.quantum == nil || tx.custody == nil || tx.descriptor.ID != purpose {
		return 0, ErrInvalid
	}
	if tx.done || !tx.begun {
		return 0, ErrClosed
	}
	defer tx.guardPanic()
	if err := t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	deadline, ok := tx.ctx.Deadline()
	if !ok {
		return 0, ErrInvalid
	}
	now := tx.writer.now()
	ms := now.UnixMilli()
	if ms < 0 || !time.UnixMilli(ms).Equal(now.Truncate(time.Millisecond)) {
		return 0, ErrInvalid
	}
	if err := tx.observeTime(now); err != nil {
		return 0, err
	}
	if purpose != InventoryCreate && purpose != ProgressAccept && purpose != SecurityAccept {
		expires := time.UnixMilli(leaseExpiresMS)
		if !now.Before(expires) || deadline.After(expires) {
			return 0, ErrConflict
		}
	}
	if !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	return ms, nil
}
