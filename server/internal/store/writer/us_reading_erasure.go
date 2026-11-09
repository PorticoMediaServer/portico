package writer

import "context"

// US-21/26 participants, not authority mints or accepting commands. The outer
// erasure owns the exact old membership/profile lifetime, selected personal
// parent, fresh original, all other children, accounting and commit. Legacy
// profile_purge_registry remains cutover-held and MUST NOT use this v1 SQL.
func checkUSReadingErasureTurn(turn *commandTurn) error {
	if turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	tx := turn.transaction
	if !tx.begun || tx.done || !tx.policyVerified || tx.descriptor.Durability != Full || tx.settings.Synchronous != 2 {
		return ErrInvalid
	}
	switch tx.descriptor.ID {
	case 539, 582:
		return nil
	default:
		return ErrInvalid
	}
}

func drainUSReadingPersonal(ctx context.Context, turn *commandTurn, s uoReadingPersonalScope) (out uoReadingResumeDrainEffect, err error) {
	defer func() { uoReadingCustodyFailure(turn, err) }()
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err := checkUSReadingErasureTurn(turn); err != nil {
		return uoReadingResumeDrainEffect{}, err
	}
	return drainUOReadingResume(ctx, turn, s)
}

func pruneUSReadingProfileReceipts(ctx context.Context, turn *commandTurn, s uoReadingPruneScope) (out uoReadingPruneEffect, err error) {
	defer func() { uoReadingCustodyFailure(turn, err) }()
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err := checkUSReadingErasureTurn(turn); err != nil {
		return uoReadingPruneEffect{}, err
	}
	if s.kind != uoReadingPruneProfile {
		return uoReadingPruneEffect{}, ErrInvalid
	}
	return pruneUOReadingReceipts(ctx, turn, s)
}
