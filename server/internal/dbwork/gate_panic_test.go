package dbwork

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStuckWaitPanicRemovesWaiterAndLeavesGateUsable(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	stuck := make(chan time.Time, 1)
	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		release, _ := g.acquire(context.Background(), ClassBackgroundMedia, stuckClock{C: stuck})
		if release != nil {
			release()
		}
	}()
	waitForWaiters(t, g, 1)
	g.mu.Lock()
	g.heldSince = time.Now().Add(-StuckWait - time.Second)
	g.mu.Unlock()
	stuck <- time.Now()
	select {
	case value := <-panicked:
		if value == nil {
			t.Fatal("stuck waiter did not panic")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stuck waiter did not unwind")
	}
	for class, count := range g.Waiting() {
		if count != 0 {
			t.Fatalf("panic left %d waiters in %s", count, class)
		}
	}
	hold()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := g.Acquire(ctx, ClassBackgroundMedia)
	if err != nil {
		t.Fatalf("abandoned waiter stranded the next writer: %v", err)
	}
	release()
}

// With users always ahead, a background writer can wait longer than StuckWait
// behind many short holds. That is load, not a leak: the stuck check must judge
// the current hold, keep waiting, and look again when that hold could first
// qualify.
func TestStuckWaitJudgesTheHoldNotTheWait(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	stuck := make(chan time.Time, 1)
	var mu sync.Mutex
	var rearmed []time.Duration
	clock := stuckClock{C: stuck, reset: func(d time.Duration) {
		mu.Lock()
		rearmed = append(rearmed, d)
		mu.Unlock()
	}}
	result := make(chan any, 1)
	go func() {
		defer func() {
			if value := recover(); value != nil {
				result <- value
			}
		}()
		release, err := g.acquire(context.Background(), ClassBackgroundMedia, clock)
		if err != nil {
			result <- err
			return
		}
		release()
		result <- nil
	}()
	waitForWaiters(t, g, 1)
	// The waiter itself has waited far past StuckWait; the hold is a second old.
	g.mu.Lock()
	g.queues[ClassBackgroundMedia].front().since = time.Now().Add(-10 * StuckWait)
	g.heldSince = time.Now().Add(-time.Second)
	g.mu.Unlock()
	stuck <- time.Now()
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		count := len(rearmed)
		mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stuck check never re-armed")
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	next := rearmed[0]
	mu.Unlock()
	if next <= 0 || next > StuckWait-time.Second+100*time.Millisecond {
		t.Fatalf("re-armed for %s, want the rest of the current hold's allowance", next)
	}
	select {
	case value := <-result:
		t.Fatalf("a long wait behind a short hold ended the waiter: %v", value)
	default:
	}
	if g.Waiting()[ClassBackgroundMedia.String()] != 1 {
		t.Fatalf("waiter left the queue after a false stuck alarm: %v", g.Waiting())
	}
	hold()
	select {
	case value := <-result:
		if value != nil {
			t.Fatalf("waiter failed after the hold ended: %v", value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not acquire after the hold ended")
	}
}
