package writer

import (
	"context"
	"errors"
	"time"
)

// finishCommand is the sole release rule for native command results and scope
// proofs. It runs after all deferred native/terminal work. No error, including
// late cancellation of a physically committed command, may carry output.
// Physical truth remains in the private frame; retry must resolve the ORIGINAL
// command rather than interpreting zero output as a rollback.
func finishCommand[T any](ctx, lifetime context.Context, value T, err error) (T, error) {
	for _, c := range []context.Context{ctx, lifetime} {
		if c == nil {
			err = errors.Join(err, ErrInvalid)
		} else {
			err = errors.Join(err, c.Err())
		}
	}
	if err != nil {
		var zero T
		return zero, err
	}
	return value, nil
}

func commandLifetime(turn *commandTurn) context.Context {
	if turn == nil || turn.transaction == nil {
		return nil
	}
	return turn.transaction.ctx
}

// The caller owns this cancel function through its FINAL release defer. Keeping
// deadline creation together makes that ownership explicit while the frame has
// a separate cancel for SQL cleanup; neither cancel may hide lifetime failure.
func commandDeadline(ctx context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	return context.WithDeadline(ctx, deadline)
}
