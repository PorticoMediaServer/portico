package dbwork

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func expectNoResult(t *testing.T, results <-chan admissionResult, wait time.Duration) {
	t.Helper()
	select {
	case r := <-results:
		t.Fatalf("%s ran during the recovery hold: %v", r.label, r.err)
	case <-time.After(wait):
	}
}

func expectResult(t *testing.T, results <-chan admissionResult, label string) {
	t.Helper()
	select {
	case r := <-results:
		if r.label != label || r.err != nil {
			t.Fatalf("got %s (%v), want %s", r.label, r.err, label)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never ran", label)
	}
}

// A pressure hold keeps every new writer queued but lets a security fence
// through; the final pause keeps even the fence queued; only the reserved turn
// runs, it moves the write generation once, and the queue resumes after.
func TestRecoveryHoldPauseAndReservedTurn(t *testing.T) {
	g := NewGate()
	ctx := context.Background()
	results := make(chan admissionResult, 8)
	holder, err := g.Acquire(ctx, ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := g.enterPressureHold()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.enterPressureHold(); err == nil {
		t.Fatal("a second recovery attempt started")
	}
	queueTicket(t, g, ClassInteractive, "viewer", "interactive", closedChannel(), results)
	queueTicket(t, g, ClassSecurityFence, "", "fence-1", closedChannel(), results)
	holder()
	// The holder finished normally; the fence passes the hold, the user waits.
	expectResult(t, results, "fence-1")
	expectNoResult(t, results, 50*time.Millisecond)
	if err = g.waitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if err = g.enterBeginPause(epoch); err != nil {
		t.Fatal(err)
	}
	queueTicket(t, g, ClassSecurityFence, "", "fence-2", closedChannel(), results)
	expectNoResult(t, results, 50*time.Millisecond)
	fence, _ := g.checkpointCopyFence()
	release, err := g.acquireRecoveryTurn(ctx, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if generation, _ := g.checkpointCopyFence(); generation != fence+1 {
		t.Fatalf("recovery turn moved the generation %d -> %d", fence, generation)
	}
	expectNoResult(t, results, 20*time.Millisecond)
	release()
	// Still paused: releasing the turn admits nothing until the owner ends it.
	expectNoResult(t, results, 20*time.Millisecond)
	g.endRecovery(epoch)
	expectResult(t, results, "fence-2")
	expectResult(t, results, "interactive")
	if hold, pause := g.RecoveryState(); hold || pause {
		t.Fatal("recovery state survived its end")
	}
}

// The security reserve is finite: past it, fences wait for the recovery too.
func TestRecoveryHoldSecurityReserveIsFinite(t *testing.T) {
	g := NewGate()
	epoch, err := g.enterPressureHold()
	if err != nil {
		t.Fatal(err)
	}
	defer g.endRecovery(epoch)
	for i := 0; i < RecoverySecurityReserve; i++ {
		release, err := g.Acquire(context.Background(), ClassSecurityFence)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err = g.Acquire(ctx, ClassSecurityFence); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fence past the reserve: %v", err)
	}
}

// A hold withdraws a grant already reserved for a ticket it excludes, and a
// pause withdraws a fence's; ending the attempt restores them.
func TestRecoveryWithdrawsPendingGrants(t *testing.T) {
	g := NewGate()
	g.mu.Lock()
	g.nextID++
	user := &ticket{id: g.nextID, class: ClassInteractive, since: time.Now()}
	g.enqueueLocked(user)
	g.grant = user
	g.mu.Unlock()
	epoch, err := g.enterPressureHold()
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	if g.grant == user {
		t.Fatal("hold kept a user's pending grant")
	}
	g.mu.Unlock()
	g.endRecovery(epoch)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.grant != user {
		t.Fatal("ending the attempt did not restore the queue")
	}
}

// The reserved turn exists only inside a pause, once; cancelling it or ending
// the attempt while it waits leaves the gate usable.
func TestRecoveryTurnBoundaries(t *testing.T) {
	g := NewGate()
	ctx := context.Background()
	epoch, err := g.enterPressureHold()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.acquireRecoveryTurn(ctx, epoch); !errors.Is(err, errRecoveryEnded) {
		t.Fatalf("a turn outside the pause: %v", err)
	}
	if err = g.enterBeginPause(epoch + 1); !errors.Is(err, errRecoveryEnded) {
		t.Fatal("another owner's epoch paused the gate")
	}
	holder, err := g.Acquire(ctx, ClassSecurityFence)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.enterBeginPause(epoch); err != nil {
		t.Fatal(err)
	}
	// The turn waits behind the current holder; cancellation removes it.
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	if _, err = g.acquireRecoveryTurn(waitCtx, epoch); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("turn behind a holder: %v", err)
	}
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := g.acquireRecoveryTurn(ctx, epoch)
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	g.endRecovery(epoch)
	if err = <-done; !errors.Is(err, errRecoveryEnded) {
		t.Fatalf("turn after the attempt ended: %v", err)
	}
	holder()
	release, err := g.Acquire(ctx, ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

// r3 falsifier 11: with users writing continuously and the log at hard
// pressure, the reset is no longer queued behind them. The attempt holds new
// writers, takes the one reserved turn, proves generation==copyFence+1 and
// resets; the writers carry on afterwards with nothing refused.
func TestHardPressureResetsUnderSustainedUserWrites(t *testing.T) {
	db := readerFixture(t)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	failures := make(chan error, 4)
	var written [4]int64
	done := make(chan struct{})
	var running sync.WaitGroup
	for writer := range 4 {
		running.Add(1)
		go func() {
			defer running.Done()
			for ctx.Err() == nil {
				if _, err := ExecWrite(ctx, db, ClassInteractive, `INSERT INTO sample VALUES(?)`, writer); err != nil {
					if ctx.Err() == nil {
						failures <- err
					}
					return
				}
				atomic.AddInt64(&written[writer], 1)
			}
		}()
	}
	go func() { running.Wait(); close(done) }()
	total := func() (n int64) {
		for i := range written {
			n += atomic.LoadInt64(&written[i])
		}
		return n
	}
	for total() < 50 {
		time.Sleep(time.Millisecond)
	}
	if !WriteGate().ActiveOrWaiting() {
		t.Log("writers momentarily idle; the attempt still has to win against them")
	}
	result := checkpoint(context.Background(), db, schedulePressureFile(t))
	if result.Err != nil || result.Mode != "truncate" || result.Outcome != "truncated" {
		t.Fatalf("sustained user writes kept the reset from running: %+v", result)
	}
	after := total()
	for total() < after+50 {
		select {
		case err := <-failures:
			t.Fatal(err)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	stop()
	<-done
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	if hold, pause := WriteGate().RecoveryState(); hold || pause {
		t.Fatal("recovery outlived its attempt")
	}
}
