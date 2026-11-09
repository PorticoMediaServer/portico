package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// One bounded maintenance quantum: at most 16 sparse acknowledgements, at most
// one expired shared notice and at most one expired broadcast operation. A
// notice remains until ALL referencing acknowledgements are physically gone, so
// an ACK-heavy record takes several bounded cycles instead of a cascade or a
// recipient loop. The descriptor durability stays NORMAL; this command is
// admitted as maintenance (M) with its own bounded quantum, never as a normal
// notice turn. Reclaim refunds ONLY the variable bytes it actually deleted and
// never allocates a notice revision or appends a hint.
const (
	uoNoticeReclaimMaxAckRows    = 16
	uoNoticeReclaimVictimProbe   = uoNoticeReclaimMaxAckRows + 1
	uoNoticeReclaimMaxOperations = 1
)

type uoNoticeReclaimEffect struct {
	ackRows, noticeRows, operationRows    int
	ackBytes, noticeBytes, operationBytes int64
	noticePending, operationPending       bool
}

// Actual deleted variable bytes. No fixed page overhead and no column maximum
// is refunded.
func (e uoNoticeReclaimEffect) refundedBytes() int64 {
	return e.ackBytes + e.noticeBytes + e.operationBytes
}

// bindUONoticeReclaimRoot uses the same named refusal seam as UO-12. The private
// lifetime-root receipt projection (UO-01) and its root reclaim (UO-02) are
// separate tasks and are not available to this build; a caller that needs the
// root-bound original result must refuse rather than fabricate custody.
func bindUONoticeReclaimRoot(uoNoticeReclaimEffect) (uoNoticeRootBinding, error) {
	return uoNoticeRootBinding{}, ErrNoticeRootUnavailable
}

func reclaimUONotices(ctx context.Context, turn *commandTurn, nowMS int64) (out uoNoticeReclaimEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if nowMS <= 0 {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, uoNoticeReclaimExpired, nowMS)
	if err != nil {
		return out, err
	}
	var sequence, revision int64
	err = row.Scan(&sequence, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		if sequence <= 0 || revision <= 0 {
			return out, ErrResolveRequired
		}
		victims, e := uoNoticeVictims(ctx, turn, sequence)
		if e != nil {
			return out, e
		}
		// The probe bound is cap+1: a seventeenth principal proves the notice
		// still has sparse ACKs beyond this quantum.
		complete := len(victims) <= uoNoticeReclaimMaxAckRows
		if len(victims) > uoNoticeReclaimMaxAckRows {
			victims = victims[:uoNoticeReclaimMaxAckRows]
		}
		for _, principal := range victims {
			removed, e := uoNoticeDeletedBytes(ctx, turn, uoNoticeReclaimAckDelete, uoNoticeReclaimAckColumns, principal, sequence)
			if e != nil {
				return out, e
			}
			out.ackRows++
			out.ackBytes += removed
		}
		if complete {
			removed, e := uoNoticeDeletedBytes(ctx, turn, uoNoticeReclaimRecordDelete, uoNoticeReclaimRecordColumns, sequence, revision, nowMS)
			if e != nil {
				return out, e
			}
			out.noticeRows++
			out.noticeBytes += removed
		}
		// One more bounded candidate probe distinguishes remaining work from done.
		probe, e := wc008DraftQuery(ctx, turn, uoNoticeReclaimExpired, nowMS)
		if e != nil {
			return out, e
		}
		e = probe.Scan(&sequence, &revision)
		if e == nil {
			out.noticePending = true
		} else if !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
	}
	row, err = wc008DraftQuery(ctx, turn, uoNoticeReclaimOperationExpired, nowMS)
	if err != nil {
		return out, err
	}
	var scope, operation string
	err = row.Scan(&scope, &operation)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	for ; err == nil && out.operationRows < uoNoticeReclaimMaxOperations; out.operationRows++ {
		if !uoNoticeText(scope, uoNoticeMaxScopeBytes, true) || !uoNoticeIdentifier(operation, uoNoticeMaxIDBytes) {
			return out, ErrResolveRequired
		}
		removed, e := uoNoticeDeletedBytes(ctx, turn, uoNoticeReclaimOperationDelete, uoNoticeReclaimOperationColumns, scope, operation, nowMS)
		if e != nil {
			return out, e
		}
		out.operationBytes += removed
		row, err = wc008DraftQuery(ctx, turn, uoNoticeReclaimOperationExpired, nowMS)
		if err != nil {
			return out, err
		}
		err = row.Scan(&scope, &operation)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		out.operationPending = true
	}
	return out, turn.checkLifetimes(ctx)
}

// uoNoticeDeletedBytes scans the physically deleted TEXT row and sums the exact
// variable bytes it removed. RETURNING remains a closed plain-column list (the
// DML parser rejects expressions), so the arithmetic lives here and includes
// every column the row owns, last_profile included.
func uoNoticeDeletedBytes(ctx context.Context, turn *commandTurn, id stmt.ID, columns int, args ...any) (int64, error) {
	if columns < 1 || columns > 16 {
		return 0, ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, id, args...)
	if err != nil {
		return 0, err
	}
	values := make([]string, columns)
	dest := make([]any, columns)
	for i := range values {
		dest[i] = &values[i]
	}
	if err = row.Scan(dest...); errors.Is(err, sql.ErrNoRows) {
		return 0, ErrConflict
	}
	if err != nil {
		return 0, err
	}
	var total int64
	for _, value := range values {
		total += int64(len(value))
	}
	return total, turn.checkLifetimes(ctx)
}

// One exact ACK seek by notice sequence, bounded to the quantum probe. Another
// principal's acknowledgement is never selected and the caller deletes only the
// returned principals, so reclaim cannot become a recipient loop.
func uoNoticeVictims(ctx context.Context, turn *commandTurn, sequence int64) (out []string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	step, ok := turn.steps[uoNoticeReclaimAckVictims]
	if !ok {
		return nil, ErrInvalid
	}
	rows, err := turn.query(ctx, step.statementStep, sequence)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			return nil, err
		}
		if !uoNoticeText(p, uoNoticeMaxPrincipalBytes, true) || len(out) >= uoNoticeReclaimVictimProbe {
			return nil, ErrResolveRequired
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
