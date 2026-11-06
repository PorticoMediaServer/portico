package writer

import "context"

// inventoryTimeMS supplies the W-owned acceptance timestamp after the ORIGINAL
// receipt miss and fresh native lease proof. It confers no scope/admission or
// native-owner authority. The same frozen lease must cover the WHOLE existing
// protected deadline, including terminal work; no extra timer is created.
func (t *commandTurn) inventoryTimeMS(ctx context.Context, leaseExpiresMS int64) (nowMS int64, err error) {
	defer func() { nowMS, err = finishCommand(ctx, commandLifetime(t), nowMS, err) }()
	return t.commandTimeMS(ctx, InventoryAccept, leaseExpiresMS)
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
