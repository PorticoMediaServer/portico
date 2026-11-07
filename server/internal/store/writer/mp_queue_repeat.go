package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// MPQueueRepeat is reserved in MP's explicit range. Registration is deliberately
// deferred to the per-domain registry seam; never append to the shared iota.
const MPQueueRepeat CommandID = 754

// This private draft is an effect component, not an accepting facade. The sole
// dispatcher must first prove current opaque disclosure and receiptFresh, issue
// trusted actor/time facts, then save the ORIGINAL typed result and bounded
// events/debt in this SAME turn before any Applied response. MP-001 supplies the
// missing native owner/result relation. No caller may treat this effect as ACK.
type mpQueueRepeatRequest struct {
	queue, account, profile, device   string
	expectedRevision, expectedCustody int64
	repeat                            string
}
type mpQueueRepeatInvocation struct {
	request   mpQueueRepeatRequest
	appliedMS int64 // trusted writer command clock, never client time
}
type mpQueueRepeatEffect struct {
	queue, repeat, canceledToken, canceledMedia      string
	revision, custody, updatedMS, canceledGeneration int64
	canceledRequest                                  string
	physicalPending                                  bool
	changed                                          bool
}

func (q mpQueueRepeatRequest) validate() error {
	for _, s := range []string{q.queue, q.account, q.profile, q.device} {
		if len(s) == 0 || len(s) > 128 || !utf8.ValidString(s) {
			return ErrInvalid
		}
		for _, r := range s {
			if r < 32 || r == 127 {
				return ErrInvalid
			}
		}
	}
	if q.expectedRevision <= 0 || q.expectedRevision == math.MaxInt64 || q.expectedCustody < 0 || (q.repeat != "off" && q.repeat != "one" && q.repeat != "all") {
		return ErrInvalid
	}
	return nil
}
func mpQueueRepeatRow(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if turn == nil {
		return commandRow{}, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return commandRow{}, ErrInvalid
	}
	return turn.queryRow(ctx, step.statementStep, args...)
}
func applyMPQueueRepeat(ctx context.Context, turn *commandTurn, q mpQueueRepeatInvocation) (out mpQueueRepeatEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != MPQueueRepeat || q.appliedMS <= 0 {
		return out, ErrInvalid
	}
	if err = q.request.validate(); err != nil {
		return out, err
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	r := q.request
	row, err := mpQueueRepeatRow(ctx, turn, mpQueueRepeatCurrent, r.queue, r.account, r.profile, r.device, r.expectedRevision, r.expectedCustody)
	if err != nil {
		return out, err
	}
	var before string
	var updated int64
	if err = row.Scan(&before, &updated); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	} else if err != nil {
		return out, err
	}
	if before != "off" && before != "one" && before != "all" {
		return out, ErrResolveRequired
	}
	out = mpQueueRepeatEffect{queue: r.queue, repeat: before, revision: r.expectedRevision, custody: r.expectedCustody, updatedMS: updated}
	if before == r.repeat {
		return out, turn.checkLifetimes(ctx)
	}
	// The accepted protocol has at most one pending-next per queue. Probe two
	// instead of turning historical/corrupt multiplicity into an unbounded turn.
	step, ok := turn.steps[mpQueueRepeatPrepared]
	if !ok {
		return out, ErrInvalid
	}
	rows, err := turn.query(ctx, step.statementStep, r.queue)
	if err != nil {
		return out, err
	}
	count := 0
	for rows.Next() {
		count++
		if count > 1 {
			err = ErrResolveRequired
			break
		}
		if err = rows.Scan(&out.canceledToken, &out.canceledMedia, &out.canceledGeneration); err != nil {
			break
		}
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	if count == 1 {
		// W's same-turn physical retirement recipe must already have fenced
		// this exact occurrence. Never synthesize a request from a media ID.
		if !mpID(out.canceledToken) || !mpID(out.canceledMedia) || out.canceledGeneration <= 0 {
			return out, ErrResolveRequired
		}
		row, err = mpQueueRepeatRow(ctx, turn, mpQueueRepeatStopFact, out.canceledMedia)
		if err != nil {
			return out, err
		}
		var proof mpPhysicalStopFact
		if err = row.Scan(&proof.retired, &proof.exited, &proof.requestID, &proof.exitRequestID, &proof.supervisor, &proof.exitedMS); err != nil {
			return out, err
		}
		if !mpPhysicalRetirementValid(proof, out.canceledGeneration) {
			return out, ErrConflict
		}
		out.canceledRequest = proof.requestID
		out.physicalPending = proof.exited < out.canceledGeneration
		if out.physicalPending {
			row, err = mpQueueRepeatRow(ctx, turn, mpQueueRepeatStopInsert, out.canceledToken, out.canceledGeneration, q.appliedMS, out.canceledMedia, out.canceledGeneration, proof.requestID)
			if err != nil {
				return out, err
			}
			var request string
			if err = row.Scan(&request); err != nil {
				return out, err
			}
			if request != proof.requestID {
				return out, ErrResolveRequired
			}
		}
		row, err = mpQueueRepeatRow(ctx, turn, mpQueueRepeatCancel, out.canceledToken, r.queue, out.canceledMedia, out.canceledGeneration)
		if err != nil {
			return out, err
		}
		var token string
		if err = row.Scan(&token); errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		} else if err != nil {
			return out, err
		}
		if token != out.canceledToken {
			return out, ErrResolveRequired
		}
	}
	row, err = mpQueueRepeatRow(ctx, turn, mpQueueRepeatUpdate, r.repeat, max(updated, q.appliedMS), r.queue, r.account, r.profile, r.device, r.expectedRevision, r.expectedCustody, before)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.revision, &out.updatedMS); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	} else if err != nil {
		return out, err
	}
	if out.revision != r.expectedRevision+1 {
		return out, ErrResolveRequired
	}
	out.repeat, out.changed = r.repeat, true
	// The independent native stop obligation survives receipt ACK/reclaim.
	// W saves this complete original projection with effects before commit;
	// external cancellation and actual-exit confirmation remain post-commit.
	return out, turn.checkLifetimes(ctx)
}
