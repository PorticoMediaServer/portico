package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

// UO-02 ties the shared lifetime-root custody to the reviewed WC reclamation
// transition (#1266). It is one bounded cleanup quantum for ONE root: at most
// one ACKed child receipt is removed, its exact native variable bytes are
// refunded, and the complete original result is saved in the owner's fixed
// <=4096 cleanup slot. No child receipt is created for the cleanup itself, so
// reclaim can never recurse or manufacture idle debt.
//
// The NORMAL durability is not authority. Every entry binds the same private
// same-commandTurn proof UO-01 defines before any retained owner/slot/child read
// and re-proves the frame, identity and root before release. The proof's
// current-authority mint is still pending from Sol, so 654 is not product-wired.

// uoRootProofCurrent validates the closed private current proof for this frame
// and returns the identity-bound root reader. It mints nothing: the proof has no
// constructor or exported field outside this package, so a caller string,
// cached role, descriptor or registration cannot stand in for it.
//
// Its release re-proof (uoRootReclaimRelease) covers only this frame/identity and
// the exact root row; it deliberately makes no current-authority check call.
// uoRootBinding.check refuses every NORMAL turn until Sol's us_current_fence
// mint exists, and that mint is the only thing that can answer the check. So 654
// stays a component until the mint lands; do not wire it before then.
func uoRootProofCurrent(proof *uoRootCurrentProof, turn *commandTurn) (*uoRootBinding, error) {
	if err := proof.current(turn); err != nil {
		return nil, err
	}
	binding := &uoRootBinding{identity: proof.identity}
	if !binding.identity.complete() {
		return nil, ErrUORootCurrentFenceUnavailable
	}
	return binding, nil
}

// uoRootReclaimRelease re-proves current authority and the exact native root
// identity/state immediately before release. A fresh cleanup must end on an
// active root; an authorized original replay may end draining.
func uoRootReclaimRelease(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, rootID string, decision receiptDecision) error {
	binding, err := uoRootProofCurrent(proof, turn)
	if err != nil {
		return err
	}
	state, err := binding.root(ctx, turn, rootID)
	if err != nil {
		return err
	}
	if decision == receiptFresh && state != "active" {
		return ErrUORootGone
	}
	return turn.checkLifetimes(ctx)
}

// lookupUORootReclaim classifies the caller's own 654 original from the fixed
// owner slot. The reclaim command is the only root purpose whose original lives
// there instead of a child receipt; a replay of a newer slot can never be
// decoded as an older command.
func lookupUORootReclaim(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, q uoRootReference) (out uoRootLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !q.validFor(turn) || q.kind != RootReceiptReclaim {
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
	// This is NOT a second classifier. The fixed slot is projected into W's
	// receiptFacts and the one shared classifyReceipt makes the decision. The
	// owner incarnation is compared before the slot is read: a stale-incarnation
	// retry must answer exactly as W does (ResolveRequired joined with
	// OwnerUnavailable), never as a raw slot miss.
	expected := receiptIdentity{command: q.identity, digest: q.request}
	found := receiptFacts{}
	var old uoRootStoredSlot
	if owner.facts.incarnation == q.identity.Incarnation {
		old, err = loadUORootSlot(ctx, turn, q)
		if err != nil {
			return out, err
		}
		if old.slot.present && old.slot.reference.Identity.Sequence == q.identity.Sequence {
			found = receiptFacts{
				exists:      true,
				identity:    receiptIdentity{command: old.slot.reference.Identity, digest: old.slot.reference.RequestDigest},
				kind:        q.kind,
				resultCount: old.slot.removedCount,
			}
		}
	}
	decision, err := classifyReceipt(expected, q.kind, owner.facts, found)
	if err != nil {
		return out, err
	}
	if decision == receiptFresh && rootState != "active" {
		return out, ErrUORootGone
	}
	// A constructor-time read cannot outlive a revocation or root change.
	if err = uoRootReclaimRelease(ctx, turn, proof, q.root, decision); err != nil {
		return out, err
	}
	out = uoRootLookup{turn: turn, reference: q, owner: owner, decision: decision, resultCount: old.slot.removedCount, appliedMS: old.slot.appliedMS}
	if decision == receiptReplay {
		out.payloadBytes = old.payloadBytes
		out.variableBytes = old.payloadBytes + uoRootFixedBytes
	}
	return out, nil
}

// uoRootSlotEnvelope is the exact control_variable_bytes contribution of a
// populated fixed slot before its payload: root 22 + command incarnation 16 +
// operation 16 + request digest 32 + result digest 32.
const uoRootSlotEnvelope = uoRootFixedBytes

type uoRootStoredSlot struct {
	slot         wcReclaimSlot
	payloadBytes int64
	payload      []byte
	digest       [32]byte
	controlBytes int64
}

// loadUORootSlot reads the complete fixed slot in ≤4096 bytes. A zero sequence
// means the slot is empty (38 control bytes); any partially populated slot is
// Resolve-required rather than decoded as an older result.
func loadUORootSlot(ctx context.Context, turn *commandTurn, q uoRootReference) (out uoRootStoredSlot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, uoRootOldSlotOctets, q.root, q.identity.Incarnation[:])
	if err != nil {
		return out, err
	}
	var seq, opBytes, requestBytes, digestBytes, payloadBytes, controlBytes int64
	if err = row.Scan(&seq, &opBytes, &requestBytes, &digestBytes, &payloadBytes, &controlBytes); err != nil {
		return out, err
	}
	if seq == 0 {
		if opBytes != 0 || requestBytes != 0 || digestBytes != 0 || payloadBytes != 0 || controlBytes != 38 {
			return out, ErrResolveRequired
		}
		return uoRootStoredSlot{controlBytes: 38}, nil
	}
	if seq < 0 || opBytes != 16 || requestBytes != 32 || digestBytes != 32 || payloadBytes < 1 || payloadBytes > uoRootReclaimPayloadCap || controlBytes != uoRootSlotEnvelope+payloadBytes {
		return out, ErrResolveRequired
	}
	row, err = wc008DraftQuery(ctx, turn, uoRootOldSlotPoint, q.root, q.identity.Incarnation[:], seq, payloadBytes)
	if err != nil {
		return out, err
	}
	var op, request, digest, payload []byte
	var kind, version, count, floor, oldCursor, removedSequence, removedBytes, nextCursor, noNext, appliedMS int64
	if err = row.Scan(&op, &request, &digest, &payload, &kind, &version, &count, &floor, &oldCursor, &removedSequence, &removedBytes, &nextCursor, &noNext, &appliedMS); err != nil {
		return out, err
	}
	if len(op) != 16 || len(request) != 32 || len(digest) != 32 || int64(len(payload)) != payloadBytes || kind != int64(RootReceiptReclaim) || version != 1 {
		return out, ErrResolveRequired
	}
	i := CommandIdentity{Incarnation: q.identity.Incarnation, Sequence: seq}
	copy(i.OperationID[:], op)
	var req RequestDigest
	copy(req[:], request)
	var sum ResultDigest
	copy(sum[:], digest)
	v, err := decodeUORootReclaim(i, req, q.root, payload, sum)
	if err != nil {
		return out, err
	}
	if v.removedCount != count || v.floor != floor || v.oldCursor != oldCursor || v.removedSequence != removedSequence || v.removedBytes != removedBytes || v.nextCursor != nextCursor || v.noNext != (noNext == 1) || noNext < 0 || noNext > 1 || v.appliedMS != appliedMS {
		return out, ErrResolveRequired
	}
	out = uoRootStoredSlot{slot: wcReclaimSlot{present: true, reference: ReceiptReference{Identity: i, RequestDigest: req}, resultDigest: [32]byte(sum), floor: v.floor, oldCursor: v.oldCursor, removedSequence: v.removedSequence, removedCount: v.removedCount, removedBytes: v.removedBytes, nextCursor: v.nextCursor, noNext: v.noNext, appliedMS: v.appliedMS}, payloadBytes: payloadBytes, digest: [32]byte(sum), controlBytes: controlBytes}
	out.payload = append([]byte(nil), payload...)
	return out, turn.checkLifetimes(ctx)
}

// restoreUORootReclaim returns the retained ORIGINAL from the fixed slot. It
// never re-runs the cleanup, deletes a child or moves debt.
func restoreUORootReclaim(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, lookup uoRootLookup) (out uoRootReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	q := lookup.reference
	if lookup.turn != turn || q.kind != RootReceiptReclaim || lookup.decision != receiptReplay {
		return out, ErrInvalid
	}
	if _, err = uoRootProofCurrent(proof, turn); err != nil {
		return out, err
	}
	stored, err := loadUORootSlot(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if !stored.slot.present || stored.slot.reference.Identity.Sequence != q.identity.Sequence || stored.slot.reference.Identity.Incarnation != q.identity.Incarnation || stored.slot.reference.Identity.OperationID != q.identity.OperationID || stored.slot.reference.RequestDigest != q.request {
		return out, ErrResolveRequired
	}
	result, err := decodeUORootReclaim(q.identity, q.request, q.root, stored.payload, stored.digest)
	if err != nil {
		return out, err
	}
	if result.removedCount != lookup.resultCount || result.appliedMS != lookup.appliedMS {
		return out, ErrResolveRequired
	}
	if err = uoRootReclaimRelease(ctx, turn, proof, q.root, receiptReplay); err != nil {
		return out, err
	}
	return result, turn.checkLifetimes(ctx)
}

// probeUORootChild observes the bounded first child in (after, ACK]. It attests
// nothing about current authority and never skips an unknown sequence.
func probeUORootChild(ctx context.Context, turn *commandTurn, q uoRootReference, after, through int64) (sequence, bytes int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct{ s, b int64 }{sequence, bytes}, err)
		sequence, bytes, err = v.s, v.b, e
	}()
	row, err := wc008DraftQuery(ctx, turn, uoRootChildNext, q.root, q.identity.Incarnation[:], after, through)
	if err != nil {
		return 0, 0, err
	}
	err = row.Scan(&sequence, &bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if sequence <= after || sequence > through || bytes <= uoRootFixedBytes {
		return 0, 0, ErrResolveRequired
	}
	return sequence, bytes, turn.checkLifetimes(ctx)
}

func uoRootReclaimProbe(root string, incarnation [16]byte, after, through, next int64) wcReclaimProbe {
	return wcReclaimProbe{playbackID: root, incarnation: incarnation, afterSequence: after, throughACK: through, nextSequence: next}
}

// reclaimUORootReceipt performs exactly one reviewed WC reclamation step on the
// root graph. ACK itself never refunds; this command deletes at most one ACKed
// child and refunds ONLY its actual native variable bytes. The refund and the
// new owner debt are validated before anything is deleted (charge before
// growth), and the complete original result is saved in the fixed slot.
//
// "Charge" is arithmetic only: W's global root-receipt quota is still open in
// the contract, so no quota row is debited or reserved here. prepareWCReclaim
// proves the refund/debt delta and this file must not invent a charge.
func reclaimUORootReceipt(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, lookup uoRootLookup, nowMS int64) (out uoRootReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	q, o := lookup.reference, lookup.owner
	if lookup.turn != turn || q.kind != RootReceiptReclaim || lookup.decision != receiptFresh || nowMS <= 0 || o.revision == math.MaxInt64 || o.rows == math.MaxInt64 || o.variableBytes == math.MaxInt64 {
		return out, ErrInvalid
	}
	if err = uoRootReclaimRelease(ctx, turn, proof, q.root, receiptFresh); err != nil {
		return out, err
	}
	if !o.due.Valid || o.due.Int64 > nowMS {
		return out, ErrConflict
	}
	old, err := loadUORootSlot(ctx, turn, q)
	if err != nil {
		return out, err
	}
	first, bytes, err := probeUORootChild(ctx, turn, q, o.afterSequence, o.facts.acknowledgedSequence)
	if err != nil {
		return out, err
	}
	owner := wcReclaimOwner{playbackID: q.root, incarnation: q.identity.Incarnation, lastSequence: o.facts.lastSequence, acknowledgedSequence: o.facts.acknowledgedSequence, afterSequence: o.afterSequence, rows: o.rows, variableBytes: o.variableBytes, due: o.due.Valid, dueMS: o.due.Int64}
	in := wcReclaimInput{owner: owner, command: q.identity, expectedOldSlot: old.slot, oldSlot: old.slot,
		firstProbe: uoRootReclaimProbe(q.root, q.identity.Incarnation, o.afterSequence, o.facts.acknowledgedSequence, first)}
	in.probe = in.firstProbe
	if first > 0 {
		in.child = &wcReclaimChild{key: wcReclaimKey{playbackID: q.root, incarnation: q.identity.Incarnation, sequence: first}, variableBytes: bytes}
		next, _, e := probeUORootChild(ctx, turn, q, first, o.facts.acknowledgedSequence)
		if e != nil {
			return out, e
		}
		in.probe = uoRootReclaimProbe(q.root, q.identity.Incarnation, first, o.facts.acknowledgedSequence, next)
	}
	prepared, err := prepareWCReclaim(in)
	if err != nil {
		return out, err
	}
	out = uoRootReclaimResult{root: q.root, floor: o.facts.acknowledgedSequence, oldCursor: o.afterSequence, nextCursor: prepared.afterSequence, remainingRows: prepared.remainingRows, remainingBytes: prepared.remainingVariableBytes, noNext: prepared.noNext, appliedMS: nowMS}
	if prepared.remove {
		out.removedCount, out.removedSequence, out.removedBytes = 1, prepared.key.sequence, prepared.removedVariableBytes
	}
	if err = preflightUORootReclaim(out); err != nil {
		return uoRootReclaimResult{}, err
	}
	payload, digest, err := encodeUORootReclaim(q.identity, q.request, out)
	if err != nil {
		return uoRootReclaimResult{}, err
	}
	if prepared.remove {
		removed, e := wc008DraftInteger(ctx, turn, uoRootChildDelete, q.root, q.identity.Incarnation[:], prepared.key.sequence, o.afterSequence, o.facts.acknowledgedSequence, prepared.removedVariableBytes)
		if e != nil {
			return uoRootReclaimResult{}, e
		}
		if removed != prepared.removedVariableBytes {
			return uoRootReclaimResult{}, ErrResolveRequired
		}
	}
	var nextDue, oldDue, removedSequence any
	if o.due.Valid {
		oldDue = o.due.Int64
	}
	if !prepared.noNext {
		nextDue = o.due.Int64
	}
	if prepared.remove {
		removedSequence = prepared.key.sequence
	}
	oldSeq := int64(0)
	if old.slot.present {
		oldSeq = old.slot.reference.Identity.Sequence
	}
	saved, err := wc008DraftInteger(ctx, turn, uoRootReclaimSave, q.identity.Sequence, prepared.remainingRows, prepared.remainingVariableBytes, prepared.afterSequence, nextDue, o.revision+1, q.identity.Sequence, q.identity.OperationID[:], q.request[:], int64(RootReceiptReclaim), out.removedCount, digest[:], payload, out.floor, out.oldCursor, removedSequence, out.removedBytes, out.nextCursor, uoFlagInteger(out.noNext), out.appliedMS, q.root, q.identity.Incarnation[:], o.facts.lastSequence, o.facts.acknowledgedSequence, o.revision, o.rows, o.variableBytes, o.afterSequence, oldDue, oldSeq, old.controlBytes)
	if err != nil {
		return uoRootReclaimResult{}, err
	}
	if saved != q.identity.Sequence {
		return uoRootReclaimResult{}, ErrResolveRequired
	}
	// A revocation or root change during the effects cannot publish an Applied
	// body: the caller rolls the whole prefix back.
	if err = uoRootReclaimRelease(ctx, turn, proof, q.root, receiptFresh); err != nil {
		return uoRootReclaimResult{}, err
	}
	return out, turn.checkLifetimes(ctx)
}

// acknowledgeUORootReclaim verifies the complete retained original before
// moving only the ACK floor. It never refunds the child/slot debt.
func acknowledgeUORootReclaim(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, lookup uoRootLookup, nowMS int64) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(turn), floor, err) }()
	if lookup.reference.kind != RootReceiptReclaim {
		return 0, ErrInvalid
	}
	if lookup.decision == receiptReplay {
		if _, err := restoreUORootReclaim(ctx, turn, proof, lookup); err != nil {
			return 0, err
		}
	}
	return acknowledgeUORootVerified(ctx, turn, proof, lookup, nowMS)
}

// uoRootReclaimDue observes at most one due root through the partial
// user_mutation_command_reclaim_due index. It is a maintenance candidate probe,
// never an enumeration or a claimant-selected priority.
//
// It is UNPROOFED across principals: it returns any due root ID without a
// current-authority or worker proof. No sweeper may call it until Sol supplies
// the worker proof; today it is a bounded component probe only.
func uoRootReclaimDueRoot(ctx context.Context, turn *commandTurn, nowMS int64) (root string, dueMS int64, found bool, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			r string
			d int64
			f bool
		}{root, dueMS, found}, err)
		root, dueMS, found, err = v.r, v.d, v.f, e
	}()
	if nowMS <= 0 {
		return "", 0, false, ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, uoRootReclaimDue, nowMS)
	if err != nil {
		return "", 0, false, err
	}
	err = row.Scan(&root, &dueMS)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, turn.checkLifetimes(ctx)
	}
	if err != nil {
		return "", 0, false, err
	}
	if len(root) != 22 || dueMS < 0 || dueMS > nowMS {
		return "", 0, false, ErrResolveRequired
	}
	return root, dueMS, true, turn.checkLifetimes(ctx)
}

// uoRootReclaimInvocation is one cleanup or ACK request. The reference is a
// comparison value supplied by the caller's retry custody, never proof.
type uoRootReclaimInvocation struct {
	reference uoRootReference
	nowMS     int64
	ack       bool
}

// uoRootReclaimPublished is the pre-terminal release packet of the closed
// dispatch entry.
type uoRootReclaimPublished struct {
	out   uoRootReclaimResult
	floor int64
}

// runUORootReclaimDispatch is the closed test/harness entry for 654. It mirrors
// runUSPolicyDispatch on main: the body is released through finishCommand
// BEFORE the physical terminal (which cancels its own lifetime), a release-time
// denial REPLACES the body and rolls the prefix back, an unknown COMMIT is
// Resolve-required with zero output, and a final deferred publication on the
// CALLER's context withdraws a result that committed before a late cancel.
func runUORootReclaimDispatch(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, inv uoRootReclaimInvocation) (out uoRootReclaimResult, floor int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, ctx, uoRootReclaimPublished{out, floor}, err)
		out, floor, err = v.out, v.floor, e
	}()
	lookup, err := lookupUORootReclaim(ctx, turn, proof, inv.reference)
	if err != nil {
		return withdrawUORootReclaim(turn, err)
	}
	if inv.ack {
		floor, err = acknowledgeUORootReclaim(ctx, turn, proof, lookup, inv.nowMS)
	} else {
		switch lookup.decision {
		case receiptFresh:
			out, err = reclaimUORootReceipt(ctx, turn, proof, lookup, inv.nowMS)
		case receiptReplay:
			out, err = restoreUORootReclaim(ctx, turn, proof, lookup)
		default:
			out, err = uoRootReclaimResult{}, ErrResolveRequired
		}
	}
	if err != nil {
		return withdrawUORootReclaim(turn, err)
	}
	// Final current authority/root before the terminal. A release-time denial
	// replaces the body outcome and rolls back the whole prefix.
	if out != (uoRootReclaimResult{}) {
		decision := receiptFresh
		if lookup.decision == receiptReplay {
			decision = receiptReplay
		}
		if err = uoRootReclaimRelease(ctx, turn, proof, inv.reference.root, decision); err != nil {
			return withdrawUORootReclaim(turn, err)
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return withdrawUORootReclaim(turn, ctxErr)
	}
	release, releaseErr := finishCommand(ctx, commandLifetime(turn), uoRootReclaimPublished{out, floor}, nil)
	if releaseErr != nil {
		return withdrawUORootReclaim(turn, releaseErr)
	}
	out, floor = release.out, release.floor
	if rowsErr := turn.closeRows(); rowsErr != nil {
		return withdrawUORootReclaim(turn, rowsErr)
	}
	physical, commitErr := turn.transaction.commit()
	switch physical.state {
	case committed:
		// finishCommand's release rule: no error, including a late cancel or a
		// committed safety overrun, may carry output from the private frame.
		if commitErr != nil {
			return uoRootReclaimResult{}, 0, commitErr
		}
		return out, floor, nil
	case commitUnknown:
		_, _ = turn.transaction.rollback(commitErr)
		return uoRootReclaimResult{}, 0, errors.Join(ErrResolveRequired, commitErr)
	default:
		_, _ = turn.transaction.rollback(commitErr)
		if commitErr == nil {
			commitErr = ErrResolveRequired
		}
		return uoRootReclaimResult{}, 0, commitErr
	}
}

func withdrawUORootReclaim(turn *commandTurn, err error) (uoRootReclaimResult, int64, error) {
	if turn != nil && turn.transaction != nil {
		_, _ = turn.transaction.rollback(err)
	}
	return uoRootReclaimResult{}, 0, err
}
