package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"
)

// waitForWaiters blocks until the gate reports n queued waiters, so a test never
// depends on a sleep to establish ordering.
func waitForWaiters(t *testing.T, g *Gate, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		total := 0
		for _, count := range g.Waiting() {
			total += count
		}
		if total >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("gate never reported %d waiters: %#v", n, g.Waiting())
}

func TestGateStrictPriorityAcrossClasses(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	order := []Class{}
	launched := 1
	var wg sync.WaitGroup
	// Queue the lowest priorities first: the gate must still serve the fence and
	// playback ahead of them when the holder releases.
	for _, class := range []Class{ClassMaintenance, ClassBackgroundMedia, ClassInteractive, ClassEstablishedPlayback, ClassSecurityFence} {
		wg.Add(1)
		go func(class Class) {
			defer wg.Done()
			release, err := g.Acquire(context.Background(), class)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, class)
			mu.Unlock()
			release()
		}(class)
		waitForWaiters(t, g, launched)
		launched++
	}
	hold()
	wg.Wait()
	want := []Class{ClassSecurityFence, ClassEstablishedPlayback, ClassInteractive, ClassBackgroundMedia, ClassMaintenance}
	if len(order) != len(want) {
		t.Fatalf("expected %d acquisitions, got %v", len(want), order)
	}
	for i, class := range want {
		if order[i] != class {
			t.Fatalf("priority order was %v, want %v", order, want)
		}
	}
}

// ageFront backdates the ticket class would serve next, so a test can cross a
// promotion boundary without sleeping through it.
func ageFront(g *Gate, class Class, by time.Duration) {
	g.mu.Lock()
	g.queues[class].front().since = time.Now().Add(-by)
	g.mu.Unlock()
}

func TestAgedBackgroundNeverPassesAWaitingUser(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	order := make(chan Class, 3)
	var wg sync.WaitGroup
	for i, class := range []Class{ClassMaintenance, ClassInteractive, ClassSecurityFence} {
		wg.Add(1)
		go func(class Class) {
			defer wg.Done()
			release, e := g.Acquire(context.Background(), class)
			if e != nil {
				t.Error(e)
				return
			}
			order <- class
			release()
		}(class)
		waitForWaiters(t, g, i+1)
	}
	ageFront(g, ClassMaintenance, time.Hour)
	hold()
	wg.Wait()
	close(order)
	got := []Class{}
	for class := range order {
		got = append(got, class)
	}
	want := []Class{ClassSecurityFence, ClassInteractive, ClassMaintenance}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("aged priority %v, want %v", got, want)
		}
	}
}

func TestAgedBackgroundWaitsBehindEveryQueuedRequest(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan Class, 51)
	done := make(chan struct{}, 51)
	acquire := func(class Class) {
		release, err := g.Acquire(context.Background(), class)
		if err == nil {
			served <- class
			release()
		}
		done <- struct{}{}
	}
	go acquire(ClassBackgroundMedia)
	waitForWaiters(t, g, 1)
	ageFront(g, ClassBackgroundMedia, time.Hour)
	for i := 0; i < 50; i++ {
		go acquire(ClassInteractive)
	}
	waitForWaiters(t, g, 51)
	hold()
	for i := 0; i < 51; i++ {
		<-done
	}
	close(served)
	position := 0
	for class := range served {
		if class == ClassBackgroundMedia && position != 50 {
			t.Fatalf("an aged background writer was served at position %d, ahead of queued requests", position)
		}
		position++
	}
}

// Users always go first, but background work is only deferred: the moment no
// user write is waiting, the oldest background writer runs. The user load here
// is a chain in which a new request always queues before the holder leaves, so
// the background writer must wait through every link and then win at once.
func TestBackgroundProgressesAfterUserPressureEnds(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	background := make(chan struct{})
	go func() {
		release, err := g.Acquire(context.Background(), ClassBackgroundMedia)
		if err != nil {
			t.Error(err)
			return
		}
		release()
		close(background)
	}()
	waitForWaiters(t, g, 1)
	ageFront(g, ClassBackgroundMedia, time.Hour)
	for link := 0; link < 20; link++ {
		next := make(chan func())
		go func() {
			release, err := g.Acquire(context.Background(), ClassInteractive)
			if err != nil {
				t.Error(err)
				close(next)
				return
			}
			next <- release
		}()
		waitForWaiters(t, g, 2)
		hold()
		release, ok := <-next
		if !ok {
			t.FailNow()
		}
		hold = release
		select {
		case <-background:
			t.Fatalf("background writer ran at link %d while a request was queued", link)
		default:
		}
		if waiting := g.Waiting()[ClassBackgroundMedia.String()]; waiting != 1 {
			t.Fatalf("background writer left the queue under user pressure: %v", g.Waiting())
		}
	}
	hold()
	select {
	case <-background:
	case <-time.After(5 * time.Second):
		t.Fatal("background writer did not run once user pressure ended")
	}
}

func TestAgedMaintenanceOvertakesNewerBackgroundMedia(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	order := make(chan Class, 2)
	var wg sync.WaitGroup
	for i, class := range []Class{ClassMaintenance, ClassBackgroundMedia} {
		wg.Add(1)
		go func(class Class) {
			defer wg.Done()
			release, err := g.Acquire(context.Background(), class)
			if err != nil {
				t.Error(err)
				return
			}
			order <- class
			release()
		}(class)
		waitForWaiters(t, g, i+1)
		if class == ClassMaintenance {
			ageFront(g, ClassMaintenance, 2*GateAgePromotionInterval)
		}
	}
	hold()
	wg.Wait()
	if first := <-order; first != ClassMaintenance {
		t.Fatalf("a scan starved an aged maintenance writer: %s went first", first)
	}
}

func TestGateHandoffSurvivesPromotionBoundary(t *testing.T) {
	for n := 0; n < 100; n++ {
		g := NewGate()
		hold, err := g.Acquire(context.Background(), ClassInteractive)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		done := make(chan error, 2)
		for _, class := range []Class{ClassMaintenance, ClassBackgroundMedia} {
			go func(class Class) {
				release, err := g.Acquire(ctx, class)
				if err == nil {
					release()
				}
				done <- err
			}(class)
		}
		waitForWaiters(t, g, 2)
		// An old maintenance waiter is exactly at the promotion edge when
		// the holder releases. Both waiters must still acquire in turn.
		ageFront(g, ClassMaintenance, GateAgePromotionInterval-time.Millisecond)
		hold()
		for i := 0; i < 2; i++ {
			if err := <-done; err != nil {
				t.Fatalf("free gate stalled on iteration %d: %v", n, err)
			}
		}
		cancel()
	}
}

func TestGateIsFIFOWithinAClass(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	order := []int{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		index := i
		go func() {
			defer wg.Done()
			release, err := g.Acquire(context.Background(), ClassBackgroundMedia)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, index)
			mu.Unlock()
			release()
		}()
		// Enqueue deterministically so the expected order is the arrival order.
		waitForWaiters(t, g, index+1)
	}
	hold()
	wg.Wait()
	for i, index := range order {
		if index != i {
			t.Fatalf("arrival order not preserved inside a class: %v", order)
		}
	}
}

func TestCancelledWaiterLeavesTheQueue(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := g.Acquire(ctx, ClassSecurityFence)
		done <- err
	}()
	waitForWaiters(t, g, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter returned %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(g.Waiting()) == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("cancelled waiter still holds a place: %#v", g.Waiting())
}

func TestCancelledWaiterDoesNotBlockLowerPriority(t *testing.T) {
	// A fence that gives up must not keep an interactive writer waiting for it.
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	fenceCtx, cancelFence := context.WithCancel(context.Background())
	fenceDone := make(chan error, 1)
	go func() {
		_, err := g.Acquire(fenceCtx, ClassSecurityFence)
		fenceDone <- err
	}()
	waitForWaiters(t, g, 1)
	interactive := make(chan struct{})
	go func() {
		release, err := g.Acquire(context.Background(), ClassInteractive)
		if err != nil {
			t.Error(err)
			return
		}
		release()
		close(interactive)
	}()
	waitForWaiters(t, g, 2)
	cancelFence()
	<-fenceDone
	hold()
	select {
	case <-interactive:
	case <-time.After(5 * time.Second):
		t.Fatal("an abandoned fence blocked an interactive writer")
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	g := NewGate()
	release, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	release()
	if g.ActiveOrWaiting() {
		t.Fatal("a repeated release corrupted the gate state")
	}
	second, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatalf("gate unusable after repeated release: %v", err)
	}
	second()
}

func TestActiveOrWaitingReportsQueuedWriters(t *testing.T) {
	g := NewGate()
	if g.ActiveOrWaiting() {
		t.Fatal("a fresh gate reported a writer")
	}
	release, err := g.Acquire(context.Background(), ClassMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	if !g.ActiveOrWaiting() {
		t.Fatal("an active writer was not reported")
	}
	release()
	if g.ActiveOrWaiting() {
		t.Fatal("a released writer was still reported")
	}
}

func TestAcquireHonoursAnAlreadyCancelledContext(t *testing.T) {
	g := NewGate()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Acquire(ctx, ClassInteractive); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if g.ActiveOrWaiting() {
		t.Fatal("a cancelled acquire took the slot")
	}
}

func TestClassLadderIsOrderedAndNamed(t *testing.T) {
	previous := 0
	for _, class := range Classes() {
		if class.Priority() <= previous {
			t.Fatalf("class %s is out of order", class)
		}
		previous = class.Priority()
		parsed, ok := ParseClass(class.String())
		if !ok || parsed != class {
			t.Fatalf("class %s did not round-trip through its name", class)
		}
	}
	if !ClassBackgroundMedia.Background() || !ClassMaintenance.Background() {
		t.Fatal("bulk classes must report as background")
	}
	if ClassInteractive.Background() || ClassPlaybackStart.Background() {
		t.Fatal("foreground classes must not report as background")
	}
}

// A watchdog that cannot hand the process a replacement handle must never
// recycle: every service would keep the old *sql.DB and it would be closed
// under them.
func TestWatchdogWithoutSwapNeverRecycles(t *testing.T) {
	w := NewWatchdog(nil)
	w.Reopen = func() (*sql.DB, error) { return nil, errors.New("unused") }
	w.state = DegradedState
	if w.mayRecycleLocked(KindBusy) {
		t.Fatal("recycle allowed without a Swap")
	}
	w.Swap = func(*sql.DB) {}
	if !w.mayRecycleLocked(KindBusy) {
		t.Fatal("recycle refused although the process can adopt the new handle")
	}
	if w.mayRecycleLocked(KindCorrupt) {
		t.Fatal("corruption must never recycle")
	}
}

func TestProductionGateReportsActualStuckHold(t *testing.T) {
	t.Setenv(StrictEnv, "0")
	g := NewGate()
	release, err := g.Acquire(context.Background(), ClassMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	g.mu.Lock()
	g.heldSince = time.Now().Add(-StuckWait - time.Second)
	g.mu.Unlock()
	if err := g.StuckHolder(StuckWait); err == nil {
		t.Fatal("long hold was silent without a waiter")
	}
	if err := g.StuckHolder(StuckWait); err == nil {
		t.Fatal("health report disappeared after first log")
	}
	release()
	if err := g.StuckHolder(StuckWait); err != nil {
		t.Fatal("released gate still unwell", err)
	}
}
