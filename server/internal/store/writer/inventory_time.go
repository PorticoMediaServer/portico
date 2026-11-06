package writer

import (
	"context"
	"time"
)

// inventoryTimeMS supplies the W-owned acceptance timestamp after the ORIGINAL
// receipt miss and fresh native lease proof. It confers no scope/admission or
// native-owner authority. The same frozen lease must cover the WHOLE existing
// protected deadline, including terminal work; no extra timer is created.
func (t *commandTurn) inventoryTimeMS(ctx context.Context, leaseExpiresMS int64) (nowMS int64, err error) {
	defer func() { nowMS, err = finishCommand(ctx, commandLifetime(t), nowMS, err) }()
	if ctx == nil || t == nil || t.transaction == nil || leaseExpiresMS <= 0 {
		return 0, ErrInvalid
	}
	tx := t.transaction
	if tx.writer == nil || tx.writer.now == nil || tx.ctx == nil || tx.conn == nil || tx.quantum == nil || tx.custody == nil || tx.descriptor.ID != InventoryAccept {
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
	expires := time.UnixMilli(leaseExpiresMS)
	if !now.Before(expires) || deadline.After(expires) {
		return 0, ErrConflict
	}
	if !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	return ms, nil
}

// Only the closed Inventory fresh branch calls this, after complete domain
// effects/row closure and before the common preterminal permission check. It
// rechecks time without replacing the timestamp already stored in the result.
func checkInventoryTerminal(ctx context.Context, turn *commandTurn, input *inventoryAcceptCommand) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if input == nil || !input.valid {
		return ErrInvalid
	}
	_, err = turn.inventoryTimeMS(ctx, input.request.run.leaseExpiresMS)
	return err
}
