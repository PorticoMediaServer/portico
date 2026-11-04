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
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	active     int
	closed     bool
	retirement *backgroundRetirement
}

// One attempt owns all physical retirement. A failed final flush may be retried
// only after that attempt has physically finished; concurrent callers join it.
type backgroundRetirement struct {
	done chan struct{}
	err  error // published by closing done
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
	observationsDone := s.stopObservations()
	b := &s.background
	b.mu.Lock()
	attempt := b.retirement
	start := attempt == nil
	if attempt != nil {
		select {
		case <-attempt.done:
			start = attempt.err != nil
		default:
		}
	}
	if start {
		attempt = &backgroundRetirement{done: make(chan struct{})}
		b.retirement = attempt
	}
	b.mu.Unlock()
	if start {
		supervise.Go("playback.background-retirement", func() {
			defer close(attempt.done)
			// Context cancellation may mark a SQL Tx done before the driver rollback
			// returns. Joining the observation worker and flush stays inside this task,
			// so no caller can outwait its deadline while physical cleanup continues.
			if observationsDone != nil {
				<-observationsDone
			}
			attempt.err = s.FlushObservations(ctx)
			<-done
			s.build.mu.Lock()
			captures := s.build.captures
			s.build.captures = nil
			s.build.mu.Unlock()
			for _, capture := range captures {
				capture.close()
			}
		})
	}
	select {
	case <-attempt.done:
		return errors.Join(attempt.err, ctx.Err())
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ShutdownAfterHTTPDrain preserves admitted requests before joining playback.
// The playback budget starts after the HTTP drain's independent budget.
func (s *Service) ShutdownAfterHTTPDrain(ctx context.Context, drained <-chan struct{}, budget time.Duration) error {
	select {
	case <-drained:
	case <-ctx.Done():
		return ctx.Err()
	}
	stop, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return s.Shutdown(stop)
}

func waitWorkerRetry(ctx context.Context, wait time.Duration) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
