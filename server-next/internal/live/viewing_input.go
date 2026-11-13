package live

import (
	"context"
	"io"
	"time"
)

// ViewingInput applies the owner's initial live buffer to a viewer, whether playing
// the source or converting it. Recording and measurement keep reading Input directly.
type ViewingInput struct{ *Input }

func (in ViewingInput) ReadFrom(ctx context.Context, at time.Time, w io.Writer, began func(time.Time)) error {
	if at.IsZero() && in.t.tuning().Buffer > 0 {
		stop := context.AfterFunc(ctx, func() { in.mu.Lock(); in.changed.Broadcast(); in.mu.Unlock() })
		in.mu.Lock()
		for ctx.Err() == nil && in.stopped == nil {
			buffer := in.t.tuning().Buffer
			if len(in.segments) > 0 && in.t.now().Sub(in.segments[0].start) >= buffer {
				break
			}
			in.changed.Wait()
		}
		at = in.t.now().Add(-in.t.tuning().Buffer)
		in.mu.Unlock()
		stop()
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return in.Input.ReadFrom(ctx, at, w, began)
}
