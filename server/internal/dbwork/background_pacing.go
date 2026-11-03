package dbwork

import (
	"context"
	"time"
)

// PaceBackground gives foreground requests a brief scheduling opportunity after
// a completed background quantum. Call it only outside transactions, after the
// batch has released its writer/read leases. With no foreground pressure the
// next batch starts immediately. Under sustained pressure every batch still
// progresses: the pause is bounded and cancellation interrupts it.
//
// Yield remains a nonblocking cancellation boundary; this separate operation is
// intended for the continuous bulk/derived-data loops that can otherwise consume
// CPU and fill the WAL as quickly as they commit.
func PaceBackground(ctx context.Context, batchStarted time.Time) bool {
	if !WaitBackground(ctx) {
		return false
	}
	if !ForegroundWorkActive() {
		return true
	}
	elapsed := time.Since(batchStarted)
	if slice, _ := ctx.Value(paceSliceKey{}).(*paceSlice); slice != nil {
		// Short quanta are paced as one slice of work: a pause after each
		// would cost far more than the quantum itself.
		slice.ran += elapsed
		if slice.ran < paceSliceMinimum {
			return ctx.Err() == nil
		}
		elapsed, slice.ran = slice.ran, 0
	}
	yields.Add(1)
	pause := 50 * time.Millisecond
	switch BackgroundSpeed(ctx) {
	case SpeedGentle:
		pause = 100 * time.Millisecond
	case SpeedFast:
		pause = 20 * time.Millisecond
	}
	// Compare before multiplication so even a stale start cannot overflow.
	if elapsed < pause/3 {
		pause = max(2*time.Millisecond, 3*elapsed)
	}
	started := time.Now()
	defer func() { yieldWaitNanos.Add(uint64(time.Since(started))) }()
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

// paceSliceMinimum is the work a sliced loop does between pauses.
const paceSliceMinimum = 5 * time.Millisecond

type paceSliceKey struct{}

type paceSlice struct{ ran time.Duration }

// WithPaceSlices makes PaceBackground under ctx pause once per slice of at
// least paceSliceMinimum of work instead of after every quantum. ctx must
// belong to one loop goroutine.
func WithPaceSlices(ctx context.Context) context.Context {
	return context.WithValue(ctx, paceSliceKey{}, &paceSlice{})
}
