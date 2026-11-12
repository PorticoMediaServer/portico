package settings

import (
	"context"
	"time"
)

// PauseBackground holds heavy work and scheduled backups for one hour. The pause lives
// only in memory, so opening the server's settings after a restart starts unpaused.
func (s *Settings) PauseBackground(now time.Time, paused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pausedUntil = time.Time{}
	if paused {
		s.pausedUntil = now.Add(time.Hour)
	}
}
func (s *Settings) BackgroundPausedUntil(now time.Time) time.Time {
	if s == nil {
		return time.Time{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pausedUntil.After(now) {
		return s.pausedUntil
	}
	return time.Time{}
}

// WaitForBackground waits without holding a settings or database lock. A resume is noticed
// within a second, and cancellation always ends the wait.
func (s *Settings) WaitForBackground(ctx context.Context, now func() time.Time) error {
	for {
		until := s.BackgroundPausedUntil(now())
		if until.IsZero() {
			return ctx.Err()
		}
		timer := time.NewTimer(min(until.Sub(now()), time.Second))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
