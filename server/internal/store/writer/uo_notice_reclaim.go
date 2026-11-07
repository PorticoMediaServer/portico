package writer

import (
	"context"
	"database/sql"
	"errors"
)

// One bounded raw unit: <=16 sparse ACKs, <=1 expired notice and <=1 expired
// operation. A notice remains until ALL referencing ACKs are physically gone.
// No cascade, recipient fanout or unbounded expired-row DELETE. W must admit
// this as maintenance with its own forecasts, never as a normal notice turn.
type uoNoticeReclaimEffect struct {
	ackRows, noticeRows, operationRows int
	noticePending, operationPending    bool
}

func reclaimUONotices(ctx context.Context, turn *commandTurn, nowMS int64) (out uoNoticeReclaimEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if nowMS <= 0 {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, uoNoticeExpired, nowMS)
	if err != nil {
		return out, err
	}
	var sequence, revision int64
	err = row.Scan(&sequence, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		victims, e := uoNoticeVictims(ctx, turn, sequence)
		if e != nil {
			return out, e
		}
		for _, principal := range victims[:min(16, len(victims))] {
			if _, e = wc008DraftInteger(ctx, turn, uoNoticeAckDelete, principal, sequence); e != nil {
				return out, e
			}
			out.ackRows++
		}
		if len(victims) <= 16 {
			if _, e = wc008DraftInteger(ctx, turn, uoNoticeDelete, sequence, revision, nowMS); e != nil {
				return out, e
			}
			out.noticeRows++
		}
		// One more bounded candidate probe distinguishes remaining work from done.
		probe, e := wc008DraftQuery(ctx, turn, uoNoticeExpired, nowMS)
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
	row, err = wc008DraftQuery(ctx, turn, uoNoticeOperationExpired, nowMS)
	if err != nil {
		return out, err
	}
	var scope, operation string
	err = row.Scan(&scope, &operation)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		if !uoNoticeText(scope, 400, true) || !uoNoticeText(operation, 160, true) {
			return out, ErrResolveRequired
		}
		if _, err = wc008DraftInteger(ctx, turn, uoNoticeOperationDelete, scope, operation, nowMS); err != nil {
			return out, err
		}
		out.operationRows++
		row, err = wc008DraftQuery(ctx, turn, uoNoticeOperationExpired, nowMS)
		if err != nil {
			return out, err
		}
		err = row.Scan(&scope, &operation)
		if err == nil {
			out.operationPending = true
		} else if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
	}
	return out, turn.checkLifetimes(ctx)
}
func uoNoticeVictims(ctx context.Context, turn *commandTurn, sequence int64) (out []string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	step, ok := turn.steps[uoNoticeAckVictims]
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
		if !uoNoticeText(p, 512, true) || len(out) >= 17 {
			return nil, ErrResolveRequired
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
