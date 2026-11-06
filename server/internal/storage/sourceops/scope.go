// Package sourceops owns physical source-helper debt without loading the
// storage client, supervisor policy or legacy database initialization.
package sourceops

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrRootLease is shared with the legacy storage API by identity.
var ErrRootLease = errors.New("source root registration lease required")

// Scope tracks physical helper debt for one runtime owner, including
// setup failures that never create a registration entry. Completion follows
// actual Wait/no-start and supervisor permit retirement, not caller cancellation.
type Scope struct {
	mu     sync.Mutex
	life   context.Context
	cancel context.CancelFunc
	active int
	closed bool
	done   chan struct{}
}

func New() *Scope {
	life, cancel := context.WithCancel(context.Background())
	return &Scope{life: life, cancel: cancel, done: make(chan struct{})}
}

func (s *Scope) Context() context.Context { return s.life }

// Lease is the existing once-only physical-debt release, carried as a typed
// value so the pure observer never receives or invokes a callback slot.
type Lease struct {
	scope *Scope
	once  sync.Once
}

func (s *Scope) Acquire() (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrRootLease
	}
	s.active++
	return &Lease{scope: s}, nil
}

func (l *Lease) Release() {
	l.once.Do(func() {
		s := l.scope
		s.mu.Lock()
		s.active--
		if s.closed && s.active == 0 {
			close(s.done)
		}
		s.mu.Unlock()
	})
}

// Begin preserves the original API, release identity and shared accounting.
func (s *Scope) Begin() (func(), error) {
	lease, err := s.Acquire()
	if err != nil {
		return nil, err
	}
	return lease.Release, nil
}

// Deadline holds exactly the original context cancellation and AfterFunc stop.
// It owns no scope, budget, process or additional physical-debt ledger.
type Deadline struct {
	ctx    context.Context
	cancel context.CancelFunc
	stop   func() bool
}

func NewDeadline(ctx context.Context, timeout time.Duration) *Deadline {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	return &Deadline{ctx: ctx, cancel: cancel}
}
func (d *Deadline) Context() context.Context { return d.ctx }
func (d *Deadline) Cancel()                  { d.cancel() }
func (d *Deadline) BindOwner(owner *Scope)   { d.stop = context.AfterFunc(owner.Context(), d.cancel) }
func (d *Deadline) UnbindOwner()             { d.stop() }

func (s *Scope) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
		if s.active == 0 {
			close(s.done)
		}
	}
	s.mu.Unlock()
}
func (s *Scope) Wait(ctx context.Context) error {
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Scope) Active() int { s.mu.Lock(); defer s.mu.Unlock(); return s.active }
