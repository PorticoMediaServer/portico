package playbackv1

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// One lifetime owns queue resume/build/sweep and the session lease sweeper.
// Closing admission before waiting includes work whose goroutine has not run yet.
type backgroundWorkers struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	active int
	closed bool
}

func (s *Service) beginBackground() (context.Context, func(), bool) {
	b := &s.background
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, nil, false
	}
	if b.ctx == nil {
		b.ctx, b.cancel = context.WithCancel(context.Background())
		b.done = make(chan struct{})
	}
	b.active++
	return b.ctx, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.active--
		if b.closed && b.active == 0 {
			close(b.done)
		}
	}, true
}

func (s *Service) startWorker(name string, run func(context.Context)) bool {
	ctx, finish, ok := s.beginBackground()
	if !ok {
		return false
	}
	supervise.Go(name, func() { defer finish(); run(ctx) })
	return true
}

func (s *Service) stopBackground() <-chan struct{} {
	b := &s.background
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		if b.cancel != nil {
			b.cancel()
		}
		if b.done == nil {
			b.done = make(chan struct{})
		}
		if b.active == 0 {
			close(b.done)
		}
	}
	return b.done
}

// Shutdown stops background admission, cancels and joins the workers, and saves
// pending observations. Call after HTTP drain and before closing the database.
// A deadline reports an incomplete join; it does not pretend a stuck worker ended.
func (s *Service) Shutdown(ctx context.Context) error {
	done := s.stopBackground()
	observationsErr := s.CloseObservations(ctx)
	select {
	case <-done:
		s.build.mu.Lock()
		captures := s.build.captures
		s.build.captures = nil
		s.build.mu.Unlock()
		for _, capture := range captures {
			capture.close()
		}
		return observationsErr
	case <-ctx.Done():
		return errors.Join(observationsErr, ctx.Err())
	}
}

func waitWorkerRetry(ctx context.Context, wait time.Duration) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
