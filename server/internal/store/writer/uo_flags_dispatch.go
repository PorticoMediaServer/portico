package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
)

// UO-03 is the complete root original for native purpose 600: one sparse flags
// patch on personal_current_live plus the viewer/selection heads, saved as one
// closed typed result under the shared lifetime root. It is NORMAL durability,
// which is not authorization.
//
// Every entry consumes the SAME private same-commandTurn proof UO-01 defines and
// UO-02's component already uses: the proof's frame, purpose, lifetime, deadline
// and identity are rechecked before the retained owner/receipt lookup and again
// at release through the same shared uoRootReclaimRelease rule. This file mints
// no developer token, invents no second issuer and accepts no caller bool.
// UO-01's bindUORoot still refuses every NORMAL proof until Sol's current-session
// mint lands, so the purpose keeps its own proof checks instead of presenting a
// binding that cannot pass.

// lookupUORootFlags follows UO-01's skeleton exactly: current authority and the
// root PK precede every retained owner/header read, the owner point is read once,
// the receipt meta point is read before any payload, and W's classifyReceipt is
// the sole receipt classifier. No second sequence comparison is introduced here,
// so a stale incarnation still returns ErrResolveRequired joined with
// ErrOwnerUnavailable.
func lookupUORootFlags(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, q uoRootReference) (out uoRootLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !q.validFor(turn) || q.kind != SetPersonalFlags {
		return out, ErrInvalid
	}
	binding, err := uoRootProofCurrent(proof, turn)
	if err != nil {
		return out, err
	}
	rootState, err := binding.root(ctx, turn, q.root)
	if err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, uoRootOwnerPoint, q.root)
	if err != nil {
		return out, err
	}
	var owner uoRootOwner
	var incarnation []byte
	err = row.Scan(&incarnation, &owner.facts.lastSequence, &owner.facts.acknowledgedSequence, &owner.revision, &owner.rows, &owner.variableBytes, &owner.afterSequence, &owner.due)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if len(incarnation) != 16 || owner.revision <= 0 || owner.rows < 0 || owner.variableBytes < 0 || (owner.rows == 0) != (owner.variableBytes == 0) || owner.afterSequence < 0 || owner.afterSequence > owner.facts.acknowledgedSequence || owner.due.Valid && owner.due.Int64 < 0 {
		return out, ErrResolveRequired
	}
	copy(owner.facts.incarnation[:], incarnation)
	owner.facts.exists = true
	expected := receiptIdentity{command: q.identity, digest: q.request}
	found := receiptFacts{}
	var size, variable, appliedMS int64
	// ACKed originals need no receipt header to remain retired; the floor is
	// classified from the owner and the row is left at the floor.
	if q.identity.Sequence > owner.facts.acknowledgedSequence {
		row, err = wc008DraftQuery(ctx, turn, uoRootReceiptMeta, q.root, q.identity.Incarnation[:], q.identity.Sequence)
		if err != nil {
			return out, err
		}
		var kind, version, count, digestBytes int64
		var operation, request []byte
		err = row.Scan(&kind, &version, &count, &appliedMS, &size, &variable, &digestBytes, &operation, &request)
		switch {
		case err == nil:
			if kind != int64(q.kind) || !bytes.Equal(operation, q.identity.OperationID[:]) || !bytes.Equal(request, q.request[:]) {
				return out, ErrConflict
			}
			// Exact prospective bounds precede any payload copy. variable_bytes
			// also proves the stored row is the complete native record.
			if version != 1 || count != 0 || appliedMS < 0 || size < 1 || size > uoRootPayloadCap || variable != size+uoRootFixedBytes || digestBytes != 32 {
				return out, ErrResolveRequired
			}
			found = receiptFacts{exists: true, identity: expected, kind: q.kind, resultCount: count}
		case !errors.Is(err, sql.ErrNoRows):
			return out, err
		}
	}
	decision, err := classifyReceipt(expected, q.kind, owner.facts, found)
	if err != nil {
		return out, err
	}
	// A draining root keeps an authorized ORIGINAL replay but admits no Fresh
	// effect.
	if decision == receiptFresh && rootState != "active" {
		return out, ErrUORootGone
	}
	// A constructor-time check cannot outlive a revocation or root change during
	// these reads.
	if err = uoRootReclaimRelease(ctx, turn, proof, q.root, decision); err != nil {
		return out, err
	}
	return uoRootLookup{turn: turn, binding: binding, reference: q, owner: owner, decision: decision, payloadBytes: size, variableBytes: variable, resultCount: found.resultCount, appliedMS: appliedMS}, nil
}

// uoRootFlagsInvocation is one patch or ACK request. The reference is a
// comparison value supplied by the caller's retry custody, never proof.
type uoRootFlagsInvocation struct {
	reference uoRootReference
	public    [16]byte
	input     uoPersonalFlagsInput
	nowMS     int64
	ack       bool
}

// uoRootFlagsPublished is the pre-terminal release packet of the closed dispatch
// entry.
type uoRootFlagsPublished struct {
	out   uoRootFlagsResult
	floor int64
}

// runUOFlagsDispatch is the closed test/harness entry for 600. It mirrors
// runUSPolicyDispatch on main: the body is released through finishCommand BEFORE
// the physical terminal (which cancels its own lifetime), a release-time denial
// REPLACES the body and rolls the prefix back, an unknown COMMIT is
// Resolve-required with zero output, and a final deferred publication on the
// CALLER's context withdraws a result that committed before a late cancel.
func runUOFlagsDispatch(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, inv uoRootFlagsInvocation) (out uoRootFlagsResult, floor int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, ctx, uoRootFlagsPublished{out, floor}, err)
		out, floor, err = v.out, v.floor, e
	}()
	lookup, err := lookupUORootFlags(ctx, turn, proof, inv.reference)
	if err != nil {
		return withdrawUOFlags(turn, err)
	}
	if inv.ack {
		floor, err = acknowledgeUORootFlags(ctx, turn, proof, lookup, inv.nowMS)
	} else {
		switch lookup.decision {
		case receiptFresh:
			if inv.public != inv.input.publicID || inv.nowMS <= 0 {
				return withdrawUOFlags(turn, ErrInvalid)
			}
			var effect uoPersonalFlagsEffect
			if effect, err = applyUOPersonalFlags(ctx, turn, inv.input); err == nil {
				// A nonempty personal prefix is rolled back with the whole turn if
				// the root original cannot be saved.
				if _, err = persistUORootFlags(ctx, turn, proof, lookup, inv.public, effect, inv.nowMS); err == nil {
					out = uoRootFlagsResult{root: inv.reference.root, public: inv.public, effect: effect, appliedMS: inv.nowMS}
				}
			}
		case receiptReplay:
			out, err = restoreUORootFlags(ctx, turn, proof, lookup)
		default:
			out, err = uoRootFlagsResult{}, ErrResolveRequired
		}
	}
	if err != nil {
		return withdrawUOFlags(turn, err)
	}
	// Final current authority/root before the terminal. A release-time denial
	// replaces the body outcome and rolls back the whole prefix.
	if out != (uoRootFlagsResult{}) {
		decision := receiptFresh
		if lookup.decision == receiptReplay {
			decision = receiptReplay
		}
		if err = uoRootReclaimRelease(ctx, turn, proof, inv.reference.root, decision); err != nil {
			return withdrawUOFlags(turn, err)
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return withdrawUOFlags(turn, ctxErr)
	}
	release, releaseErr := finishCommand(ctx, commandLifetime(turn), uoRootFlagsPublished{out, floor}, nil)
	if releaseErr != nil {
		return withdrawUOFlags(turn, releaseErr)
	}
	out, floor = release.out, release.floor
	if rowsErr := turn.closeRows(); rowsErr != nil {
		return withdrawUOFlags(turn, rowsErr)
	}
	physical, commitErr := turn.transaction.commit()
	switch physical.state {
	case committed:
		// finishCommand's release rule: no error, including a late cancel or a
		// committed safety overrun, may carry output from the private frame.
		if commitErr != nil {
			return uoRootFlagsResult{}, 0, commitErr
		}
		return out, floor, nil
	case commitUnknown:
		_, _ = turn.transaction.rollback(commitErr)
		return uoRootFlagsResult{}, 0, errors.Join(ErrResolveRequired, commitErr)
	default:
		_, _ = turn.transaction.rollback(commitErr)
		if commitErr == nil {
			commitErr = ErrResolveRequired
		}
		return uoRootFlagsResult{}, 0, commitErr
	}
}

func withdrawUOFlags(turn *commandTurn, err error) (uoRootFlagsResult, int64, error) {
	if turn != nil && turn.transaction != nil {
		_, _ = turn.transaction.rollback(err)
	}
	return uoRootFlagsResult{}, 0, err
}
