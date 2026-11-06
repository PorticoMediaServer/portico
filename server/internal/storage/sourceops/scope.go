// Package sourceops owns physical source-helper debt without loading the
// storage client, supervisor policy or legacy database initialization.
package sourceops

import (
	"context"
	"errors"
	"sync"
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
func (s *Scope) Begin() (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrRootLease
	}
	s.active++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.active--
			if s.closed && s.active == 0 {
				close(s.done)
			}
			s.mu.Unlock()
		})
	}, nil
}
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
