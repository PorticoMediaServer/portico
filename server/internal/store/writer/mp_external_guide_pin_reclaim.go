package writer

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Exact retained physical occurrence, including its ORIGINAL source request.
// This is key data, not authority or exit evidence. A logical successor cannot
// substitute its generation/execution/request for an old released occurrence.
type mpGuidePinOccurrence struct {
	MediaSessionID  string
	Generation      int64
	ExecutionID     string
	SourceRequestID string
}
type mpGuidePinReclaimEffect struct{ Rows, VariableBytes int64 }

const (
	mpGuidePinReclaim stmt.ID = "mp.guide_pin.reclaim"
	mpGuidePinFirst   stmt.ID = "mp.guide_pin.first"
	mpGuidePinTie     stmt.ID = "mp.guide_pin.tie"
)

func mpGuidePinReclaimTurn(t *commandTurn) error {
	if t == nil || t.transaction == nil || !t.transaction.begun || t.transaction.done {
		return ErrInvalid
	}
	switch t.transaction.descriptor.ID {
	case 727, 719, IARetireLibrary, IARetireSource, IARetireStep:
		return nil
	default:
		return ErrInvalid
	}
}
func (k mpGuidePinOccurrence) valid() bool {
	return mpWorkerID(k.MediaSessionID) && k.Generation > 0 && len(k.ExecutionID) > 0 && len(k.ExecutionID) <= 512 && len(k.SourceRequestID) > 0 && len(k.SourceRequestID) <= 512
}

// The outer owner supplies current authority/retirement fence, existing lease,
// classification, quota and debt updates/commit in this SAME W transaction.
// It subtracts only returned native bytes and only when Rows==1. Replay/failure
// never turns this effect into Applied. Unreleased/mismatched keys fail closed.
func reclaimMPReleasedGuidePin(ctx context.Context, t *commandTurn, k mpGuidePinOccurrence) (out mpGuidePinReclaimEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpGuidePinReclaimTurn(t); err != nil {
		return out, err
	}
	if !k.valid() {
		return out, ErrInvalid
	}
	n, err := wc008DraftInteger(ctx, t, mpGuidePinReclaim, k.MediaSessionID, k.Generation, k.ExecutionID, k.SourceRequestID)
	if err != nil {
		return out, err
	}
	if n <= 0 || n > 8192 {
		return out, ErrResolveRequired
	}
	return mpGuidePinReclaimEffect{1, n}, t.checkLifetimes(ctx)
}

// At most two PK point/range reads, one raw candidate each. Select every exact
// occurrence in PK order, INCLUDING unreleased rows; callers must stop/refuse
// on those, never skip them by filtering a potentially unbounded range.
// sql.ErrNoRows marks durable exhaustion only within this outer owner's turn.
func selectMPGuidePinOccurrence(ctx context.Context, t *commandTurn, media string, after mpGuidePinOccurrence) (out mpGuidePinOccurrence, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpGuidePinReclaimTurn(t); err != nil {
		return out, err
	}
	if !mpWorkerID(media) || after.Generation < 0 || after.MediaSessionID != "" && after.MediaSessionID != media {
		return out, ErrInvalid
	}
	scan := func(id stmt.ID, args ...any) error {
		row, e := wc008DraftQuery(ctx, t, id, args...)
		if e != nil {
			return e
		}
		out.MediaSessionID = media
		return row.Scan(&out.Generation, &out.ExecutionID, &out.SourceRequestID)
	}
	if after.ExecutionID != "" {
		err = scan(mpGuidePinTie, media, after.Generation, after.ExecutionID)
		if err == nil {
			return out, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
	}
	err = scan(mpGuidePinFirst, media, after.Generation)
	return out, err
}
