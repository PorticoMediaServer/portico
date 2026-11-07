package writer

import "database/sql"

// NULL witnesses survive scanning until the closed decoder checks the complete
// group. In particular a NULL removed sequence is not an absent scalar.
type wcReclaimRawSlot struct {
	sequence, version, floor, oldCursor         sql.NullInt64
	removedSequence, removedCount, removedBytes sql.NullInt64
	nextCursor, noNext, appliedMS, resultCount  sql.NullInt64
	kind                                        sql.NullString
	operation, request, digest                  []byte
}

func scanWCReclaimSlot(row commandRow) (out wcReclaimRawSlot, err error) {
	err = row.Scan(&out.sequence, &out.operation, &out.request, &out.kind, &out.version,
		&out.floor, &out.oldCursor, &out.removedSequence, &out.removedCount, &out.removedBytes,
		&out.nextCursor, &out.noNext, &out.appliedMS, &out.resultCount, &out.digest)
	return out, err
}

func (s wcReclaimRawSlot) value() ReclaimValue {
	return ReclaimValue{CommandKind: s.kind.String, ResultVersion: s.version.Int64,
		CapturedAcknowledgedSequence: s.floor.Int64, OldAfterSequence: s.oldCursor.Int64,
		RemovedSequence: s.removedSequence, RemovedCount: s.removedCount.Int64,
		RemovedVariableBytes: s.removedBytes.Int64, ResultAfterSequence: s.nextCursor.Int64,
		ResultNoNext: s.noNext.Int64 == 1, AppliedMS: s.appliedMS.Int64, ResultCount: s.resultCount.Int64}
}

func preflightReclaimResult(id CommandIdentity, request RequestDigest, v ReclaimValue) error {
	if err := preflightReclaimValue(v); err != nil {
		return err
	}
	if id.Incarnation == [16]byte{} || id.OperationID == [16]byte{} || request == (RequestDigest{}) ||
		id.Sequence <= v.CapturedAcknowledgedSequence || id.Sequence-v.CapturedAcknowledgedSequence != 1 {
		return ErrResolveRequired
	}
	return nil
}

func decodeWCReclaimSlot(raw wcReclaimRawSlot, owner wcReclaimOwner) (wcReclaimSlot, int64, error) {
	out, version, err := decodeWCReclaimSlotShape(raw, owner)
	if err != nil || !out.present {
		return out, version, err
	}
	// Uses the one shared result codec; parent adds its closed ReclaimValue
	// switches. There is no local digest fallback for an uncompiled schema.
	computed, err := canonicalResultDigest(out.reference.Identity, out.reference.RequestDigest, raw.value())
	if err != nil || computed != ResultDigest(out.resultDigest) {
		return wcReclaimSlot{}, 0, ErrResolveRequired
	}
	return out, version, nil
}

// Pure native-shape validation is separate only so it can be tested before the
// parent integrates the shared codec. It does not attest a result digest.
func decodeWCReclaimSlotShape(raw wcReclaimRawSlot, owner wcReclaimOwner) (out wcReclaimSlot, version int64, err error) {
	fields := []sql.NullInt64{raw.sequence, raw.version, raw.floor, raw.oldCursor, raw.removedCount,
		raw.removedBytes, raw.nextCursor, raw.noNext, raw.appliedMS, raw.resultCount}
	if !raw.sequence.Valid {
		if raw.kind != (sql.NullString{}) || raw.removedSequence != (sql.NullInt64{}) ||
			raw.operation != nil || raw.request != nil || raw.digest != nil || owner.afterSequence != 0 {
			return out, 0, ErrResolveRequired
		}
		for _, field := range fields {
			if field != (sql.NullInt64{}) {
				return out, 0, ErrResolveRequired
			}
		}
		return out, 0, nil
	}
	for _, field := range fields {
		if !field.Valid {
			return out, 0, ErrResolveRequired
		}
	}
	if !raw.kind.Valid || len(raw.operation) != 16 || len(raw.request) != 32 || len(raw.digest) != 32 ||
		(raw.noNext.Int64 != 0 && raw.noNext.Int64 != 1) || raw.sequence.Int64 > owner.lastSequence ||
		raw.floor.Int64 > owner.acknowledgedSequence || raw.nextCursor.Int64 != owner.afterSequence {
		return out, 0, ErrResolveRequired
	}
	out.present = true
	out.reference.Identity = CommandIdentity{Incarnation: owner.incarnation, Sequence: raw.sequence.Int64}
	copy(out.reference.Identity.OperationID[:], raw.operation)
	copy(out.reference.RequestDigest[:], raw.request)
	copy(out.resultDigest[:], raw.digest)
	if out.resultDigest == [32]byte{} || preflightReclaimResult(out.reference.Identity, out.reference.RequestDigest, raw.value()) != nil {
		return wcReclaimSlot{}, 0, ErrResolveRequired
	}
	out.floor, out.oldCursor = raw.floor.Int64, raw.oldCursor.Int64
	out.removedSequence, out.removedCount = raw.removedSequence.Int64, raw.removedCount.Int64
	out.removedBytes, out.nextCursor = raw.removedBytes.Int64, raw.nextCursor.Int64
	out.noNext, out.appliedMS = raw.noNext.Int64 == 1, raw.appliedMS.Int64
	return out, raw.version.Int64, nil
}
