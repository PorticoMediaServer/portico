package writer

import (
	"context"
	"errors"
	"math"
)

func advanceBasicAcceptedRun(ctx context.Context, turn *commandTurn, identity CommandIdentity, in basicOriginalScope, facts basicFreshFacts) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != BasicAccept || identity.Sequence <= 0 || in.run.lastSequence != identity.Sequence-1 || in.run.acknowledgedSequence != in.run.lastSequence || facts.revision != in.run.revision || facts.revision <= 0 || facts.discovered < 0 || facts.analyzed < 0 || facts.warnings < 0 {
		return 0, ErrInvalid
	}
	if facts.revision == math.MaxInt64 || facts.analyzed == math.MaxInt64 {
		return 0, ErrCap
	}
	args := []any{facts.revision + 1, identity.Sequence, facts.analyzed + 1}
	args = append(args, basicControlRunArgs(identity, in)...)
	args = append(args, facts.discovered, facts.analyzed, facts.warnings)
	row, err := basicStageRead(ctx, turn, basicAcceptRunCAS, args...)
	if err != nil {
		return 0, err
	}
	var sequence, discovered, analyzed, warnings int64
	if err = row.Scan(&value, &sequence, &discovered, &analyzed, &warnings); err != nil {
		return 0, basicStageError(err)
	}
	if value != facts.revision+1 || sequence != identity.Sequence || discovered != facts.discovered || analyzed != facts.analyzed+1 || warnings != facts.warnings {
		return 0, ErrConflict
	}
	return value, nil
}

// Header and its complete ONE-member result are indivisible caller-owned
// effects. There is no prefix receipt, subsequent association reconstruction,
// independent commit, ACK refund or Applied assignment.
func writeBasicAcceptReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, value BasicResult) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != BasicAccept || value.Purpose != BasicPurposeAccept || value.ResultCount != 1 || len(value.Members) != 1 || value.Outcome != (ReceiptOutcome{}) {
		return ErrInvalid
	}
	digest, err := basicResultDigest(identity, request, value)
	if err != nil || digest != value.Digest {
		return errors.Join(ErrConflict, err)
	}
	if err = writeBasicControlReceipt(ctx, turn, identity, request, value); err != nil {
		return err
	}
	step, ok := turn.steps[scanResultInsert]
	if !ok {
		return invalid("basic_accept_member_step")
	}
	m := value.Members[0]
	changed, err := turn.exec(ctx, step.statementStep, value.RunID, identity.Incarnation[:], identity.Sequence, m.InputOrdinal, m.ChildKind, m.ChildOrdinal, m.ObjectID, m.LibraryID, m.SubjectID, m.AssetID, m.AcceptedAssetRevision)
	if err != nil {
		return err
	}
	count, err := changed.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}
