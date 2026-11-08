package writer

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"strconv"
)

func applyMPRepeat(ctx context.Context, t *commandTurn, q mpQueueRepeatRequest, now int64) (out mpQueueRepeatValue, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = q.validate(); err != nil || now <= 0 {
		return out, errors.Join(ErrInvalid, err)
	}
	row, err := wc008DraftQuery(ctx, t, mpRepeatCurrent, q.Queue, q.ExpectedRevision, q.ExpectedCustody)
	if err != nil {
		return out, err
	}
	var before string
	var updated int64
	if err = row.Scan(&before, &updated); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	out = mpQueueRepeatValue{Queue: q.Queue, Repeat: before, Revision: q.ExpectedRevision, Custody: q.ExpectedCustody, UpdatedMS: updated, AppliedMS: now}
	if before == q.Repeat {
		return out, nil
	}

	// Only bounded identity stubs (129 Unicode scalars, at most516 bytes each)
	// enter the fixed workspace. They authorize nothing and cannot truncate into
	// a selected row: the exact PK point below must still be prepared in this queue.
	rows, err := t.query(ctx, t.steps[mpRepeatPreparedKey].statementStep, q.Queue)
	if err != nil {
		return out, err
	}
	count := 0
	var nextKey string
	for rows.Next() {
		count++
		if count > 1 {
			err = ErrResolveRequired
			break
		}
		if err = rows.Scan(&nextKey); err != nil {
			break
		}
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	if count == 1 {
		if !mpWorkerID(nextKey) {
			return out, ErrCap
		}
		row, err = wc008DraftQuery(ctx, t, mpRepeatPreparedBytes, nextKey, q.Queue)
		if err != nil {
			return out, err
		}
		var tokenBytes, mediaBytes int64
		if err = row.Scan(&tokenBytes, &mediaBytes); err != nil {
			return out, err
		}
		if tokenBytes <= 0 || tokenBytes > 128 || mediaBytes <= 0 || mediaBytes > 128 {
			return out, ErrCap
		}
		row, err = wc008DraftQuery(ctx, t, mpRepeatPrepared, nextKey, q.Queue)
		if err != nil {
			return out, err
		}
		if err = row.Scan(&out.CanceledToken, &out.Media, &out.Generation); err != nil {
			return out, err
		}
		if out.CanceledToken != nextKey || int64(len(out.CanceledToken)) != tokenBytes || int64(len(out.Media)) != mediaBytes || !mpWorkerID(out.Media) || out.Generation <= 0 {
			return out, ErrResolveRequired
		}

		// The original stop ID is bounded before copying even when this physical
		// occurrence was already retired by another accepted command.
		row, err = wc008DraftQuery(ctx, t, mpRepeatStopBytes, out.Media, out.Generation)
		if err != nil {
			return out, err
		}
		var stopBytes, retired, exited int64
		if err = row.Scan(&stopBytes, &retired, &exited); err != nil {
			return out, err
		}
		if stopBytes < 0 || stopBytes > 128 || retired < 0 || retired > out.Generation || exited < 0 || exited > retired || retired == out.Generation && stopBytes == 0 {
			return out, ErrResolveRequired
		}
		// REQUEST retirement; never accept a caller's physical-exit observation.
		if retired == out.Generation {
			row, err = wc008DraftQuery(ctx, t, mpRepeatExistingStop, out.Media, out.Generation, q.Account, q.Profile)
		} else {
			row, err = wc008DraftQuery(ctx, t, mpRepeatStop, q.StopRequest, out.Media, out.Generation, q.Account, q.Profile)
		}
		if err != nil {
			return out, err
		}
		var actualExit int64
		if err = row.Scan(&out.StopRequest, &actualExit); err != nil {
			return out, errors.Join(ErrConflict, err)
		}
		expectedBytes := int64(len(q.StopRequest))
		if retired == out.Generation {
			expectedBytes = stopBytes
		}
		if !mpWorkerID(out.StopRequest) || int64(len(out.StopRequest)) != expectedBytes || actualExit != exited {
			return out, ErrResolveRequired
		}
		out.PhysicalPending = exited < out.Generation
		if out.PhysicalPending {
			row, err = wc008DraftQuery(ctx, t, mpRepeatObligation, out.CanceledToken, out.Media, out.Generation, out.StopRequest, "queue_changed", now)
			if err != nil {
				return out, err
			}
			var bytes int64
			if err = row.Scan(&bytes); err != nil {
				return out, err
			}
			if bytes != int64(len(out.CanceledToken)+len(out.Media)+len(out.StopRequest)+len("queue_changed")) {
				return out, ErrResolveRequired
			}
		}

		row, err = wc008DraftQuery(ctx, t, mpRepeatCancel, out.CanceledToken, q.Queue, out.Media, out.Generation)
		if err != nil {
			return out, err
		}
		var token string
		if err = row.Scan(&token); err != nil {
			return out, errors.Join(ErrConflict, err)
		}
		if token != out.CanceledToken {
			return out, ErrResolveRequired
		}
	}
	row, err = wc008DraftQuery(ctx, t, mpRepeatUpdate, q.Repeat, max(updated, now), q.Queue, q.ExpectedRevision, q.ExpectedCustody, before)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.Revision, &out.UpdatedMS); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if out.Revision != q.ExpectedRevision+1 || out.UpdatedMS != max(updated, now) {
		return out, ErrResolveRequired
	}
	out.Repeat, out.Changed = q.Repeat, true
	// Two closed existing audiences and the existing event vocabulary. These
	// normalized invalidation hints are not a replacement wire QueueHeader;
	// phase4's feed adapter still owns the full public event DTO oracle.
	for _, audience := range []string{apievents.DeviceAudience(q.Device), apievents.ProfileAudience("local", q.Account, q.Profile)} {
		if _, err = appendWCAPIEvent(ctx, t, wcAPIEvent{audience: audience, eventType: "queue.updated", resourceKind: "queue", resourceID: q.Queue, revision: strconv.FormatInt(out.Revision, 10), data: "{}", atMS: now}); err != nil {
			return out, err
		}
	}
	return out, err
}
