package dbwork

import (
	"context"
	"database/sql"
)

type writerProbeKey struct{}

// CheckWriteReady checks the same writer and priority gate that sign-in and
// other mutations need. A read-pool ping can succeed while this path is stuck.
// Nothing is changed or committed; the caller supplies a short probe deadline.
func CheckWriteReady(ctx context.Context, db *sql.DB) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// Only this closed BEGIN/ROLLBACK operation can claim to append no frames;
	// ordinary request and background writers retain the checkpoint fence.
	ctx = context.WithValue(ctx, writerProbeKey{}, true)
	w, err := Begin(ctx, db, ClassInteractive)
	if err != nil {
		return err
	}
	if err = w.Rollback(); err != nil {
		return err
	}
	return ctx.Err()
}
