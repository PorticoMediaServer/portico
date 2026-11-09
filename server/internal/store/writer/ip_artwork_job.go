package writer

import (
	"context"
	"errors"
)

// The native artwork job is the physical work owner; the retained subject job is
// the command owner. Claiming here binds the two by primary key and fixes the
// material generation, so a crash between the intent and the installed handoff
// cannot be confused with a different job generation. The job row is still the
// caller's work record: no provider call, file IO or image work happens inside
// this transaction.

// The job must be the exact row this owner intends to work: same subject job,
// namespace, role/choice, image, provider and selection revision, still due
// under its own revision/generation and not already completed.
func ipArtworkMaterialJobDue(ctx context.Context, turn *commandTurn, q ipArtworkMaterialRequest, nowMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	kind, subject := ipArtworkMaterialJobKind(q)
	j := q.job
	rows, err := ipTextRows(ctx, turn, ipAMJobDue, j.id, q.owner.id, kind, subject, j.role, j.choice,
		j.imageID, j.provider, j.selectionRevision, j.revision, j.leaseGeneration, j.dueMS, nowMS)
	if err != nil {
		return err
	}
	var one int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&one) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

// Claim advances the job lease and revision exactly once. The owner lease is the
// outer allowance; the returned generation is the claimed job generation, which
// the intent records and the installed handoff must still match.
func ipArtworkMaterialClaimJob(ctx context.Context, turn *commandTurn, q ipArtworkMaterialRequest, nowMS int64) (generation int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), generation, err)
		generation, err = v, e
	}()
	kind, subject := ipArtworkMaterialJobKind(q)
	j := q.job
	if err = ipTextExecOne(ctx, turn, ipAMClaim, q.owner.leaseOwner, q.owner.leaseUntilMS, j.id,
		q.owner.id, kind, subject, j.revision, j.leaseGeneration, nowMS); err != nil {
		return 0, err
	}
	return j.leaseGeneration + 1, nil
}

// The installed handoff requires the same live claim, never a fresh one.
func ipArtworkMaterialJobLive(ctx context.Context, turn *commandTurn, q ipArtworkMaterialRequest, nowMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	kind, subject := ipArtworkMaterialJobKind(q)
	rows, err := ipTextRows(ctx, turn, ipAMJobLive, q.spec.intentID, q.owner.id, kind, subject,
		q.jobRevision, q.spec.provider, q.owner.leaseOwner, q.jobGeneration, q.owner.leaseUntilMS, nowMS)
	if err != nil {
		return err
	}
	var one int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&one) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

// Completion is the same CAS under the same lease. It is refused for a job that
// is no longer running or whose claim already moved.
func ipArtworkMaterialCompleteJob(ctx context.Context, turn *commandTurn, q ipArtworkMaterialRequest, nowMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = ipTextExecOne(ctx, turn, ipAMComplete, q.spec.intentID, q.owner.id, q.jobRevision,
		q.owner.leaseOwner, q.jobGeneration, q.owner.leaseUntilMS, nowMS); err != nil {
		return err
	}
	return nil
}
