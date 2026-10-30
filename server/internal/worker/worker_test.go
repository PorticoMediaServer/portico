package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// These tests move package tunables, so every one of them has to stop its loop
// and wait for it before putting them back: a goroutine still reading a variable
// the test is restoring is a data race, and the detector is right about it.
func stopBefore(t *testing.T, cancel context.CancelFunc, done <-chan struct{}, restore func()) func() {
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the loop did not stop")
		}
		restore()
	}
}

func TestALoopWithNothingToDoDoesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		previous := SafetyTick
		SafetyTick = time.Hour
		ctx, cancel := context.WithCancel(context.Background())
		var passes atomic.Int64
		done := Go(ctx, "test.idle", NewSignal(), func(context.Context) time.Duration {
			passes.Add(1)
			return 0
		})
		defer stopBefore(t, cancel, done, func() { SafetyTick = previous })()
		// One pass at start, because a server that just came up has a backlog by
		// definition. Then nothing at all.
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		if passes.Load() != 1 {
			t.Fatalf("an idle loop ran %d times", passes.Load())
		}
	})
}

func TestASignalWakesTheLoopAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		previousTick, previousFloor := SafetyTick, MinSignalInterval
		SafetyTick, MinSignalInterval = time.Hour, 10*time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		signal := NewSignal()
		woken := make(chan struct{}, 4)
		done := Go(ctx, "test.signal", signal, func(context.Context) time.Duration {
			select {
			case woken <- struct{}{}:
			default:
			}
			return 0
		})
		defer stopBefore(t, cancel, done, func() { SafetyTick, MinSignalInterval = previousTick, previousFloor })()
		<-woken                           // the initial pass
		time.Sleep(20 * time.Millisecond) // past the floor
		started := time.Now()
		signal.Wake()
		select {
		case <-woken:
			if time.Since(started) > time.Second {
				t.Fatalf("the wake took %s", time.Since(started))
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a signalled loop did not wake")
		}
	})
}

// Many enqueues between two passes must not queue many passes: the signal
// coalesces, which is what makes it safe to call from a hot path.
func TestSignalsCoalesce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		signal := NewSignal()
		for index := 0; index < 1000; index++ {
			signal.Wake()
		}
		select {
		case <-signal.channel():
		default:
			t.Fatal("a thousand wakes produced none")
		}
		select {
		case <-signal.channel():
			t.Fatal("a thousand wakes produced more than one")
		default:
		}
	})
}

func TestAStepThatKnowsWhenItIsNextDueIsRunThen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		previous := SafetyTick
		SafetyTick = time.Hour
		ctx, cancel := context.WithCancel(context.Background())
		var passes atomic.Int64
		done := Go(ctx, "test.due", NewSignal(), func(context.Context) time.Duration {
			passes.Add(1)
			return 50 * time.Millisecond
		})
		defer stopBefore(t, cancel, done, func() { SafetyTick = previous })()
		time.Sleep(400 * time.Millisecond)
		synctest.Wait()
		if count := passes.Load(); count != 9 {
			t.Fatalf("a loop due every 50 ms ran %d times in 400 ms", count)
		}
	})
}

func TestTheLoopStopsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		done := Go(ctx, "test.stop", NewSignal(), func(context.Context) time.Duration { return time.Millisecond })
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("the loop outlived its context")
		}
	})
}

// The commit signal is deliberately coarse — a loop's own writes wake everybody,
// including itself — so the floor is what stops that being a feedback loop. It
// turned two thousand idle calls a minute into two hundred thousand the first
// time this was wired up without one.
func TestASignalStormCannotSpinTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		previousTick, previousFloor := SafetyTick, MinSignalInterval
		SafetyTick, MinSignalInterval = time.Hour, 100*time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		signal := NewSignal()
		var passes atomic.Int64
		done := Go(ctx, "test.storm", signal, func(context.Context) time.Duration {
			passes.Add(1)
			// The step signals itself, which is what a loop that writes does.
			signal.Wake()
			return 0
		})
		defer stopBefore(t, cancel, done, func() { SafetyTick, MinSignalInterval = previousTick, previousFloor })()
		time.Sleep(550 * time.Millisecond)
		synctest.Wait()
		if count := passes.Load(); count != 6 {
			t.Fatalf("a self-signalling loop ran %d times in 550 ms with a 100 ms floor", count)
		}
	})
}
