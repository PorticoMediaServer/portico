package writer

import "database/sql"

const wcReclaimKind = "progress_receipt_reclaim"

// ReclaimValue is the COMPLETE retained cleanup scalar, not current owner debt.
// ResultCount is always one; RemovedCount independently records zero or one.
type ReclaimValue struct {
	CommandKind                  string
	ResultVersion                int64
	CapturedAcknowledgedSequence int64
	OldAfterSequence             int64
	RemovedSequence              sql.NullInt64
	RemovedCount                 int64
	RemovedVariableBytes         int64
	ResultAfterSequence          int64
	ResultNoNext                 bool
	AppliedMS                    int64
	ResultCount                  int64
}

func preflightReclaimValue(v ReclaimValue) error {
	if v.CommandKind != wcReclaimKind || v.ResultVersion != 1 || v.ResultCount != 1 ||
		v.CapturedAcknowledgedSequence < 0 || v.OldAfterSequence < 0 ||
		v.OldAfterSequence > v.CapturedAcknowledgedSequence || v.AppliedMS < 0 ||
		v.RemovedVariableBytes < 0 || !v.RemovedSequence.Valid && v.RemovedSequence.Int64 != 0 {
		return ErrResolveRequired
	}
	switch v.RemovedCount {
	case 0:
		if v.RemovedSequence.Valid || v.RemovedVariableBytes != 0 || v.ResultAfterSequence != v.OldAfterSequence || !v.ResultNoNext {
			return ErrResolveRequired
		}
	case 1:
		if !v.RemovedSequence.Valid || v.RemovedSequence.Int64 <= v.OldAfterSequence ||
			v.RemovedSequence.Int64 > v.CapturedAcknowledgedSequence || v.RemovedVariableBytes <= 0 ||
			v.ResultAfterSequence != v.RemovedSequence.Int64 {
			return ErrResolveRequired
		}
	default:
		return ErrResolveRequired
	}
	return nil
}

// Parent adds ReclaimValue to the shared preflight/write switches. No reflection
// of generated output, enum ordinal, registration hook or parallel wire format.
func writeReclaimValue(e *requestEncoder, id CommandIdentity, request RequestDigest, v ReclaimValue) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(wcReclaimKind)
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.CommandKind)
	e.number(uint64(v.ResultVersion))
	e.number(uint64(v.CapturedAcknowledgedSequence))
	e.number(uint64(v.OldAfterSequence))
	e.flag(v.RemovedSequence.Valid)
	if v.RemovedSequence.Valid {
		e.number(uint64(v.RemovedSequence.Int64))
	}
	e.number(uint64(v.RemovedCount))
	e.number(uint64(v.RemovedVariableBytes))
	e.number(uint64(v.ResultAfterSequence))
	e.flag(v.ResultNoNext)
	e.number(uint64(v.AppliedMS))
	e.number(uint64(v.ResultCount))
	return e.err
}

// The complete native OLD context participates in the common request digest.
// AppliedMS is deliberately supplied by the parent AFTER original classification.
type wcReclaimRequest struct {
	Scope    wcProgressScope
	Snapshot wcReclaimSnapshot
}

func preflightWCReclaimRequest(id CommandIdentity, q wcReclaimRequest) error {
	s, o := q.Snapshot, q.Snapshot.owner
	if !validWCReclaimScope(q.Scope) || q.Scope.playbackID != o.playbackID || q.Scope.incarnation != o.incarnation ||
		id.Incarnation != o.incarnation || id.OperationID == [16]byte{} || id.Sequence <= 0 ||
		o.lastSequence < 0 || o.acknowledgedSequence != o.lastSequence || id.Sequence <= o.lastSequence ||
		id.Sequence-o.lastSequence != 1 || !o.due || o.dueMS < 0 || o.rows < 0 || o.variableBytes < 0 ||
		(o.rows == 0) != (o.variableBytes == 0) || o.afterSequence < 0 || o.afterSequence > o.acknowledgedSequence ||
		s.controlBytes <= 0 || s.slot.present != (s.resultVersion == 1) || !s.slot.present && s.resultVersion != 0 ||
		!validWCReclaimSlot(s.slot, o) {
		return ErrResolveRequired
	}
	return nil
}

func wcReclaimRequestDigest(purpose CommandID, id CommandIdentity, q wcReclaimRequest) (RequestDigest, error) {
	if err := checkWCReclaimPurpose(purpose); err != nil {
		return RequestDigest{}, err
	}
	if err := preflightWCReclaimRequest(id, q); err != nil {
		return RequestDigest{}, err
	}
	return canonicalRequestDigest(purpose, 1, id, q)
}
