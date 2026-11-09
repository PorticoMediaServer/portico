package writer

import (
	"context"
	"math"
)

// The root adapter follows W's existing original-result ACK rule. It verifies
// the complete closed operation payload before moving only the historical
// floor/due/revision. Receipt rows, payloads, the fixed cleanup slot and the
// retained-byte quota stay held: ACK never refunds.
//
// The current proof is the same private same-commandTurn NORMAL seam UO-01
// defines. This file mints no authority and invents no second issuer; a caller
// bool, cached role or frozen descriptor cannot fill it.
func acknowledgeUORootOperation(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, lookup uoRootLookup, nowMS int64) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(turn), floor, err) }()
	if lookup.decision == receiptReplay {
		if _, err := restoreUORootOperation(ctx, turn, lookup); err != nil {
			return 0, err
		}
	}
	return acknowledgeUORootVerified(ctx, turn, proof, lookup, nowMS)
}

// Only a closed purpose verifier calls this scalar native ACK body after
// complete original result validation, or with an already retired floor. The
// UPDATE carries every observed owner fact so a concurrent change fails
// instead of moving a stale floor.
func acknowledgeUORootVerified(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, lookup uoRootLookup, nowMS int64) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(turn), floor, err) }()
	if lookup.turn != turn || nowMS <= 0 || !lookup.reference.valid() {
		return 0, ErrInvalid
	}
	binding, err := uoRootProofCurrent(proof, turn)
	if err != nil {
		return 0, err
	}
	q := lookup.reference
	if _, err = binding.root(ctx, turn, q.root); err != nil {
		return 0, err
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	o := lookup.owner
	if lookup.decision == receiptRetired && q.identity.Sequence <= o.facts.acknowledgedSequence {
		return o.facts.acknowledgedSequence, nil
	}
	// A reclaim original has no child receipt, so its own row/byte debt may be
	// zero; every other purpose must still hold a real original.
	if lookup.decision != receiptReplay || q.identity.Sequence != o.facts.lastSequence || q.identity.Sequence <= o.facts.acknowledgedSequence || q.kind != RootReceiptReclaim && (o.rows <= 0 || o.variableBytes <= 0) || o.revision == math.MaxInt64 {
		return 0, ErrResolveRequired
	}
	var nextDue, oldDue any
	statement := uoRootAckNull
	if o.due.Valid {
		oldDue = o.due.Int64
		statement = uoRootAckPoint
	}
	if o.rows > 0 {
		due := nowMS
		if o.due.Valid {
			due = min(due, o.due.Int64)
		}
		nextDue = due
	}
	args := []any{q.identity.Sequence, nextDue, o.revision + 1, q.root, q.identity.Incarnation[:], o.facts.lastSequence, o.facts.acknowledgedSequence, o.revision, o.rows, o.variableBytes, o.afterSequence}
	if o.due.Valid {
		args = append(args, oldDue)
	}
	floor, err = wc008DraftInteger(ctx, turn, statement, args...)
	if err != nil {
		return 0, err
	}
	if floor != q.identity.Sequence {
		return 0, ErrResolveRequired
	}
	// The floor is not released while current authority or the root is stale.
	if err = uoRootReclaimRelease(ctx, turn, proof, q.root, receiptReplay); err != nil {
		return 0, err
	}
	return floor, turn.checkLifetimes(ctx)
}
