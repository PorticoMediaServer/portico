package storage

import (
	"context"
	"sync"
)

// OperationScope tracks physical helper debt for one runtime owner, including
// setup failures that never create a registration entry. Completion follows
// actual Wait/no-start and supervisor permit retirement, not caller cancellation.
type OperationScope struct {
	mu     sync.Mutex
	life   context.Context
	cancel context.CancelFunc
	active int
	closed bool
	done   chan struct{}
}

func NewOperationScope() *OperationScope {
	life, cancel := context.WithCancel(context.Background())
	return &OperationScope{life: life, cancel: cancel, done: make(chan struct{})}
}
func (s *OperationScope) Context() context.Context { return s.life }
func (s *OperationScope) Begin() (func(), error) {
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
func (s *OperationScope) Close() {
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
func (s *OperationScope) Wait(ctx context.Context) error {
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *OperationScope) Active() int { s.mu.Lock(); defer s.mu.Unlock(); return s.active }

// scopeOwnerLifetime checks direct owner cancellation even before AfterFunc has
// propagated it to a helper's wakeup context.
type scopeOwnerLifetime struct {
	context.Context
	scope context.Context
}

func (c *scopeOwnerLifetime) Err() error {
	if err := c.scope.Err(); err != nil {
		return err
	}
	return c.Context.Err()
}
