package storage

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

const nativeRetirementGrace = 5 * time.Second

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
type nativeRetirement struct {
	mu         sync.Mutex
	done       chan struct{}
	supervisor *Supervisor
	finished   bool
	pending    bool
}

func newNativeRetirement(s *Supervisor) *nativeRetirement {
	return &nativeRetirement{done: make(chan struct{}), supervisor: s}
}

func (r *nativeRetirement) retire() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finishLocked() {
		close(r.done)
	}
}

func (r *nativeRetirement) finishLocked() bool {
	if r.finished {
		return false
	}
	r.finished = true
	if r.pending {
		r.supervisor.nativeRetiring.Add(-1)
	}
	return true
}

func (r *nativeRetirement) markPending() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return false
	}
	if !r.pending {
		r.pending = true
		r.supervisor.nativeRetiring.Add(1)
		r.supervisor.nativeRetirementOverGrace.Add(1)
	}
	return true
}

func (r *nativeRetirement) wait(err error) error {
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
	timer := time.NewTimer(nativeRetirementGrace)
	defer timer.Stop()
	select {
	case <-r.done:
		return err
	case <-timer.C:
		if !r.markPending() {
			return err
		}
		r.supervisor.warnNativeRetirement()
		return errors.Join(ErrHelperRetirementPending, err)
	}
}
