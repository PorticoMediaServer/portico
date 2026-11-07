package writer

import (
	"context"
	"database/sql"
	"errors"
)

type iaConfigurationGCNativeSlot struct {
	due                                                             sql.NullInt64
	sequence, version, floor, old, removed, next                    sql.NullInt64
	removedCount, removedBytes, noNext, rows, bytes, applied, count sql.NullInt64
	operation, request, digest                                      []byte
	kind, removedKind                                               sql.NullString
	charge                                                          int64
	oldOrdinal, removedOrdinal, nextOrdinal                         sql.NullInt64
}

func (r *iaConfigurationGCNativeSlot) destinations() []any {
	return []any{&r.due, &r.sequence, &r.operation, &r.request, &r.kind, &r.version, &r.floor, &r.old, &r.removed, &r.removedKind,
		&r.removedCount, &r.removedBytes, &r.next, &r.noNext, &r.rows, &r.bytes, &r.applied, &r.count, &r.digest, &r.oldOrdinal, &r.removedOrdinal, &r.nextOrdinal, &r.charge}
}

func (r iaConfigurationGCNativeSlot) metadata(inc [16]byte) (iaReceiptReclaimSlot, error) {
	if r.operation == nil {
		return iaReceiptReclaimSlot{}, nil
	}
	if !r.sequence.Valid || len(r.operation) != 16 || len(r.request) != 32 || len(r.digest) != 32 || !r.kind.Valid || r.kind.String != "configuration_receipt_reclaim" {
		return iaReceiptReclaimSlot{}, ErrResolveRequired
	}
	s := iaReceiptReclaimSlot{present: true, identity: CommandIdentity{Incarnation: inc, Sequence: r.sequence.Int64}, purpose: IAConfigurationReceiptReclaim, version: r.version.Int64, resultCount: r.count.Int64, retainedBytes: r.charge}
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
func (r iaConfigurationGCNativeSlot) decode(inc [16]byte) (iaReceiptReclaimSlot, error) {
	s, err := r.metadata(inc)
	if err != nil {
		return s, err
	}
	mandatory := []sql.NullInt64{r.sequence, r.version, r.floor, r.old, r.removedCount, r.removedBytes, r.next, r.noNext, r.rows, r.bytes, r.applied, r.count, r.oldOrdinal, r.nextOrdinal}
	if !s.present {
		for _, n := range append(mandatory, r.removed, r.removedOrdinal) {
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
	if !r.removedKind.Valid || r.removed.Valid != (r.removedCount.Int64 == 1) || r.noNext.Int64 < 0 || r.noNext.Int64 > 1 || r.removedOrdinal.Valid != (r.removedKind.String == "source") {
		return iaReceiptReclaimSlot{}, ErrResolveRequired
	}
	s.facts = iaReceiptReclaimResult{capturedACK: r.floor.Int64, old: iaReceiptCursor{r.old.Int64, r.oldOrdinal.Int64}, next: iaReceiptCursor{r.next.Int64, r.nextOrdinal.Int64}, removedKind: r.removedKind.String,
		removedSequence: r.removed.Int64, removedOrdinal: -1, removedCount: r.removedCount.Int64, removedBytes: r.removedBytes.Int64, rows: r.rows.Int64, bytes: r.bytes.Int64, noNext: r.noNext.Int64 == 1, appliedMS: r.applied.Int64}
	if r.removedOrdinal.Valid {
		s.facts.removedOrdinal = r.removedOrdinal.Int64
	}
	return s, nil
}

func iaConfigurationGCReference(q iaReceiptReclaimReference) iaConfigurationReference {
	return iaConfigurationReference{original: q.configuration, identity: q.identity, request: q.request}
}
func iaConfigurationGCKey(q iaReceiptReclaimReference) []any {
	kind, _ := iaConfigurationResultKindName(q.configuration.purpose)
	return []any{q.configuration.operationID[:], q.identity.Incarnation[:], kind}
}

// Private105 adapter. The caller must already prove CURRENT retained-owner
// maintenance/disclosure authority and admit actual complete native/copy bytes.
// Original configuration roots remain in their typed children until consumed;
// no live source observation, borrowed lease or cleanup child is introduced.
func lookupIAConfigurationReceiptReclaim(ctx context.Context, turn *commandTurn, q iaReceiptReclaimReference) (value iaReceiptReclaimNativeHistory, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if q.purpose != IAConfigurationReceiptReclaim || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IAConfigurationReceiptReclaim {
		return value, ErrInvalid
	}
	if err = validateIAReceiptReclaimReference(q); err != nil {
		return
	}
	base, e := loadIAConfigurationOwner(ctx, turn, iaConfigurationGCReference(q))
	if e != nil {
		return value, e
	}
	o := iaReceiptReclaimOwner{exists: base.exists, incarnation: base.incarnation, configuration: base.original, last: base.last, acknowledged: base.acknowledged, cursor: base.cursor, rows: base.rows, bytes: base.bytes}
	if !base.exists {
		_, err = classifyIAReceiptReclaimMetadata(q, o)
		return
	}
	if !iaReceiptReclaimScopeMatches(q, o) {
		return value, ErrConflict
	}
	// The only data header is sequence1; a valid GC reference is >1. Its
	// purpose cannot alias an original100/101 result, even below ACK.
	var raw iaConfigurationGCNativeSlot
	if err = iaRetirementPoint(ctx, turn, iaConfigurationGCSlot, iaConfigurationGCKey(q), raw.destinations()...); err != nil {
		return
	}
	o.due, o.dueMS = raw.due.Valid, raw.due.Int64
	if o.slot, err = raw.metadata(o.incarnation); err != nil {
		return
	}
	decision, e := classifyIAReceiptReclaimMetadata(q, o)
	if e != nil {
		return value, e
	}
	if decision == receiptRetired {
		return iaReceiptReclaimNativeHistory{decision: decision, owner: o}, nil
	}
	if o.slot, err = raw.decode(o.incarnation); err != nil {
		return
	}
	if decision == receiptReplay {
		if _, err = classifyIAReceiptReclaim(q, o); err != nil {
			return
		}
		return iaReceiptReclaimNativeHistory{decision: decision, owner: o, facts: o.slot.facts, digest: o.slot.digest}, nil
	}
	return iaReceiptReclaimNativeHistory{decision: decision, owner: o}, nil
}

func firstIAConfigurationGCUnit(ctx context.Context, turn *commandTurn, q iaReceiptReclaimReference, o iaReceiptReclaimOwner, cursor iaReceiptCursor) (iaReceiptReclaimProbe, error) {
	p := iaReceiptReclaimProbe{incarnation: o.incarnation, originalOperation: o.configuration.operationID, after: cursor, throughACK: o.acknowledged}
	if cursor.sequence == 1 {
		return p, nil
	}
	var ordinal, bytes int64
	key := iaConfigurationGCKey(q)
	err := iaRetirementPoint(ctx, turn, iaConfigurationGCSourceFirst, append(append([]any(nil), key...), cursor.ordinal), &ordinal, &bytes)
	if err == nil {
		p.unit = iaReceiptReclaimUnit{kind: "source", sequence: 1, ordinal: ordinal, bytes: bytes}
		return p, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return iaReceiptReclaimProbe{}, err
	}
	var count int64
	err = iaRetirementPoint(ctx, turn, iaConfigurationGCHeaderFirst, append(append([]any(nil), key...), key...), &count, &bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return iaReceiptReclaimProbe{}, err
	}
	p.unit = iaReceiptReclaimUnit{kind: "header", sequence: 1, ordinal: -1, sourceCount: count, bytes: bytes}
	return p, nil
}

func iaConfigurationGCScopeValues(q iaReceiptReclaimReference) []any {
	o := q.configuration
	args := append(iaConfigurationGCKey(q), o.requestDigest[:], o.libraryID, o.libraryKey)
	if o.purpose == IAConfigureSource {
		args = append(args, o.sourceID)
	}
	return args
}
func iaConfigurationGCSlotValues(s iaReceiptReclaimSlot) []any {
	args := iaRetirementGCSlotValues(s)
	args[3] = "configuration_receipt_reclaim"
	var removed any
	if s.facts.removedKind == "source" {
		removed = s.facts.removedOrdinal
	}
	return append(args, s.facts.old.ordinal, removed, s.facts.next.ordinal)
}
func iaConfigurationGCStoreArguments(q iaReceiptReclaimReference, o iaReceiptReclaimOwner, slot iaReceiptReclaimSlot) (int, []any) {
	v := slot.facts
	var due any
	if !v.noNext {
		due = v.appliedMS
	}
	args := append([]any{q.identity.Sequence, v.next.sequence, v.next.ordinal, v.rows, v.bytes, due}, iaConfigurationGCSlotValues(slot)...)
	args = append(args, iaConfigurationGCScopeValues(q)...)
	args = append(args, o.last, o.acknowledged, o.cursor.sequence, o.cursor.ordinal, o.rows, o.bytes, o.dueMS, o.slot.retainedBytes)
	shape := 0
	if o.slot.present {
		shape = 1
		if o.slot.facts.removedKind == "source" {
			shape = 2
		} else if o.slot.facts.removedKind == "header" {
			shape = 3
		}
		for n, x := range iaConfigurationGCSlotValues(o.slot) {
			p := iaConfigurationGCColumns()[n]
			if shape == 1 && p.Nullable || shape == 3 && p.Name == "reclaim_removed_ordinal" {
				continue
			}
			args = append(args, x)
		}
	}
	return shape, args
}

// Exactly one source or its now-childless original header is deleted per
// turn. All OLD scope/slot/debt/cursor/due facts CAS on the SAME actual owner.
// Resolve unknown COMMIT through the same saved reference before fresh probes.
func applyIAConfigurationReceiptReclaim(ctx context.Context, turn *commandTurn, q iaReceiptReclaimReference, expectedOldSlot iaReceiptReclaimSlot) (value iaReceiptReclaimNativeHistory, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	h, err := lookupIAConfigurationReceiptReclaim(ctx, turn, q)
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
	first, err := firstIAConfigurationGCUnit(ctx, turn, q, o, o.cursor)
	if err != nil {
		return value, err
	}
	in := iaReceiptReclaimInput{reference: q, owner: o, expectedOldSlot: expectedOldSlot, first: first, appliedMS: applied}
	unit, err := prepareIAReceiptReclaimUnit(in)
	if err != nil {
		return value, err
	}
	if unit.removedCount != 1 {
		return value, ErrResolveRequired
	}
	key := iaConfigurationGCKey(q)
	id := iaConfigurationGCSourceDelete
	args := append(append([]any(nil), key...), unit.removedOrdinal, unit.removedBytes)
	if unit.removedKind == "header" {
		id = iaConfigurationGCHeaderDelete
		args = append(append(append([]any(nil), key...), first.unit.sourceCount, unit.removedBytes), key...)
	}
	var actual int64
	if err = iaRetirementPoint(ctx, turn, id, args, &actual); err != nil {
		return value, err
	}
	if actual != unit.removedBytes {
		return value, ErrResolveRequired
	}
	// Header eligibility is a physical NOT EXISTS over the original sources.
	// Probe only after deleting the selected child; missing/gapped/debt-drift
	// evidence fails closed and rolls this transaction's deletion back.
	if in.after, err = firstIAConfigurationGCUnit(ctx, turn, q, o, unit.next); err != nil {
		return value, err
	}
	v, err := prepareIAReceiptReclaim(in)
	if err != nil {
		return value, err
	}
	digest, err := canonicalResultDigest(q.identity, q.request, iaConfigurationReceiptReclaimResult{q.configuration, v})
	if err != nil {
		return value, err
	}
	slot := iaReceiptReclaimSlot{present: true, identity: q.identity, request: q.request, purpose: q.purpose, version: 1, resultCount: 1, facts: v, digest: digest}
	if slot.retainedBytes, err = iaReceiptReclaimSlotBytes(q.purpose, v); err != nil {
		return value, err
	}
	shape, args := iaConfigurationGCStoreArguments(q, o, slot)
	var charge int64
	if err = iaRetirementPoint(ctx, turn, iaConfigurationGCStoreID(q.configuration.purpose == IAConfigureSource, shape), args, &charge); err != nil {
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

func acknowledgeIAConfigurationReceiptReclaim(ctx context.Context, turn *commandTurn, q iaReceiptReclaimReference) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	h, err := lookupIAConfigurationReceiptReclaim(ctx, turn, q)
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
	args := append([]any{q.identity.Sequence}, iaConfigurationGCScopeValues(q)...)
	args = append(args, o.last, o.acknowledged, o.slot.identity.Sequence, o.slot.identity.OperationID[:], o.slot.request[:], o.slot.digest[:], o.slot.retainedBytes)
	if err = iaRetirementOneEffect(ctx, turn, iaConfigurationGCAckID(q.configuration.purpose == IAConfigureSource), args...); err != nil {
		return 0, err
	}
	return q.identity.Sequence, nil
}
