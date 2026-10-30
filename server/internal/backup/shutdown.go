package backup

import "context"

// Shutdown stops admission, cancels detached backup work and waits for every
// admitted backup, including a worker not yet started. Call after HTTP drain
// and before closing the database. A timeout does not release a native copy's
// connections or blob hold: its owner still has to unwind them.
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.stopping = true
	s.stop()
	idle := s.idle
	s.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Caller holds s.mu; the first admission opens a fresh join generation.
func (s *Service) admitLocked() {
	if s.active == 0 {
		s.idle = make(chan struct{})
	}
	s.active++
}

func (s *Service) leave() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	if s.active == 0 {
		close(s.idle)
	}
}

func (s *Service) enter(parent context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return nil, nil, context.Canceled
	}
	s.admitLocked()
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(s.lifetime, cancel)
	return operationContext{Context: ctx, parent: parent}, func() { stop(); cancel(); s.leave() }, nil
}

// Preserve a caller context's Err semantics while adding service cancellation.
// Some contexts perform a source-fence check there; replacing it with only the
// child cancelCtx would bypass that check during copied-source verification.
type operationContext struct {
	context.Context
	parent context.Context
}

func (c operationContext) Err() error {
	if err := c.parent.Err(); err != nil {
		return err
	}
	return c.Context.Err()
}
