package remotemedia

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExclusiveSourceTestDrainsWithoutRetiringReaderAndQueuesNewWork(t *testing.T) {
	life, stop := context.WithCancel(context.Background())
	defer stop()
	budget := NewOperationBudget(life, 1)
	reader, release, err := budget.enter(life)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tests := make(chan *ExclusiveTest, 1)
	errs := make(chan error, 1)
	go func() { hold, err := budget.AcquireTest(life); tests <- hold; errs <- err }()
	deadline := time.After(time.Second)
	for {
		budget.mu.Lock()
		draining := budget.exclusive != nil
		budget.mu.Unlock()
		if draining {
			break
		}
		select {
		case <-deadline:
			t.Fatal("test did not begin drain")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	gateObserved := time.Now()
	// The in-flight operation's actual reconnect/metadata fence can still finish.
	_, nested, err := budget.enter(reader)
	if err != nil {
		t.Fatal(err)
	}
	nested()
	queued := make(chan error, 1)
	go func() {
		_, done, err := budget.enter(life)
		if err == nil {
			done()
		}
		queued <- err
	}()
	select {
	case <-queued:
		t.Fatal("new operation bypassed drain")
	case <-time.After(20 * time.Millisecond):
	}
	if reader.Err() != nil {
		t.Fatal("test canceled existing reader", reader.Err())
	}
	release()
	hold := <-tests
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	if deadline, ok := hold.Context().Deadline(); !ok || deadline.After(gateObserved.Add(15*time.Second)) {
		t.Fatal("exclusive bound omitted drain time", deadline)
	}
	if _, err := budget.AcquireTest(life); !errors.Is(err, ErrBusy) {
		t.Fatal("overlapping tests acquired independent hold", err)
	}
	if hold.Operations().Counts().Capacity != 1 || budget.Counts().Active != 0 {
		t.Fatal("test changed saved budget")
	}
	scope, finish, err := hold.Operations().enter(hold.Context())
	if err != nil || scope.Err() != nil {
		t.Fatal(err)
	}
	finish()
	select {
	case <-queued:
		t.Fatal("new operation bypassed exclusive test")
	case <-time.After(20 * time.Millisecond):
	}
	hold.Close()
	select {
	case err := <-queued:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not resume")
	}
	if life.Err() != nil || hold.Context().Err() == nil {
		t.Fatal("wrong lifetime ended")
	}
}

func TestExclusiveSourceTestStuckDrainIsBusyAtFiveSeconds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	budget := NewOperationBudget(context.Background(), 1)
	reader, release, err := budget.enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = budget.AcquireTest(reader); !errors.Is(err, ErrBusy) {
		t.Fatal("own operation deadlocked drain", err)
	}
	start := time.Now()
	if _, err = budget.AcquireTest(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < 5*time.Second || elapsed > 6*time.Second {
		t.Fatal("wrong drain bound", elapsed)
	}
	if reader.Err() != nil || budget.Counts().Active != 1 {
		t.Fatal("stuck reader was force-stopped")
	}
	release()
	_, done, err := budget.enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done()
}

func TestExclusiveSourceTestTwentySecondWorkIsTerminatedAtFifteen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	life, stop := context.WithCancel(context.Background())
	defer stop()
	budget := NewOperationBudget(life, 1)
	hold, err := budget.AcquireTest(life)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	queued := make(chan error, 1)
	go func() {
		_, done, err := budget.enter(life)
		if err == nil {
			done()
		}
		queued <- err
	}()
	start := time.Now()
	select {
	case <-time.After(20 * time.Second):
		t.Fatal("20 second test escaped timeout")
	case <-hold.Context().Done():
	}
	if !errors.Is(hold.Context().Err(), context.DeadlineExceeded) {
		t.Fatal(hold.Context().Err())
	}
	if elapsed := time.Since(start); elapsed < 15*time.Second || elapsed > 16*time.Second {
		t.Fatal("wrong exclusive bound", elapsed)
	}
	select {
	case err := <-queued:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout retained exclusive hold")
	}
	if life.Err() != nil {
		t.Fatal("timeout retired viewing session")
	}
}

func TestExclusiveSourceTestCancellationAndRetirementReleaseHold(t *testing.T) {
	for _, retire := range []bool{false, true} {
		life, stop := context.WithCancel(context.Background())
		ctx, cancel := context.WithCancel(context.Background())
		budget := NewOperationBudget(life, 1)
		hold, err := budget.AcquireTest(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if retire {
			stop()
		} else {
			cancel()
		}
		select {
		case <-hold.Context().Done():
		case <-time.After(time.Second):
			t.Fatal("test outlived captured lifetime")
		}
		hold.Close()
		_, done, err := budget.enter(context.Background())
		if retire {
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			done()
		}
		cancel()
		stop()
	}
}
