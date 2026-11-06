package nativeinventory

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrHelperRetirementPending means a cancelled native call exhausted its fixed
// caller grace. Its output is zero; its physical owner and helper slot remain
// charged until the existing reaper actually finishes. It is not retry authority.
var ErrHelperRetirementPending = errors.New("native storage helper retirement pending")

const RetirementGrace = 5 * time.Second

type nativeInventoryAdmissionKey struct{}

func nativeInventoryContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, nativeInventoryAdmissionKey{}, true)
}

func nativeInventoryAdmission(ctx context.Context) bool {
	native, _ := ctx.Value(nativeInventoryAdmissionKey{}).(bool)
	return native
}

// Retirement state belongs to the physical callback, never the returned page.
// The mutex makes the grace observation and eventual metric removal atomic.
type Retirement struct {
	mu       sync.Mutex
	done     chan struct{}
	monitor  *Monitor
	finished bool
	pending  bool
}

func NewRetirement(m *Monitor) *Retirement {
	return &Retirement{done: make(chan struct{}), monitor: m}
}

func (r *Retirement) Retire() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finishLocked() {
		close(r.done)
	}
}

func (r *Retirement) finishLocked() bool {
	if r.finished {
		return false
	}
	r.finished = true
	if r.pending {
		r.monitor.Active.Add(-1)
	}
	return true
}

func (r *Retirement) MarkPending() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return false
	}
	if !r.pending {
		r.pending = true
		r.monitor.Active.Add(1)
		r.monitor.OverGrace.Add(1)
	}
	return true
}

func (r *Retirement) Wait(err error) error {
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		<-r.done
		return err
	}
	select {
	case <-r.done:
		return err
	default:
	}
	// RunOwnedCompletion has already killed the cancelled child. This timer
	// bounds only its caller's subsequent wait, not Wait/consumer/permit custody.
	timer := time.NewTimer(RetirementGrace)
	defer timer.Stop()
	select {
	case <-r.done:
		return err
	case <-timer.C:
		if !r.MarkPending() {
			return err
		}
		r.monitor.Warn()
		return errors.Join(ErrHelperRetirementPending, err)
	}
}

func IsNativeContext(ctx context.Context) bool { return nativeInventoryAdmission(ctx) }
