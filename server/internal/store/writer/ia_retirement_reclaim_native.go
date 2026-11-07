package writer

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type iaRetirementGCNativeSlot struct {
	due                                                             sql.NullInt64
	sequence, version, floor, old, removed, next                    sql.NullInt64
	removedCount, removedBytes, noNext, rows, bytes, applied, count sql.NullInt64
	operation, request, digest                                      []byte
	kind, removedKind                                               sql.NullString
	charge                                                          int64
}

func (r *iaRetirementGCNativeSlot) destinations() []any {
	return []any{&r.due, &r.sequence, &r.operation, &r.request, &r.kind, &r.version, &r.floor, &r.old, &r.removed, &r.removedKind,
		&r.removedCount, &r.removedBytes, &r.next, &r.noNext, &r.rows, &r.bytes, &r.applied, &r.count, &r.digest, &r.charge}
}

func (r iaRetirementGCNativeSlot) metadata(inc [16]byte) (iaReceiptReclaimSlot, error) {
	if r.operation == nil {
		return iaReceiptReclaimSlot{}, nil
	}
	if !r.sequence.Valid || len(r.operation) != 16 || len(r.request) != 32 || len(r.digest) != 32 || !r.kind.Valid || r.kind.String != "retirement_receipt_reclaim" {
		return iaReceiptReclaimSlot{}, ErrResolveRequired
	}
	s := iaReceiptReclaimSlot{present: true, identity: CommandIdentity{Incarnation: inc, Sequence: r.sequence.Int64}, purpose: IARetirementReceiptReclaim, version: r.version.Int64, resultCount: r.count.Int64, retainedBytes: r.charge}
	copy(s.identity.OperationID[:], r.operation)
	copy(s.request[:], r.request)
	copy(s.digest[:], r.digest)
	if !r.count.Valid {
		s.resultCount = -1
	}
	return s, nil
}

// Body decode is deferred until AFTER original metadata/ACK classification.
// Empty slot checks are required only for fresh replacement, not old output.
func (r iaRetirementGCNativeSlot) decode(inc [16]byte) (iaReceiptReclaimSlot, error) {
	s, err := r.metadata(inc)
	if err != nil {
		return s, err
	}
	mandatory := []sql.NullInt64{r.sequence, r.version, r.floor, r.old, r.removedCount, r.removedBytes, r.next, r.noNext, r.rows, r.bytes, r.applied, r.count}
	if !s.present {
		for _, n := range append(mandatory, r.removed) {
			if n.Valid {
				return iaReceiptReclaimSlot{}, ErrResolveRequired
			}
		}
		if r.request != nil || r.digest != nil || r.kind.Valid || r.removedKind.Valid || r.charge != 0 {
			return iaReceiptReclaimSlot{}, ErrResolveRequired
		}
		return s, nil
	}
	for _, n := range mandatory {
		if !n.Valid {
			return iaReceiptReclaimSlot{}, ErrResolveRequired
		}
	}
	if !r.removedKind.Valid || r.removed.Valid != (r.removedCount.Int64 == 1) || r.noNext.Int64 < 0 || r.noNext.Int64 > 1 {
		return iaReceiptReclaimSlot{}, ErrResolveRequired
	}
	s.facts = iaReceiptReclaimResult{capturedACK: r.floor.Int64, old: iaReceiptCursor{r.old.Int64, -1}, next: iaReceiptCursor{r.next.Int64, -1}, removedKind: r.removedKind.String,
		removedSequence: r.removed.Int64, removedOrdinal: -1, removedCount: r.removedCount.Int64, removedBytes: r.removedBytes.Int64, rows: r.rows.Int64, bytes: r.bytes.Int64, noNext: r.noNext.Int64 == 1, appliedMS: r.applied.Int64}
	return s, nil
}

type iaReceiptReclaimNativeHistory struct {
	decision receiptDecision
	facts    iaReceiptReclaimResult
	digest   ResultDigest
	owner    iaReceiptReclaimOwner
}

func iaRetirementGCReference(q iaReceiptReclaimReference) iaRetirementReference {
	o := q.retirement
	return iaRetirementReference{kind: o.kind, target: o.target, libraryID: o.libraryID, libraryKey: o.libraryKey, originalOperation: o.operationID, identity: q.identity, request: q.request, purpose: IARetireStep}
}

// Private106 adapter. CURRENT retained-owner maintenance/disclosure authority
// and actual native/copy byte admission must precede entry. This is not their
// issuer, an Applied result, a new lease, or permission to delete live data.
func lookupIARetirementReceiptReclaim(ctx context.Context, turn *commandTurn, q iaReceiptReclaimReference) (value iaReceiptReclaimNativeHistory, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if q.purpose != IARetirementReceiptReclaim || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IARetirementReceiptReclaim {
		return value, ErrInvalid
	}
	if err = validateIAReceiptReclaimReference(q); err != nil {
		return
	}
	ref := iaRetirementGCReference(q)
	base, e := loadIARetirementOwner(ctx, turn, ref)
	if e != nil {
		err = e
		return
	}
	o := iaReceiptReclaimOwner{exists: base.exists, incarnation: base.incarnation, retirement: base.original, last: base.last, acknowledged: base.acknowledged,
		cursor: iaReceiptCursor{base.reclaimed, -1}, rows: base.retainedCount, bytes: base.retainedBytes}
	if !base.exists {
		_, err = classifyIAReceiptReclaimMetadata(q, o)
		return
	}
	if !iaReceiptReclaimScopeMatches(q, o) {
		err = ErrConflict
		return
	}
	// BEGIN/STEP and GC share this actual owner's stream. A retained data
	// header at the requested sequence proves a DIFFERENT compiled purpose,
	// even below ACK. Classify it before looking at/replaying the fixed slot.
	var data int64
	err = iaRetirementPoint(ctx, turn, iaRetirementGCDataConflict, append(iaRetirementGCChildKey(q), q.identity.Sequence), &data)
	if err == nil {
		err = ErrConflict
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return
	}
	err = nil
	var raw iaRetirementGCNativeSlot
	err = iaRetirementPoint(ctx, turn, iaRetirementGCSlot, iaRetirementKey(ref), raw.destinations()...)
	if err != nil {
		return
	}
	o.due, o.dueMS = raw.due.Valid, raw.due.Int64
	o.slot, err = raw.metadata(o.incarnation)
	if err != nil {
		return
	}
	decision, e := classifyIAReceiptReclaimMetadata(q, o)
	if e != nil {
		err = e
		return
	}
	if decision == receiptRetired {
		value = iaReceiptReclaimNativeHistory{decision: decision, owner: o}
		return
	}
	o.slot, err = raw.decode(o.incarnation)
	if err != nil {
		return
	}
	if decision == receiptReplay {
		_, err = classifyIAReceiptReclaim(q, o)
		if err != nil {
			return
		}
		value = iaReceiptReclaimNativeHistory{decision: decision, owner: o, facts: o.slot.facts, digest: o.slot.digest}
		return
	}
	value = iaReceiptReclaimNativeHistory{decision: decision, owner: o}
	return
}

// GC has no physical worker lease. Use the existing admitted W frame's clock,
// high-water and protected deadline; never accept a prepared future timestamp.
func (turn *commandTurn) iaReceiptReclaimTime(ctx context.Context) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return 0, ErrInvalid
	}
	tx := turn.transaction
	if (tx.descriptor.ID != IAConfigurationReceiptReclaim && tx.descriptor.ID != IARetirementReceiptReclaim) || tx.writer == nil || tx.writer.now == nil || tx.ctx == nil || tx.conn == nil || tx.quantum == nil || tx.custody == nil {
		return 0, ErrInvalid
	}
	if tx.done || !tx.begun {
		return 0, ErrClosed
	}
	defer tx.guardPanic()
	if err = turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	deadline, ok := tx.ctx.Deadline()
	if !ok {
		return 0, ErrInvalid
	}
	now := tx.writer.now()
	value = now.UnixMilli()
	if value < 0 || !time.UnixMilli(value).Equal(now.Truncate(time.Millisecond)) {
		return 0, ErrInvalid
	}
	if err = tx.observeTime(now); err != nil {
		return 0, err
	}
	if !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	return value, nil
}

func iaRetirementGCChildKey(q iaReceiptReclaimReference) []any {
	return []any{q.retirement.operationID[:], q.identity.Incarnation[:], q.retirement.kind, q.retirement.target}
}

func firstIARetirementGCUnit(ctx context.Context, turn *commandTurn, q iaReceiptReclaimReference, o iaReceiptReclaimOwner, cursor iaReceiptCursor) (iaReceiptReclaimProbe, error) {
	p := iaReceiptReclaimProbe{incarnation: o.incarnation, originalOperation: o.retirement.operationID, after: cursor, throughACK: o.acknowledged}
	var sequence, bytes int64
	var kind string
	args := append(iaRetirementGCChildKey(q), cursor.sequence, o.acknowledged)
	err := iaRetirementPoint(ctx, turn, iaRetirementGCFirst, args, &sequence, &kind, &bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return iaReceiptReclaimProbe{}, err
	}
	want := "retire_step"
	if sequence == 1 {
		want = "retire_library"
		if o.retirement.kind == "folder" {
			want = "retire_source"
		}
	}
	if kind != want {
		return iaReceiptReclaimProbe{}, ErrResolveRequired
	}
	p.unit = iaReceiptReclaimUnit{kind: "header", sequence: sequence, ordinal: -1, bytes: bytes}
	return p, nil
}

func iaRetirementGCScopeValues(q iaReceiptReclaimReference) []any {
	o := q.retirement
	values := []any{o.kind, o.target, o.operationID[:], q.identity.Incarnation[:], o.requestDigest[:], o.libraryID, o.libraryKey, o.resolvedRoot}
	if o.kind == "folder" {
		values = append(values, o.sourceIncarnation, o.sourceGeneration, o.rootIdentity)
	}
	return values
}

func iaRetirementGCSlotValues(s iaReceiptReclaimSlot) []any {
	v := s.facts
	var removed any
	if v.removedCount == 1 {
		removed = v.removedSequence
	}
	var noNext int64
	if v.noNext {
		noNext = 1
	}
	return []any{s.identity.Sequence, s.identity.OperationID[:], s.request[:], "retirement_receipt_reclaim", s.version, v.capturedACK, v.old.sequence, removed, v.removedKind, v.removedCount, v.removedBytes,
		v.next.sequence, noNext, v.rows, v.bytes, v.appliedMS, s.resultCount, s.digest[:]}
}

func iaRetirementGCStoreArguments(q iaReceiptReclaimReference, o iaReceiptReclaimOwner, slot iaReceiptReclaimSlot) (int, []any) {
	v := slot.facts
	var due any
	if !v.noNext {
		due = v.appliedMS
	}
	args := append([]any{q.identity.Sequence, v.next.sequence, v.rows, v.bytes, due}, iaRetirementGCSlotValues(slot)...)
	args = append(args, iaRetirementGCScopeValues(q)...)
	args = append(args, o.last, o.acknowledged, o.cursor.sequence, o.rows, o.bytes, o.dueMS, o.slot.retainedBytes)
	shape := 0
	if o.slot.present {
		shape = 1
		if o.slot.facts.removedCount == 1 {
			shape = 2
		}
		old := iaRetirementGCSlotValues(o.slot)
		for i, x := range old {
			if shape == 1 && iaRetirementGCColumns[i].Nullable {
				continue
			}
			args = append(args, x)
		}
	}
	return shape, args
}

// One exact scalar DELETE plus a full OLD scope/slot/debt/cursor/due CAS share
// the caller's SAME W transaction. There is no cleanup child/header fan-out.
// Unknown COMMIT must reopen this same reference through lookup, never execute
// a missing-child guess as a second zero-removal result. Terminal COMMIT truth
// and actual resource/admission profiles remain the sole dispatcher's job.
func applyIARetirementReceiptReclaim(ctx context.Context, turn *commandTurn, q iaReceiptReclaimReference, expectedOldSlot iaReceiptReclaimSlot) (value iaReceiptReclaimNativeHistory, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	h, err := lookupIARetirementReceiptReclaim(ctx, turn, q)
	if err != nil {
		return value, err
	}
	if h.decision != receiptFresh {
		return h, nil
	}
	o := h.owner
	applied, err := turn.iaReceiptReclaimTime(ctx)
	if err != nil {
		return value, err
	}
	first, err := firstIARetirementGCUnit(ctx, turn, q, o, o.cursor)
	if err != nil {
		return value, err
	}
	nextCursor := o.cursor
	if first.unit.kind != "" {
		nextCursor = iaReceiptCursor{first.unit.sequence, -1}
	}
	after, err := firstIARetirementGCUnit(ctx, turn, q, o, nextCursor)
	if err != nil {
		return value, err
	}
	v, err := prepareIAReceiptReclaim(iaReceiptReclaimInput{reference: q, owner: o, expectedOldSlot: expectedOldSlot, first: first, after: after, appliedMS: applied})
	if err != nil {
		return value, err
	}
	digest, err := canonicalResultDigest(q.identity, q.request, iaRetirementReceiptReclaimResult{q.retirement, v})
	if err != nil {
		return value, err
	}
	slot := iaReceiptReclaimSlot{present: true, identity: q.identity, request: q.request, purpose: IARetirementReceiptReclaim, version: 1, resultCount: 1, facts: v, digest: digest}
	slot.retainedBytes, err = iaReceiptReclaimSlotBytes(q.purpose, v)
	if err != nil {
		return value, err
	}
	if v.removedCount == 1 {
		kind := "retire_step"
		if v.removedSequence == 1 {
			kind = "retire_library"
			if q.retirement.kind == "folder" {
				kind = "retire_source"
			}
		}
		args := append(iaRetirementGCChildKey(q), v.removedSequence, kind, v.removedBytes)
		var actual int64
		if err = iaRetirementPoint(ctx, turn, iaRetirementGCDelete, args, &actual); err != nil {
			return value, err
		}
		if actual != v.removedBytes {
			return value, ErrResolveRequired
		}
	}
	shape, args := iaRetirementGCStoreArguments(q, o, slot)
	var charge int64
	err = iaRetirementPoint(ctx, turn, iaRetirementGCStoreID(q.retirement.kind == "folder", shape), args, &charge)
	if err != nil {
		return value, err
	}
	if charge != slot.retainedBytes {
		return value, ErrResolveRequired
	}
	o.last, o.cursor, o.rows, o.bytes, o.due, o.dueMS, o.slot = q.identity.Sequence, v.next, v.rows, v.bytes, !v.noNext, 0, slot
	if o.due {
		o.dueMS = applied
	}
	return iaReceiptReclaimNativeHistory{decision: receiptFresh, facts: v, digest: digest, owner: o}, nil
}

func acknowledgeIARetirementReceiptReclaim(ctx context.Context, turn *commandTurn, q iaReceiptReclaimReference) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	h, err := lookupIARetirementReceiptReclaim(ctx, turn, q)
	if err != nil {
		return 0, err
	}
	o := h.owner
	if h.decision == receiptRetired {
		return o.acknowledged, nil
	}
	if h.decision != receiptReplay || q.identity.Sequence != o.last {
		return 0, ErrResolveRequired
	}
	args := append([]any{q.identity.Sequence}, iaRetirementGCScopeValues(q)...)
	args = append(args, o.last, o.acknowledged, o.slot.identity.Sequence, o.slot.identity.OperationID[:], o.slot.request[:], o.slot.digest[:], o.slot.retainedBytes)
	if err = iaRetirementOneEffect(ctx, turn, iaRetirementGCAckID(q.retirement.kind == "folder"), args...); err != nil {
		return 0, err
	}
	return q.identity.Sequence, nil
}
