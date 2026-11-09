package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// IA-10's two ACK/fixed-slot reclaim purposes share one closed result schema.
// The schema names the owner-specific command kind, so a configuration result
// can never be relabelled as a retirement result or the reverse. The value is
// the COMPLETE retained cleanup scalar: the ACK watermark this turn captured,
// the one receipt unit it measured and deleted, and the remaining owner debt.
// ACK alone never subtracts a row or a byte.
const (
	iaConfigurationReclaimKind = "configuration_receipt_reclaim"
	iaRetirementReclaimKind    = "retirement_receipt_reclaim"

	iaReclaimResultSchema = "ia_reclaim"
)

type iaReclaimResult struct {
	kind            string
	version         int64
	captured        int64
	oldSequence     int64
	removedSequence sql.NullInt64
	removedKind     string
	removedCount    int64
	removedBytes    int64
	resultSequence  int64
	removedOrdinal  sql.NullInt64
	resultOrdinal   sql.NullInt64
	noNext          bool
	appliedMS       int64
	rows, bytes     int64
	resultCount     int64
}

// Fixed wire overhead excluding the bounded texts. The kind spellings are
// checked against the two exact allowed values before any hashing.

func preflightIAReclaimResult(v iaReclaimResult) error {
	if (v.kind != iaConfigurationReclaimKind && v.kind != iaRetirementReclaimKind) ||
		v.version != 1 || v.resultCount != 1 || v.captured < 1 || v.oldSequence < 0 ||
		v.oldSequence > v.captured || v.appliedMS < 0 || v.rows < 0 || v.bytes < 0 ||
		(v.rows == 0) != (v.bytes == 0) || (v.rows == 0) != v.noNext || v.removedBytes < 0 {
		return invalid("ia_reclaim_result")
	}
	if v.resultSequence < v.oldSequence || v.resultSequence > v.captured {
		return invalid("ia_reclaim_result_sequence")
	}
	switch v.removedKind {
	case "none":
		if v.removedCount != 0 || v.removedSequence.Valid || v.removedBytes != 0 ||
			v.resultSequence != v.oldSequence || !v.noNext || v.removedOrdinal.Valid {
			return invalid("ia_reclaim_result_none")
		}
	case "source":
		if v.kind != iaConfigurationReclaimKind || v.removedCount != 1 || !v.removedSequence.Valid ||
			v.removedSequence.Int64 != 1 || v.removedBytes <= 0 || v.resultSequence != 0 || v.noNext ||
			!v.removedOrdinal.Valid || v.removedOrdinal.Int64 < 0 ||
			!v.resultOrdinal.Valid || v.resultOrdinal.Int64 != v.removedOrdinal.Int64 {
			return invalid("ia_reclaim_result_source")
		}
	case "header":
		if v.removedCount != 1 || !v.removedSequence.Valid || v.removedBytes <= 0 ||
			v.resultSequence != v.removedSequence.Int64 || v.removedOrdinal.Valid {
			return invalid("ia_reclaim_result_header")
		}
		if v.kind == iaConfigurationReclaimKind && (v.removedSequence.Int64 != 1 || v.resultSequence != 1) {
			return invalid("ia_reclaim_result_configuration_header")
		}
	default:
		return invalid("ia_reclaim_result_kind")
	}
	if v.removedSequence.Valid && (v.removedSequence.Int64 <= v.oldSequence || v.removedSequence.Int64 > v.captured) {
		return invalid("ia_reclaim_result_removed")
	}
	if v.kind == iaRetirementReclaimKind {
		if v.removedKind == "source" || v.removedOrdinal.Valid || v.resultOrdinal.Valid {
			return invalid("ia_reclaim_result_retirement_ordinal")
		}
	} else if !v.resultOrdinal.Valid || v.resultOrdinal.Int64 < -1 {
		return invalid("ia_reclaim_result_ordinal")
	}
	if v.removedKind == "header" && v.kind == iaConfigurationReclaimKind && !v.noNext {
		// The configuration header is the last retained row; a source row is
		// always deleted before it.
		return invalid("ia_reclaim_result_configuration_header_next")
	}
	return nil
}

// One shared wire shape keeps the two owner kinds decodable by one codec while
// the closed fields above stay owner-exact. No reflection of SQL rows and no
// result reconstruction from a mutable owner row.
func writeIAReclaimResult(e *requestEncoder, id CommandIdentity, request RequestDigest, v iaReclaimResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(iaReclaimResultSchema)
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.kind)
	e.number(uint64(v.version))
	e.number(uint64(v.captured))
	e.number(uint64(v.oldSequence))
	e.flag(v.removedSequence.Valid)
	if v.removedSequence.Valid {
		e.number(uint64(v.removedSequence.Int64))
	}
	e.text(v.removedKind)
	e.number(uint64(v.removedCount))
	e.number(uint64(v.removedBytes))
	e.number(uint64(v.resultSequence))
	e.flag(v.removedOrdinal.Valid)
	if v.removedOrdinal.Valid {
		e.number(uint64(v.removedOrdinal.Int64))
	}
	e.flag(v.resultOrdinal.Valid)
	if v.resultOrdinal.Valid {
		e.number(uint64(v.resultOrdinal.Int64))
	}
	e.flag(v.noNext)
	e.number(uint64(v.appliedMS))
	e.number(uint64(v.rows))
	e.number(uint64(v.bytes))
	e.number(uint64(v.resultCount))
	if e.err != nil {
		return errors.Join(invalid("ia_reclaim_result"), e.err)
	}
	return nil
}

func iaReclaimText(s string, limit int) bool {
	return len(s) > 0 && len(s) <= limit && utf8.ValidString(s) && strings.IndexByte(s, 0) < 0
}

// The permanent owner keeps exactly one fixed reclaim slot. Both group C owner
// tables declare the same reclaim columns; the configuration table adds three
// non-null ordinal witnesses, which stay absent for retirement.
type iaReclaimSlotRow struct {
	sequence                                        sql.NullInt64
	operation, request, digest                      []byte
	kind, removedKind                               sql.NullString
	version, captured, oldSequence, removedSequence sql.NullInt64
	removedCount, removedBytes, resultSequence      sql.NullInt64
	noNext, rows, bytes, appliedMS, resultCount     sql.NullInt64
	oldOrdinal, removedOrdinal, resultOrdinal       sql.NullInt64
}

type iaReclaimSaved struct {
	sequence  int64
	operation [16]byte
	request   RequestDigest
	digest    ResultDigest
	result    iaReclaimResult
}

// decodeIAReclaimSlot is the all-or-nothing slot reader. A partial group can
// never be served as a saved result, and a saved result never becomes Applied
// until its exact typed digest is recomputed.
func decodeIAReclaimSlot(s iaReclaimSlotRow, kind string, incarnation [16]byte, last, acknowledged, receiptReclaimSequence int64) (out iaReclaimSaved, present bool, err error) {
	if !s.sequence.Valid {
		if s.operation != nil || s.request != nil || s.digest != nil ||
			s.kind != (sql.NullString{}) || s.version != (sql.NullInt64{}) || s.captured != (sql.NullInt64{}) ||
			s.oldSequence != (sql.NullInt64{}) || s.removedSequence != (sql.NullInt64{}) ||
			s.removedKind != (sql.NullString{}) || s.removedCount != (sql.NullInt64{}) ||
			s.removedBytes != (sql.NullInt64{}) || s.resultSequence != (sql.NullInt64{}) ||
			s.noNext != (sql.NullInt64{}) || s.rows != (sql.NullInt64{}) ||
			s.bytes != (sql.NullInt64{}) || s.appliedMS != (sql.NullInt64{}) ||
			s.resultCount != (sql.NullInt64{}) || s.oldOrdinal != (sql.NullInt64{}) ||
			s.removedOrdinal != (sql.NullInt64{}) || s.resultOrdinal != (sql.NullInt64{}) {
			return out, false, ErrResolveRequired
		}
		return out, false, nil
	}
	if !s.kind.Valid || len(s.operation) != 16 || len(s.request) != 32 || len(s.digest) != 32 ||
		s.kind.String != kind || !s.version.Valid || s.version.Int64 != 1 ||
		!s.captured.Valid || !s.oldSequence.Valid || !s.removedCount.Valid || !s.removedBytes.Valid ||
		!s.resultSequence.Valid || !s.noNext.Valid || !s.rows.Valid || !s.bytes.Valid ||
		!s.appliedMS.Valid || !s.resultCount.Valid || s.resultCount.Int64 != 1 || !s.removedKind.Valid ||
		s.sequence.Int64 <= 1 || s.sequence.Int64 > last ||
		s.captured.Int64 != s.sequence.Int64-1 || s.captured.Int64 > acknowledged ||
		s.oldSequence.Int64 < 0 || s.oldSequence.Int64 > s.captured.Int64 ||
		s.resultSequence.Int64 != receiptReclaimSequence || s.removedBytes.Int64 < 0 ||
		(s.noNext.Int64 != 0 && s.noNext.Int64 != 1) || s.rows.Int64 < 0 || s.bytes.Int64 < 0 || s.appliedMS.Int64 < 0 {
		return out, false, ErrResolveRequired
	}
	value := iaReclaimResult{
		kind: kind, version: s.version.Int64, captured: s.captured.Int64, oldSequence: s.oldSequence.Int64,
		removedSequence: s.removedSequence, removedKind: s.removedKind.String, removedCount: s.removedCount.Int64,
		removedBytes: s.removedBytes.Int64, resultSequence: s.resultSequence.Int64,
		removedOrdinal: s.removedOrdinal, resultOrdinal: s.resultOrdinal, noNext: s.noNext.Int64 == 1,
		appliedMS: s.appliedMS.Int64, rows: s.rows.Int64, bytes: s.bytes.Int64, resultCount: s.resultCount.Int64,
	}
	if kind == iaRetirementReclaimKind {
		if s.oldOrdinal.Valid || s.removedOrdinal.Valid || s.resultOrdinal.Valid {
			return out, false, ErrResolveRequired
		}
	} else if !s.oldOrdinal.Valid || s.oldOrdinal.Int64 < -1 {
		return out, false, ErrResolveRequired
	}
	if err = preflightIAReclaimResult(value); err != nil {
		return out, false, ErrResolveRequired
	}
	identity := CommandIdentity{Incarnation: incarnation, Sequence: s.sequence.Int64}
	copy(identity.OperationID[:], s.operation)
	var request RequestDigest
	copy(request[:], s.request)
	computed, err := canonicalResultDigest(identity, request, value)
	if err != nil || !equalBytes(computed[:], s.digest) {
		return out, false, ErrResolveRequired
	}
	return iaReclaimSaved{sequence: s.sequence.Int64, operation: identity.OperationID, request: request, digest: computed, result: value}, true, nil
}

// The reclaim turn's trusted millisecond clock. It is the same owned, observed
// transaction time as the merged IA retirement begin/step: never time.Now and
// never a caller-supplied value.
func iaReclaimTime(ctx context.Context, turn *commandTurn) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return 0, ErrInvalid
	}
	tx := turn.transaction
	if tx.descriptor.ID != IAConfigureReclaim && tx.descriptor.ID != IARetireReclaim {
		return 0, ErrInvalid
	}
	if tx.writer == nil || tx.writer.now == nil || tx.ctx == nil || tx.conn == nil || tx.quantum == nil || tx.custody == nil {
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
