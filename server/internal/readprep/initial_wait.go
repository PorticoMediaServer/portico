package readprep

import (
	"context"
	"time"
)

// InitialReadyGrace is a short opportunity to deliver an already-finishing
// tiny preparation in its initial request. It never changes a request deadline
// or the polling interval, and never runs a producer on the request goroutine.
const InitialReadyGrace = 50 * time.Millisecond

// Covers the timer and transient notification/control allocations.
const initialWaitBytes int64 = 512

// PollWhenReady waits only for an initial submission's worker completion. It
// holds no database snapshot, delivery or store lock while waiting. Admission,
// computation, caller binding, expiry and delivery charging remain Poll's.
// Slow work returns the ordinary Preparing result after the fixed short grace.
func (s *Store) PollWhenReady(ctx context.Context, caller Caller, h Handle, grace time.Duration) (Result, error) {
	if grace <= 0 {
		return s.Poll(ctx, caller, h)
	}
	if grace > InitialReadyGrace {
		grace = InitialReadyGrace
	}
	if err := s.waitInitialReady(ctx, caller, h, grace); err != nil {
		return Result{}, err
	}
	return s.Poll(ctx, caller, h)
}

func (s *Store) waitInitialReady(ctx context.Context, caller Caller, h Handle, grace time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.prune(s.cfg.Now())
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	l := s.handles[h]
	if l == nil || caller == "" || l.caller != caller {
		s.mu.Unlock()
		return ErrStale
	}
	if l.e.complete || l.e.err != nil || l.waiting || !s.room(initialWaitBytes, l.e) {
		s.mu.Unlock()
		return nil
	}
	// One initial waiter per private handle. Its transient control reservation
	// remains charged even when cancellation retires the handle underneath it.
	l.waiting = true
	s.initialWaiters++
	s.used += initialWaitBytes
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		l.waiting = false
		s.used -= initialWaitBytes
		s.initialWaiters--
		if s.initialWaiters == 0 {
			s.retireChanged = nil
		}
		s.mu.Unlock()
	}()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	for {
		s.mu.Lock()
		s.prune(s.cfg.Now())
		if s.closed {
			s.mu.Unlock()
			return ErrClosed
		}
		if s.handles[h] != l {
			s.mu.Unlock()
			return ErrStale
		}
		if l.e.complete || l.e.err != nil {
			s.mu.Unlock()
			return nil
		}
		if l.e.ready == nil {
			l.e.ready = make(chan struct{})
		}
		if s.retireChanged == nil {
			s.retireChanged = make(chan struct{})
		}
		ready, retired := l.e.ready, s.retireChanged
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-ready:
		case <-retired:
		}
	}
}

// Called with mu held. Completion channels have no payload and fit the fixed
// per-entry metadata charge. Retirement also wakes waiters on a single released
// handle when other callers still own the shared computation.
func (s *Store) notifyRetirementLocked() {
	if s.initialWaiters > 0 && s.retireChanged != nil {
		close(s.retireChanged)
		s.retireChanged = make(chan struct{})
	}
}
func signalEntryReadyLocked(e *entry) {
	if e.ready != nil {
		close(e.ready)
		e.ready = nil
	}
}
