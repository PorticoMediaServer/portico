package dbwork

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func waitReaderClassCounts(t *testing.T, s *readerScope, foreground, background int) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		s.mu.Lock()
		f, b, total := s.waitingForeground, s.waitingBackground, s.waiting
		s.mu.Unlock()
		if f == foreground && b == background && total == f+b {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("reader waits foreground=%d background=%d total=%d, want %d/%d", f, b, total, foreground, background)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReaderPressureWaiterClassCapsReleaseOnCancelAndResume(t *testing.T) {
	s := &readerScope{active: make(map[*readerLease]struct{})}
	reopen, _, _ := s.pause()
	if reopen == nil {
		t.Fatal("pause not acquired")
	}
	defer reopen(true)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	results := make(chan error, 28)
	start := func(ctx context.Context, class Class) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := s.admit(WithClass(ctx, class), true)
			if lease != nil {
				lease.close()
			}
			results <- err
		}()
	}
	defer func() { cancel(); wg.Wait() }()
	for i := 0; i < readerForegroundWaiterLimit; i++ {
		start(ctx, ClassInteractive)
	}
	for i := 0; i < readerBackgroundWaiterLimit; i++ {
		start(ctx, ClassBackgroundMedia)
	}
	waitReaderClassCounts(t, s, 24, 2)
	for _, class := range []Class{ClassForegroundTransfer, ClassMaintenance} {
		if lease, err := s.admit(WithClass(ctx, class), true); lease != nil || !errors.Is(err, ErrReadPressure) {
			if lease != nil {
				lease.close()
			}
			t.Fatalf("excess %s wait admitted: %v", class, err)
		}
	}
	// Fresh authority continues even when both ordinary waiting classes are full.
	lease, err := s.admit(WithClass(ctx, ClassSecurityFence), true)
	if err != nil || lease == nil {
		t.Fatal("authority read was queued behind reset", err)
	}
	lease.close()
	cancel()
	wg.Wait()
	for i := 0; i < 26; i++ {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	waitReaderClassCounts(t, s, 0, 0)
	s.mu.Lock()
	refused := s.refused
	s.mu.Unlock()
	if refused != 2 {
		t.Fatalf("refusal count %d", refused)
	}
	// Cancellation returned the exact allowances. Reopening admits fresh waiters
	// rather than leaving an exhausted class until the next reset.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	start(ctx2, ClassInteractive)
	start(ctx2, ClassMaintenance)
	waitReaderClassCounts(t, s, 1, 1)
	reopen(true)
	wg.Wait()
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	waitReaderClassCounts(t, s, 0, 0)
}

func TestReaderPressureTimeoutReturnsBackgroundAllowance(t *testing.T) {
	s := &readerScope{active: make(map[*readerLease]struct{})}
	reopen, _, _ := s.pause()
	defer reopen(true)
	if lease, err := s.admit(WithClass(context.Background(), ClassMaintenance), true); lease != nil || !errors.Is(err, ErrReadPressure) {
		t.Fatal(lease, err)
	}
	waitReaderClassCounts(t, s, 0, 0)
}
