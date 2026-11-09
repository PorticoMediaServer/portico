package writer

import (
	"context"
	"database/sql"
	"errors"
)

// MP start-preparation expiry (packet MP-08, command 705).
//
// Timeout retires exactly one preparation whose stored deadline key has elapsed.
// The candidate is proved through the playback_start_preparations_deadline
// (deadline_ms,id) index before the lifecycle CAS runs. Expiry NEVER exits,
// cleans, deletes or re-parents the mp_start_execution row, its staged bytes or
// its custody: physical debt is retired by the physical-exit/cleanup families
// (MP-05 seam), not by elapsed time. Withheld exit is a retained observation.

func applyMPStartPrepTimeout(ctx context.Context, t *commandTurn, f mpStartAcceptFence, owner mpStartAcceptOwner) (out mpStartAcceptValue, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepTimeout); err != nil {
		return
	}
	if owner.state == "accepted" || owner.state == "retired" || owner.revision != f.revision ||
		owner.execution != f.execution || owner.custody != "" || owner.deadlineMS > owner.nowMS {
		return out, ErrConflict
	}
	row, err := mpPrepRow(ctx, t, "mp.accept.timeout_due", owner.nowMS, f.id)
	if err != nil {
		return out, err
	}
	var deadline int64
	var id string
	err = row.Scan(&deadline, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if deadline != owner.deadlineMS || id != f.id || deadline > owner.nowMS {
		return out, ErrResolveRequired
	}
	if err = mpAcceptExec(ctx, t, "mp.accept.timeout", f.id, f.revision, f.execution); err != nil {
		return out, err
	}
	return mpStartAcceptValue{Preparation: f.id, State: "retired", Execution: owner.execution,
		Session: owner.ids.session, LegacySession: owner.ids.legacySession, LegacyGeneration: owner.ids.legacyGeneration,
		Snapshot: owner.ids.snapshot, Revision: f.revision + 1, DeadlineMS: owner.deadlineMS,
		PhysicalExited: owner.physicalExited, Cleaned: owner.cleaned, AppliedMS: owner.nowMS}, nil
}
