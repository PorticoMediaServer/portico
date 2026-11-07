package writer

import "context"

// This direction is closed and cannot borrow Inventory, another Basic purpose,
// or a caller-selected grant. The purpose is also in the frozen request.
func basicResourceCommand(purpose BasicPurpose) (CommandID, error) {
	switch purpose {
	case BasicPurposeStage:
		return BasicStage, nil
	case BasicPurposeSeal:
		return BasicSeal, nil
	case BasicPurposeAccept:
		return BasicAccept, nil
	default:
		return 0, ErrInvalid
	}
}

func checkBasicGrant(turn *commandTurn, kind CommandID) error {
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != kind {
		return ErrInvalid
	}
	c, ok := turn.transaction.custody.(*governedTurn)
	if !ok || c == nil || c.g == nil || c.a == nil || c.grant == nil {
		return &Refusal{Code: RefusalUnqualified}
	}
	return c.check(kind, turn.transaction.workLimit)
}

// Only fresh native effects consume this sample, after historical resolution
// and native original-lease proof. W's ONE clock/high-water and whole protected
// deadline are reused. There is no new clock, timer or lease extension.
func (turn *commandTurn) basicTimeMS(ctx context.Context, purpose BasicPurpose, leaseExpiresMS int64) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	kind, err := basicResourceCommand(purpose)
	if err != nil {
		return 0, err
	}
	if ctx == nil {
		return 0, ErrInvalid
	}
	if err = checkBasicGrant(turn, kind); err != nil {
		return 0, err
	}
	value, err = turn.commandTimeMS(ctx, kind, leaseExpiresMS)
	if err == nil {
		err = checkBasicGrant(turn, kind)
	}
	return value, err
}

// Physical finish rechecks the original lease AFTER rows close. It never
// overwrites the sample already saved in the immutable Basic receipt.
func checkBasicTerminal(ctx context.Context, turn *commandTurn, purpose BasicPurpose, leaseExpiresMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	_, err = turn.basicTimeMS(ctx, purpose, leaseExpiresMS)
	return err
}

// requiredBytes is the complete prospective domain peak. The common checker
// verifies the SAME exact-purpose grant and its nonborrowable native reserve;
// original preparation/retained result lifetimes are not refunded or moved.
func (turn *commandTurn) requireBasicMemory(ctx context.Context, purpose BasicPurpose, requiredBytes uint64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	kind, err := basicResourceCommand(purpose)
	if err != nil {
		return err
	}
	return turn.requireCommandMemory(ctx, kind, requiredBytes, "basic_memory_partition")
}
