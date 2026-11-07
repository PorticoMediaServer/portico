package writer

import "time"

// frameEvidence is a read-only projection AFTER the frame's terminal cleanup.
// Domain receipt status is deliberately absent: physical COMMIT alone cannot
// construct Applied. It performs no I/O, clock call, policy repair or recovery.
func frameEvidence(tx *transaction, custody *governedTurn) *ExecutionEvidence {
	if tx == nil || custody == nil {
		return nil
	}
	out := &ExecutionEvidence{
		Purpose: tx.descriptor.ID, Class: custody.expected.Class,
		Durability: tx.descriptor.Durability, HoldStartedAt: tx.start,
		HoldTarget: tx.limit, HoldSafetyCeiling: MaxCommandSafetyHold,
	}
	// An acquisition failure has no complete asserted policy. In that case
	// preserve absence rather than publishing zero-valued settings as evidence.
	if tx.policyVerified {
		settings := tx.settings
		out.Policy = &settings
	}
	point := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		return &t
	}
	out.CommitAttemptedAt = point(tx.commitStartedAt)
	out.CommitReturnedAt = point(tx.commitReturnedAt)
	out.PhysicalReturnedAt = point(tx.physicalReturnedAt)
	if !tx.done || !tx.cleanupComplete {
		return out // no inferred terminal observation while cleanup is incomplete
	}
	out.HoldTargetExceeded = tx.limit > 0 && tx.result.hold > tx.limit
	switch tx.result.state {
	case committed:
		out.Commit = CommitSucceeded
	case commitUnknown:
		out.Commit = CommitUnknown
	case rolledBack:
		if tx.rollbackReturned {
			out.Commit = CommitRolledBack
		} else {
			out.Commit = CommitNotAttempted
		}
	}
	return out
}
