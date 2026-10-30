package readprep

import (
	"context"
	"testing"
	"time"
)

// InitialReadyGrace is a short opportunity to deliver an already-finishing
// tiny preparation in its initial request. It never changes a request deadline
// or the polling interval, and never runs a producer on the request goroutine.
const InitialReadyGrace = 50 * time.Millisecond

// Covers the timer and transient notification/control allocations.
const initialWaitBytes int64 = 512

// WaitPreparedForTest is a test fixture's wait on the real preparation behind
// an in-flight private handle. A fixture calls it after an ordinary handler has
// returned and before its next ordinary authenticated poll, so the existing
// poll contract, headers and deadline stay exactly as production serves them.
// It returns no payload, grants no access and charges no delivery: the next
// Poll still binds its caller and authority and takes its own delivery.
//
// The wait reuses the admitted worker's completion/retirement signals and the
// transient control reservation of waitEntryReady; it adds no worker, timer,
// cache, channel registry or persistent state, and it never runs a producer on
// the caller's goroutine. Only the handle's own lease yields the caller, so a
// foreign caller learns nothing. The caller's context (a fixture's existing
// deadline) alone bounds the wait; this seam adds none.
//
// Outside a test binary the seam is refused. ErrCapacity means the bounded
// transient control reservation did not fit, so the fixture must report that
// refusal instead of spinning or sleeping on Retry-After.
func (s *Store) WaitPreparedForTest(ctx context.Context, h Handle) error {
	if !testing.Testing() {
		return ErrInvalid
	}
	if h == "" {
		return ErrStale
	}
	s.mu.Lock()
	l := s.handles[h]
	if l == nil {
		s.mu.Unlock()
		return ErrStale
	}
	caller := l.caller
	s.mu.Unlock()
	settled, err := s.waitEntryReady(ctx, caller, h, 0)
	if err != nil {
		return err
	}
	if !settled {
		return ErrCapacity
	}
	return nil
}

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
	_, err := s.waitEntryReady(ctx, caller, h, grace)
	return err
}

// waitEntryReady waits only for one private handle's worker completion. It
// holds no database snapshot, delivery or store lock while waiting. Admission,
// computation, caller binding, expiry and delivery charging remain Poll's.
// A positive grace bounds the wait; zero waits until the worker completes,
// fails, retires or ctx ends. Only the test-only fixture seam passes zero.
// settled is false only when the transient control reservation could not be
// admitted, so a waiting fixture refuses instead of repeating the wait.
func (s *Store) waitEntryReady(ctx context.Context, caller Caller, h Handle, grace time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	s.prune(s.cfg.Now())
	if s.closed {
		s.mu.Unlock()
		return false, ErrClosed
	}
	l := s.handles[h]
	if l == nil || caller == "" || l.caller != caller {
		s.mu.Unlock()
		return false, ErrStale
	}
	if l.e.complete || l.e.err != nil || l.waiting {
		s.mu.Unlock()
		return true, nil
	}
	if !s.room(initialWaitBytes, l.e) {
		s.mu.Unlock()
		return false, nil
	}
	// One wait per private handle. Its transient control reservation remains
	// charged even when cancellation retires the handle underneath it.
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
	var expired <-chan time.Time
	if grace > 0 {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		expired = timer.C
	}
	for {
		s.mu.Lock()
		s.prune(s.cfg.Now())
		if s.closed {
			s.mu.Unlock()
			return false, ErrClosed
		}
		if s.handles[h] != l {
			s.mu.Unlock()
			return false, ErrStale
		}
		if l.e.complete || l.e.err != nil {
			s.mu.Unlock()
			return true, nil
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
			return false, ctx.Err()
		case <-expired:
			return true, nil
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
