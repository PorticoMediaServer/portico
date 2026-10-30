package ownedcapture

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// PlayCaptureCheck is an additional, mandatory original-operation restriction.
// The actual Play domain supplies its sealed claim/actor/source/queue check; it
// grants no source permission and never replaces Producer's ordinary checks.
// It must not resolve a latest request or extend the original deadline.
type PlayCaptureCheck func(context.Context, *sql.Tx) error

// CapturePlay retains the original pre-session Play operation through copying
// and receipt publication. It returns private byte custody only, never a session
// or grant. The operation must already have its finite original lifetime; this
// method does not detach from it or renew the shared client's 120-second wait.
func (s *Service) CapturePlay(ctx context.Context, request Request, original PlayCaptureCheck) (Receipt, error) {
	if s == nil || ctx == nil || original == nil || dbwork.Snapshot(ctx) != nil {
		return Receipt{}, ErrInput
	}
	deadline, finite := ctx.Deadline()
	if !finite || deadline.After(time.Now().Add(120*time.Second)) {
		return Receipt{}, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	return s.capture(ctx, request, original, nil)
}

func (s *Service) checkPlayCapture(ctx context.Context, original PlayCaptureCheck) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if original == nil {
		return nil // ordinary Root52 store foundation, not the Play composition
	}
	if tx := dbwork.Snapshot(ctx); tx != nil {
		return checkPlayCaptureTx(ctx, tx, original)
	}
	view, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	defer view.Rollback()
	return checkPlayCaptureTx(view.Context(), view.Tx(), original)
}

func checkPlayCaptureTx(ctx context.Context, tx *sql.Tx, original PlayCaptureCheck) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if original == nil {
		return nil
	}
	if err := original(ctx, tx); err != nil {
		return err
	}
	return ctx.Err()
}
