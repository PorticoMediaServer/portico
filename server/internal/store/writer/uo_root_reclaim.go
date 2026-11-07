package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

type uoRootStoredSlot struct {
	slot         wcReclaimSlot
	controlBytes int64
}

func restoreUORootReclaim(ctx context.Context, turn *commandTurn, lookup uoRootLookup) (out uoRootReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if lookup.reference.kind != RootReceiptReclaim {
		return out, ErrInvalid
	}
	p, d, err := readUORootPayload(ctx, turn, lookup)
	if err != nil {
		return out, err
	}
	q := lookup.reference
	out, err = decodeUORootReclaim(q.identity, q.request, q.root, p, d)
	if err != nil {
		return uoRootReclaimResult{}, err
	}
	if out.removedCount != lookup.resultCount || out.appliedMS != lookup.appliedMS {
		return uoRootReclaimResult{}, ErrResolveRequired
	}
	return out, turn.checkLifetimes(ctx)
}
func acknowledgeUORootReclaim(ctx context.Context, turn *commandTurn, lookup uoRootLookup, nowMS int64) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(turn), floor, err) }()
	if lookup.reference.kind != RootReceiptReclaim {
		return 0, ErrInvalid
	}
	if lookup.decision == receiptReplay {
		if _, err := restoreUORootReclaim(ctx, turn, lookup); err != nil {
			return 0, err
		}
	}
	return acknowledgeUORootVerified(ctx, turn, lookup, nowMS)
}

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
	if seq < 0 || opBytes != 16 || requestBytes != 32 || digestBytes != 32 || payloadBytes < 1 || payloadBytes > 4096 || controlBytes != payloadBytes+118 {
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
	return uoRootStoredSlot{controlBytes: controlBytes, slot: wcReclaimSlot{present: true, reference: ReceiptReference{Identity: i, RequestDigest: req}, resultDigest: [32]byte(sum), floor: v.floor, oldCursor: v.oldCursor, removedSequence: v.removedSequence, removedCount: v.removedCount, removedBytes: v.removedBytes, nextCursor: v.nextCursor, noNext: v.noNext, appliedMS: v.appliedMS}}, turn.checkLifetimes(ctx)
}

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
	if sequence <= after || sequence > through || bytes <= 118 {
		return 0, 0, ErrResolveRequired
	}
	return sequence, bytes, turn.checkLifetimes(ctx)
}

// One acknowledged source child, not an independent janitor/protocol. WC's
// reviewed pure reclamation transition checks the exact old slot, floor, cursor
// and both bounded range probes. W owns current disclosure, quota delta and the
// SAME parent commit. ACK itself never refunds this debt or the fixed slot.
func reclaimUORootReceipt(ctx context.Context, turn *commandTurn, lookup uoRootLookup, nowMS int64) (out uoRootReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	q, o := lookup.reference, lookup.owner
	if lookup.turn != turn || q.kind != RootReceiptReclaim || lookup.decision != receiptFresh || nowMS <= 0 || o.revision == math.MaxInt64 {
		return out, ErrInvalid
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
	probe := func(after, next int64) wcReclaimProbe {
		return wcReclaimProbe{playbackID: q.root, incarnation: q.identity.Incarnation, afterSequence: after, throughACK: o.facts.acknowledgedSequence, nextSequence: next}
	}
	in := wcReclaimInput{owner: owner, command: q.identity, expectedOldSlot: old.slot, oldSlot: old.slot, firstProbe: probe(o.afterSequence, first), probe: probe(o.afterSequence, first)}
	if first > 0 {
		in.child = &wcReclaimChild{key: wcReclaimKey{playbackID: q.root, incarnation: q.identity.Incarnation, sequence: first}, variableBytes: bytes}
		next, _, e := probeUORootChild(ctx, turn, q, first, o.facts.acknowledgedSequence)
		if e != nil {
			return out, e
		}
		in.probe = probe(first, next)
	}
	prepared, err := prepareWCReclaim(in)
	if err != nil {
		return out, err
	}
	out = uoRootReclaimResult{root: q.root, floor: o.facts.acknowledgedSequence, oldCursor: o.afterSequence, nextCursor: prepared.afterSequence, remainingRows: prepared.remainingRows, remainingBytes: prepared.remainingVariableBytes, noNext: prepared.noNext, appliedMS: nowMS}
	if prepared.remove {
		out.removedCount, out.removedSequence, out.removedBytes = 1, prepared.key.sequence, prepared.removedVariableBytes
	}
	payload, digest, err := encodeUORootReclaim(q.identity, q.request, out)
	if err != nil {
		return uoRootReclaimResult{}, err
	}
	if prepared.remove {
		removed, e := wc008DraftInteger(ctx, turn, uoRootChildDelete, q.root, q.identity.Incarnation[:], prepared.key.sequence, prepared.removedVariableBytes)
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
	return out, turn.checkLifetimes(ctx)
}
