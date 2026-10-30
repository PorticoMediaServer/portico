package readprep

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func coldKey(name string) Key { return Key{"cold", "witness", name} }

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("worker signal timed out")
	}
}

func pollCold(t *testing.T, s *Store, caller Caller, h Handle) {
	t.Helper()
	r, err := s.Poll(context.Background(), caller, h)
	if err != nil {
		t.Fatal(err)
	}
	r.Release()
}

// The channel handshake spans many distinct Step calls. A second factory must
// not run in the gaps, even with an idle second worker. Ordinary work still can.
func TestColdOwnerSpansQuantaCoalescesAndAllowsOrdinary(t *testing.T) {
	s := storeFor(t, Config{})
	entered, advance := make(chan struct{}, 1), make(chan struct{})
	var steps, factories atomic.Int32
	p := Plan{ColdSearch: true, InitialBytes: 64, New: func(context.Context, *Budget) (Computation, error) {
		factories.Add(1)
		return &testWork{bytes: 64, step: func(ctx context.Context, _ *Budget) (StepResult, error) {
			steps.Add(1)
			entered <- struct{}{}
			select {
			case <-advance:
				return StepResult{Done: steps.Load() == 24}, nil
			case <-ctx.Done():
				return StepResult{}, ctx.Err()
			}
		}}, nil
	}}
	h := submit(t, s, "a", coldKey("first"), p)
	waitSignal(t, entered)
	otherStarted := make(chan struct{})
	q := Plan{ColdSearch: true, New: func(context.Context, *Budget) (Computation, error) {
		close(otherStarted)
		return &testWork{step: func(context.Context, *Budget) (StepResult, error) { return StepResult{Done: true}, nil }}, nil
	}}
	second := submit(t, s, "b", coldKey("second"), q)
	duplicate := submit(t, s, "c", coldKey("first"), p)
	ordinary := submit(t, s, "ordinary", coldKey("ordinary"), simplePlan("done"))
	ready(t, s, "ordinary", ordinary).Release()
	for i := 1; i < 24; i++ {
		advance <- struct{}{}
		waitSignal(t, entered)
		select {
		case <-otherStarted:
			t.Fatal("second cold factory entered while first build still active")
		default:
		}
	}
	advance <- struct{}{}
	ready(t, s, "a", h).Release()
	ready(t, s, "c", duplicate).Release()
	ready(t, s, "b", second).Release()
	if factories.Load() != 1 || steps.Load() != 24 {
		t.Fatalf("factories=%d steps=%d", factories.Load(), steps.Load())
	}
}

func TestColdDistinctQueueIsBounded(t *testing.T) {
	s := storeFor(t, Config{})
	entered := make(chan struct{})
	p := Plan{ColdSearch: true, New: func(ctx context.Context, _ *Budget) (Computation, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	h := submit(t, s, "a", coldKey("owner"), p)
	waitSignal(t, entered)
	for i := 0; i < 16; i++ {
		q := simplePlan("unused")
		q.ColdSearch = true
		submit(t, s, "a", coldKey(fmt.Sprint(i)), q)
	}
	if _, err := s.Submit(context.Background(), "a", coldKey("overflow"), p); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	// Equivalent demand bypasses the distinct-queue bound without starting work.
	if _, err := s.Open(context.Background(), "b", coldKey("owner")); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats(); got.Queued != 16 || got.Running != 1 {
		t.Fatalf("%+v", got)
	}
	if err := s.Cancel(context.Background(), "a", h); err != nil {
		t.Fatal(err)
	}
}

func TestColdPollingDeadlineIgnoresNewHandlesAndKeepsAnyLivePoller(t *testing.T) {
	clock := &testClock{at: time.Unix(1234, 0)}
	s := storeFor(t, Config{Now: clock.now})
	entered, cancelled, exit := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(exit)
	p := Plan{ColdSearch: true, InitialBytes: 100, New: func(context.Context, *Budget) (Computation, error) {
		return &testWork{bytes: 100, step: func(ctx context.Context, _ *Budget) (StepResult, error) {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			<-exit // model slow native cleanup; memory and ownership stay charged
			return StepResult{}, ctx.Err()
		}}, nil
	}}
	h := submit(t, s, "a", coldKey("owner"), p)
	waitSignal(t, entered)
	clock.add(20 * time.Second)
	live, err := s.Open(context.Background(), "b", coldKey("owner"))
	if err != nil {
		t.Fatal(err)
	}
	pollCold(t, s, "b", live) // only this renews the build deadline, now t=50
	if err = s.Cancel(context.Background(), "a", h); err != nil {
		t.Fatal(err)
	}
	clock.add(20 * time.Second) // initial caller's deadline is past
	s.wake()
	select {
	case <-cancelled:
		t.Fatal("another caller's live polling was ignored")
	default:
	}
	clock.add(9 * time.Second)
	submit(t, s, "c", coldKey("owner"), p)
	if _, err = s.Open(context.Background(), "d", coldKey("owner")); err != nil {
		t.Fatal(err)
	}
	clock.add(time.Second)
	s.wake()
	waitSignal(t, cancelled)
	if got := s.Stats(); got.Entries != 0 || got.Handles != 0 || got.Bytes == 0 || got.Running != 1 {
		t.Fatalf("cancelled worker did not retain live memory: %+v", got)
	}
	if _, err = s.Poll(context.Background(), "b", live); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	// A replacement cannot acquire the old build's lane before native cleanup.
	replacementStarted := make(chan struct{})
	q := simplePlan("next")
	q.ColdSearch = true
	baseNew := q.New
	q.New = func(ctx context.Context, b *Budget) (Computation, error) {
		close(replacementStarted)
		return baseNew(ctx, b)
	}
	next := submit(t, s, "next", coldKey("owner"), q)
	select {
	case <-replacementStarted:
		t.Fatal("replacement entered before retired worker exited")
	default:
	}
	// Release the cleanup handshake without double-closing the deferred channel.
	exit <- struct{}{}
	ready(t, s, "next", next).Release()
}

func TestColdInitialDeadlineExpiresOwnerAndAbandonedQueueWithoutTraffic(t *testing.T) {
	clock := &testClock{at: time.Unix(1234, 0)}
	s := storeFor(t, Config{Workers: 1, Now: clock.now})
	entered, cancelled := make(chan struct{}), make(chan struct{})
	p := Plan{ColdSearch: true, New: func(ctx context.Context, _ *Budget) (Computation, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}}
	submit(t, s, "a", coldKey("owner"), p)
	waitSignal(t, entered)
	var queuedNew atomic.Int32
	q := simplePlan("queued")
	q.ColdSearch = true
	q.New = func(context.Context, *Budget) (Computation, error) { queuedNew.Add(1); return nil, ErrFailed }
	submit(t, s, "b", coldKey("queued"), q)
	clock.add(29 * time.Second)
	submit(t, s, "c", coldKey("owner"), p) // fresh handles cannot renew initial demand
	if _, err := s.Open(context.Background(), "d", coldKey("queued")); err != nil {
		t.Fatal(err)
	}
	clock.add(time.Second)
	// No Poll/Stats/wake/API traffic from here until the real store timer cancels.
	waitSignal(t, cancelled)
	eventually(t, func() bool { return s.Stats().Bytes == 0 })
	if queuedNew.Load() != 0 {
		t.Fatal("abandoned queue ran after demand expired")
	}
}

func TestColdPriorityYieldDoesNotOccupyWorkerAndExpiresWhileIdle(t *testing.T) {
	clock := &testClock{at: time.Unix(1234, 0)}
	s := storeFor(t, Config{Workers: 1, Now: clock.now})
	var probes, factories atomic.Int32
	p := Plan{ColdSearch: true, ShouldYield: func() bool { probes.Add(1); return true }, New: func(context.Context, *Budget) (Computation, error) {
		factories.Add(1)
		return nil, ErrFailed
	}}
	h := submit(t, s, "a", coldKey("yielding"), p)
	eventually(t, func() bool { return probes.Load() > 0 })
	ordinary := submit(t, s, "b", coldKey("ordinary"), simplePlan("ok"))
	ready(t, s, "b", ordinary).Release()
	clock.add(coldPollTTL)
	// Inspect memory directly so this assertion cannot itself perform lazy prune.
	eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.handles[h] == nil && s.coldOwner == nil
	})
	if factories.Load() != 0 {
		t.Fatal("yielding priority probe still constructed cold work")
	}
}

func TestColdReleaseOnTerminalCancelInvalidateAndClose(t *testing.T) {
	for _, ending := range []string{"done", "error", "panic", "cancel", "invalidate", "close"} {
		t.Run(ending, func(t *testing.T) {
			s := storeFor(t, Config{})
			entered, finish, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			p := Plan{ColdSearch: true, InitialBytes: 64, New: func(context.Context, *Budget) (Computation, error) {
				return &testWork{bytes: 64, step: func(ctx context.Context, _ *Budget) (StepResult, error) {
					close(entered)
					select {
					case <-finish:
						if ending == "error" {
							return StepResult{}, ErrFailed
						}
						if ending == "panic" {
							panic("cold test")
						}
						return StepResult{Done: true}, nil
					case <-ctx.Done():
						close(cancelled)
						return StepResult{}, ctx.Err()
					}
				}}, nil
			}}
			h := submit(t, s, "a", coldKey("owner"), p)
			waitSignal(t, entered)
			q := simplePlan("next")
			q.ColdSearch = true
			next := submit(t, s, "b", coldKey("next"), q)
			switch ending {
			case "cancel":
				if err := s.Cancel(context.Background(), "a", h); err != nil {
					t.Fatal(err)
				}
			case "invalidate":
				s.Invalidate(coldKey("owner"))
			case "close":
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				waitSignal(t, cancelled)
				if got := s.Stats(); got != (Stats{}) {
					t.Fatalf("%+v", got)
				}
				return
			default:
				close(finish)
			}
			ready(t, s, "b", next).Release()
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if got := s.Stats(); got != (Stats{}) {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestColdConcurrentPollAttachCancelAndClose(t *testing.T) {
	clock := &testClock{at: time.Unix(1234, 0)}
	s := storeFor(t, Config{Now: clock.now})
	p := Plan{ColdSearch: true, ShouldYield: func() bool { return true }, New: simplePlan("unused").New}
	var wg sync.WaitGroup
	var closeOnce sync.Once
	closeNow, closed := make(chan struct{}), make(chan struct{})
	go func() {
		<-closeNow
		_ = s.Close()
		close(closed)
	}()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				h, err := s.Submit(context.Background(), "a", coldKey("shared"), p)
				if err == nil {
					r, _ := s.Poll(context.Background(), "a", h)
					r.Release()
					_ = s.Cancel(context.Background(), "a", h)
				}
				clock.add(time.Second)
				s.wake()
				if j == 20 {
					closeOnce.Do(func() { close(closeNow) })
				}
			}
		}()
	}
	wg.Wait()
	waitSignal(t, closed)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats(); got != (Stats{}) {
		t.Fatalf("%+v", got)
	}
}

func TestColdAbandonedQueueExpiresWhileOwnerIsPolled(t *testing.T) {
	clock := &testClock{at: time.Unix(1234, 0)}
	s := storeFor(t, Config{Now: clock.now})
	entered := make(chan struct{})
	p := Plan{ColdSearch: true, New: func(ctx context.Context, _ *Budget) (Computation, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	owner := submit(t, s, "a", coldKey("owner"), p)
	waitSignal(t, entered)
	q := simplePlan("queued")
	q.ColdSearch = true
	queued := submit(t, s, "b", coldKey("queued"), q)
	clock.add(20 * time.Second)
	pollCold(t, s, "a", owner)
	clock.add(10 * time.Second)
	s.wake()
	if _, err := s.Poll(context.Background(), "b", queued); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	pollCold(t, s, "a", owner)
	if got := s.Stats(); got.Queued != 0 || got.Entries != 1 || got.Running != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestColdPriorityRetryResumesAndCompletedPlanKeepsNormalTTL(t *testing.T) {
	s := storeFor(t, Config{})
	var pressure atomic.Bool
	pressure.Store(true)
	var probes, factories atomic.Int32
	q := simplePlan("ready")
	q.ColdSearch = true
	q.ShouldYield = func() bool { probes.Add(1); return pressure.Load() }
	baseNew := q.New
	q.New = func(ctx context.Context, b *Budget) (Computation, error) {
		factories.Add(1)
		return baseNew(ctx, b)
	}
	h := submit(t, s, "a", coldKey("owner"), q)
	eventually(t, func() bool { return probes.Load() > 0 })
	if factories.Load() != 0 {
		t.Fatal("factory ran before higher priority work cleared")
	}
	pressure.Store(false)
	ready(t, s, "a", h).Release() // timer, rather than another cold submission, resumes it
	if factories.Load() != 1 {
		t.Fatal("factory was not shared across retries")
	}

	clock := &testClock{at: time.Unix(1234, 0)}
	cached := storeFor(t, Config{Now: clock.now})
	q.ShouldYield = nil
	h = submit(t, cached, "a", coldKey("cached"), q)
	ready(t, cached, "a", h).Release()
	clock.add(31 * time.Second)
	cached.wake()
	ready(t, cached, "a", h).Release() // Plan has cleared; completed TTL still applies
	clock.add(29 * time.Second)
	if _, err := cached.Open(context.Background(), "b", coldKey("cached")); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
}

func TestOrdinaryPlanIgnoresColdPriorityProbe(t *testing.T) {
	s := storeFor(t, Config{})
	p := simplePlan("ok")
	p.ShouldYield = func() bool { panic("ordinary plan consulted cold policy") }
	h := submit(t, s, "a", testKey, p)
	ready(t, s, "a", h).Release()
}

func TestColdPollAfterRetryDeadlineCannotPostponeProgress(t *testing.T) {
	clock := &testClock{at: time.Unix(1234, 0)}
	s := storeFor(t, Config{Now: clock.now})
	var pressure atomic.Bool
	pressure.Store(true)
	p := simplePlan("ready")
	p.ColdSearch = true
	p.ShouldYield = pressure.Load
	h := submit(t, s, "caller", coldKey("overdue"), p)
	eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.coldOwner != nil && !s.coldOwner.running && !s.coldOwner.retryAt.IsZero()
	})
	s.mu.Lock()
	if s.wakeTimer != nil {
		s.wakeTimer.Stop()
	}
	s.mu.Unlock()
	clock.add(20 * time.Millisecond)
	pressure.Store(false)
	// Every poll is later than retryAt. The old code reset the timer to1s
	// on each of these1ms polls, so no worker could resume. Keep the original
	// ready helper's deadline and poll cadence; do not notify the cond manually.
	ready(t, s, "caller", h).Release()
}
