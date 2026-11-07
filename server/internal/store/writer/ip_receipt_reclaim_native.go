package writer

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"math"
)

type ipReclaimOwner struct {
	facts  receiptOwnerFacts
	cursor int64
	due    sql.NullInt64
	slot   *ipReceiptReclaimSlot
}

func ipReclaimNullableSlot(s *ipReceiptReclaimSlot) []any {
	if s == nil {
		return make([]any, 11)
	}
	r := s.Value
	return []any{s.Identity.Sequence, s.Identity.OperationID[:], s.Request[:], r.AppliedMS, s.Digest[:], r.CapturedACK, r.OldCursor, r.RemovedRows, r.RemovedPayloadBytes, r.AfterSequence, ipTextBool(r.NoNext)}
}
func ipReclaimOwnerRead(ctx context.Context, t *commandTurn, scope ipRetentionScope, statements ipReclaimStatements, keys []any) (out ipReclaimOwner, err error) {
	rows, err := ipTextRows(ctx, t, statements.owner, keys...)
	if err != nil {
		return out, err
	}
	var inc, op, request, result []byte
	var sequence, applied, ack, old, count, octets, cursor, noNext sql.NullInt64
	err = ipTextOne(rows, func() error {
		return rows.Scan(&inc, &out.facts.lastSequence, &out.facts.acknowledgedSequence, &out.cursor, &out.due, &sequence, &op, &request, &applied, &result, &ack, &old, &count, &octets, &cursor, &noNext)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrOwnerUnavailable
	}
	if err != nil {
		return out, err
	}
	if len(inc) != 16 || out.cursor < 0 || out.cursor > out.facts.acknowledgedSequence || out.due.Valid && out.due.Int64 < 0 {
		return out, ErrResolveRequired
	}
	out.facts.exists = true
	copy(out.facts.incarnation[:], inc)
	numbers := []sql.NullInt64{applied, ack, old, count, octets, cursor, noNext}
	for _, n := range numbers {
		if n.Valid != sequence.Valid {
			return out, ErrResolveRequired
		}
	}
	if !sequence.Valid {
		if op != nil || request != nil || result != nil {
			return out, ErrResolveRequired
		}
		return out, nil
	}
	if len(op) != 16 || len(request) != 32 || len(result) != 32 || noNext.Int64 < 0 || noNext.Int64 > 1 {
		return out, ErrResolveRequired
	}
	slot := &ipReceiptReclaimSlot{Identity: CommandIdentity{Incarnation: out.facts.incarnation, Sequence: sequence.Int64}, Value: ipReceiptReclaimResult{scope.Family, ack.Int64, old.Int64, count.Int64, octets.Int64, cursor.Int64, applied.Int64, noNext.Int64 == 1}}
	copy(slot.Identity.OperationID[:], op)
	copy(slot.Request[:], request)
	copy(slot.Digest[:], result)
	out.slot = slot
	return out, nil
}
func ipReclaimSourceFacts(ctx context.Context, t *commandTurn, set ipReclaimStatements, keys []any, i CommandIdentity) (out receiptFacts, err error) {
	args := append(append([]any(nil), keys...), i.Incarnation[:], i.Sequence)
	rows, err := ipTextRows(ctx, t, set.source, args...)
	if err != nil {
		return out, err
	}
	var op, request, result []byte
	var kind int64
	err = ipTextOne(rows, func() error { return rows.Scan(&op, &request, &result, &kind) })
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if len(op) != 16 || len(request) != 32 || len(result) != 32 || kind <= 0 {
		return out, ErrResolveRequired
	}
	out.exists = true
	out.identity.command = i
	copy(out.identity.command.OperationID[:], op)
	copy(out.identity.digest[:], request)
	copy(out.resultDigest[:], result)
	out.kind = CommandID(kind)
	out.resultCount = 1
	return out, nil
}

// One source receipt per quantum, with at most eleven finite field-result
// children. No source/history/material body is retired by this command. Native
// actual octet lengths precede deletion and are compared again in DELETE;
// returned payload bytes/rows are the only logical debt release observations.
// Physical WAL/page/disk custody and current retained-owner maintenance
// authority are outer W responsibilities. This entry never returns Applied.
func applyIPReceiptReclaim(ctx context.Context, t *commandTurn, c ipReceiptReclaimCommand, appliedMS int64) (out ipReceiptReclaimNative, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	q := c.request
	i := q.Identity
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != IPReceiptReclaim || appliedMS < 0 {
		return out, ErrInvalid
	}
	checked, e := newIPReceiptReclaimCommand(q)
	if e != nil || checked.digest != c.digest {
		return out, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	keys, e := q.Scope.keys()
	if e != nil {
		return out, e
	}
	set, e := ipReclaimStatementSet(q.Scope.Family)
	if e != nil {
		return out, e
	}
	owner, e := ipReclaimOwnerRead(ctx, t, q.Scope, set, keys)
	if e != nil {
		return out, e
	}
	var found receiptFacts
	if owner.slot != nil && owner.slot.Identity.Sequence == i.Sequence {
		slot := owner.slot
		found = receiptFacts{exists: true, identity: receiptIdentity{slot.Identity, slot.Request}, kind: IPReceiptReclaim, resultCount: 1, resultDigest: [32]byte(slot.Digest)}
	} else {
		found, e = ipReclaimSourceFacts(ctx, t, set, keys, i)
		if e != nil {
			return out, e
		}
	}
	decision, e := classifyReceipt(receiptIdentity{i, c.digest}, IPReceiptReclaim, owner.facts, found)
	if e != nil {
		return out, e
	}
	out.decision = decision
	if decision == receiptRetired {
		return out, nil
	}
	if decision == receiptReplay {
		if owner.slot == nil || owner.slot.Identity != i || owner.slot.Request != c.digest {
			return ipReceiptReclaimNative{}, ErrResolveRequired
		}
		slot := owner.slot
		out.value = slot.Value
		out.digest, e = canonicalResultDigest(i, c.digest, out.value)
		if e != nil || out.digest != slot.Digest || out.value.CapturedACK != q.CapturedACK || out.value.OldCursor != q.AfterSequence {
			return ipReceiptReclaimNative{}, ErrResolveRequired
		}
		return out, nil
	}
	if decision != receiptFresh || q.CapturedACK != owner.facts.acknowledgedSequence || q.AfterSequence != owner.cursor || !owner.due.Valid || owner.due.Int64 > appliedMS {
		return ipReceiptReclaimNative{}, ErrConflict
	}
	if (owner.slot == nil) != (q.Previous == nil) {
		return ipReceiptReclaimNative{}, ErrConflict
	}
	if owner.slot != nil {
		old := owner.slot
		if *old != *q.Previous || old.Identity.Sequence > owner.facts.acknowledgedSequence {
			return ipReceiptReclaimNative{}, ErrConflict
		}
		digest, e := canonicalResultDigest(old.Identity, old.Request, old.Value)
		if e != nil || digest != old.Digest {
			return ipReceiptReclaimNative{}, ErrResolveRequired
		}
	}
	result := ipReceiptReclaimResult{Family: q.Scope.Family, CapturedACK: q.CapturedACK, OldCursor: q.AfterSequence, AfterSequence: q.AfterSequence, AppliedMS: appliedMS}
	args := append(append([]any(nil), keys...), i.Incarnation[:], q.AfterSequence, q.CapturedACK)
	rows, e := ipTextRows(ctx, t, set.first, args...)
	if e != nil {
		return ipReceiptReclaimNative{}, e
	}
	var sequence, octets, bundle, count, allowed int64
	e = ipTextOne(rows, func() error { return rows.Scan(&sequence, &octets, &bundle, &count, &allowed) })
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return ipReceiptReclaimNative{}, e
	}
	if errors.Is(e, sql.ErrNoRows) {
		result.NoNext = true
		result.AfterSequence = q.CapturedACK
	} else {
		if sequence <= q.AfterSequence || sequence > q.CapturedACK || octets <= 0 || bundle < 0 || bundle > 1 || allowed < 0 || allowed > 1 {
			return ipReceiptReclaimNative{}, ErrResolveRequired
		}
		if allowed == 1 {
			if q.Scope.Family == ipReceiptText {
				childRows, childBytes, e := ipReclaimTextChildren(ctx, t, q.Scope.OwnerID, i.Incarnation, sequence, bundle, count)
				if e != nil {
					return ipReceiptReclaimNative{}, e
				}
				result.RemovedRows = childRows
				result.RemovedPayloadBytes = childBytes
			} else if bundle != 0 {
				return ipReceiptReclaimNative{}, ErrResolveRequired
			}
			args = append(append([]any(nil), keys...), i.Incarnation[:], sequence, octets)
			removed, e := ipReclaimDeletePayload(ctx, t, set.remove, args...)
			if e != nil {
				return ipReceiptReclaimNative{}, e
			}
			if removed != octets || octets > math.MaxInt64-result.RemovedPayloadBytes {
				return ipReceiptReclaimNative{}, ErrResolveRequired
			}
			result.RemovedRows++
			result.RemovedPayloadBytes += octets
			result.AfterSequence = sequence
			args = append(append([]any(nil), keys...), i.Incarnation[:], sequence, q.CapturedACK)
			next, e := ipTextRows(ctx, t, set.next, args...)
			if e != nil {
				return ipReceiptReclaimNative{}, e
			}
			var following int64
			e = ipTextOne(next, func() error { return next.Scan(&following) })
			if errors.Is(e, sql.ErrNoRows) {
				result.NoNext = true
				result.AfterSequence = q.CapturedACK
			} else if e != nil {
				return ipReceiptReclaimNative{}, e
			} else if following <= sequence || following > q.CapturedACK {
				return ipReceiptReclaimNative{}, ErrResolveRequired
			}
		}
	}
	out.value = result
	out.digest, err = canonicalResultDigest(i, c.digest, result)
	if err != nil {
		return ipReceiptReclaimNative{}, err
	}
	var due any
	if !result.NoNext {
		nextDue := appliedMS
		if result.RemovedRows == 0 {
			if appliedMS > math.MaxInt64-1000 {
				return ipReceiptReclaimNative{}, ErrConflict
			}
			nextDue += 1000
		}
		due = nextDue
	}
	saved := ipReceiptReclaimSlot{i, c.digest, out.digest, result}
	args = []any{i.Sequence, result.AfterSequence, due}
	if q.Scope.Family == ipReceiptBulk {
		args = append(args, due)
	}
	args = append(args, ipReclaimNullableSlot(&saved)...)
	args = append(args, keys...)
	args = append(args, i.Incarnation[:], owner.facts.lastSequence, owner.facts.acknowledgedSequence, owner.cursor, owner.due.Int64)
	args = append(args, ipReclaimNullableSlot(owner.slot)...)
	if err = ipTextExpectOne(ctx, t, set.save, args...); err != nil {
		return ipReceiptReclaimNative{}, err
	}
	return out, t.checkLifetimes(ctx)
}
func ipReclaimDeletePayload(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (int64, error) {
	rows, err := ipTextRows(ctx, t, id, args...)
	if err != nil {
		return 0, err
	}
	var octets int64
	err = ipTextOne(rows, func() error { return rows.Scan(&octets) })
	if err != nil || octets <= 0 {
		return 0, errors.Join(ErrConflict, err)
	}
	return octets, nil
}
func ipReclaimTextChildren(ctx context.Context, t *commandTurn, owner int64, inc [16]byte, sequence, bundle, count int64) (removed, octets int64, err error) {
	rows, err := ipTextRows(ctx, t, ipRCTextChildren, owner, inc[:], sequence)
	if err != nil {
		return 0, 0, err
	}
	type child struct{ ordinal, bytes int64 }
	var children []child
	for rows.Next() {
		var c child
		if err = rows.Scan(&c.ordinal, &c.bytes); err != nil {
			break
		}
		if c.ordinal != int64(len(children)) || c.bytes <= 0 || len(children) >= 11 {
			err = ErrResolveRequired
			break
		}
		children = append(children, c)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return 0, 0, err
	}
	if bundle == 0 && len(children) != 0 || bundle == 1 && (count <= 0 || count != int64(len(children))) {
		return 0, 0, ErrResolveRequired
	}
	for _, c := range children {
		actual, e := ipReclaimDeletePayload(ctx, t, ipRCTextChildRemove, owner, inc[:], sequence, c.ordinal, c.bytes)
		if e != nil {
			return 0, 0, e
		}
		if actual != c.bytes || actual > math.MaxInt64-octets {
			return 0, 0, ErrResolveRequired
		}
		removed++
		octets += actual
	}
	return removed, octets, nil
}

// The complete fixed result is natively validated here before its ACK. This
// ACK changes only the floor; due state set by cleanup remains unchanged. A
// completed fixed slot creates no child debt and must never redue itself.
func acknowledgeIPReceiptReclaim(ctx context.Context, t *commandTurn, scope ipRetentionScope, reference ReceiptReference, expected ResultDigest) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(t), floor, err) }()
	i := reference.Identity
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != IPReceiptACK || !ipPolicyValidIdentity(i) || reference.RequestDigest == (RequestDigest{}) || expected == (ResultDigest{}) {
		return 0, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	keys, e := scope.keys()
	if e != nil {
		return 0, e
	}
	set, e := ipReclaimStatementSet(scope.Family)
	if e != nil {
		return 0, e
	}
	owner, e := ipReclaimOwnerRead(ctx, t, scope, set, keys)
	if e != nil {
		return 0, e
	}
	var found receiptFacts
	if owner.slot != nil && owner.slot.Identity.Sequence == i.Sequence {
		slot := owner.slot
		found = receiptFacts{exists: true, identity: receiptIdentity{slot.Identity, slot.Request}, kind: IPReceiptReclaim, resultCount: 1, resultDigest: [32]byte(slot.Digest)}
	} else {
		found, e = ipReclaimSourceFacts(ctx, t, set, keys, i)
		if e != nil {
			return 0, e
		}
	}
	decision, e := classifyReceipt(receiptIdentity{i, reference.RequestDigest}, IPReceiptReclaim, owner.facts, found)
	if e != nil {
		return 0, e
	}
	if decision == receiptRetired {
		return owner.facts.acknowledgedSequence, nil
	}
	if decision != receiptReplay || owner.slot == nil {
		return 0, ErrConflict
	}
	slot := owner.slot
	digest, e := canonicalResultDigest(slot.Identity, slot.Request, slot.Value)
	if e != nil || digest != slot.Digest || digest != expected {
		return 0, ErrResolveRequired
	}
	var due any
	if owner.due.Valid {
		due = owner.due.Int64
	}
	args := []any{i.Sequence}
	args = append(args, keys...)
	args = append(args, i.Incarnation[:], owner.facts.lastSequence, owner.facts.acknowledgedSequence, owner.cursor, due, i.Sequence, i.OperationID[:], reference.RequestDigest[:], expected[:])
	if err = ipTextExpectOne(ctx, t, set.ack, args...); err != nil {
		return 0, err
	}
	return i.Sequence, t.checkLifetimes(ctx)
}
