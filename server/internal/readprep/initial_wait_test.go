package readprep

// Tests for the initial-submission wait in initial_wait.go. Everything here is
// driven by explicit channels and the store's own accounting instead of sleeps
// wherever possible: the two unavoidable timing bounds are that a 50ms grace
// must actually elapse (checked generously at half the clamp) and that a second
// waiter returns immediately instead of taking its own grace period.
//
// Blocked producers select on the entry context, so Cancel/Invalidate/Close
// always release the fixed worker and no goroutine outlives its test.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// iwGate blocks a producer inside Step until the test either releases it or the
// store cancels the entry's work context.
type iwGate struct {
	started, release chan struct{}
	startOnce        sync.Once
	openOnce         sync.Once
}

func newIWGate() *iwGate {
	return &iwGate{started: make(chan struct{}), release: make(chan struct{})}
}

func (g *iwGate) enter() { g.startOnce.Do(func() { close(g.started) }) }
func (g *iwGate) open()  { g.openOnce.Do(func() { close(g.release) }) }

func iwBlockedPlan(payload string, gate *iwGate) Plan {
	return Plan{InitialBytes: 64, New: func(context.Context, *Budget) (Computation, error) {
		return &testWork{bytes: 64, step: func(ctx context.Context, _ *Budget) (StepResult, error) {
			gate.enter()
			select {
			case <-gate.release:
				return StepResult{Done: true, Payload: []byte(payload)}, nil
			case <-ctx.Done():
				return StepResult{}, ctx.Err()
			}
		}}, nil
	}}
}

type iwResult struct {
	r   Result
	err error
}

// iwAwait collects a PollWhenReady goroutine result. The waiter always returns,
// so a hang here is a real defect rather than a slow machine.
func iwAwait(t *testing.T, ch <-chan iwResult) iwResult {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(3 * time.Second):
		t.Fatal("PollWhenReady did not return")
		return iwResult{}
	}
}

func iwEntryBytes(key Key) int64 {
	return entryOverhead + int64(len(key.Namespace)+len(key.Basis)+len(key.Canonical))
}

func iwHandleBytes(caller Caller) int64 {
	return handleOverhead + int64(len(caller))
}

// iwBase is the exact charge for one unfinished entry plus one private handle.
func iwBase(key Key, caller Caller, initial int64) int64 {
	return iwEntryBytes(key) + iwHandleBytes(caller) + initial
}

func iwDeliveryBytes(key Key, payload int) int64 {
	return deliveryOverhead + int64(len(key.Namespace)+len(key.Basis)+len(key.Canonical)+payload)
}

// A worker that finishes while the caller is parked in the grace must hand the
// request a normal, singly charged Ready delivery, and Release must return it.
func TestPollWhenReadyDeliversCompletionInsideGrace(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "initial", Basis: "basis", Canonical: "completes"}
	caller := Caller("caller")
	h := submit(t, s, caller, key, iwBlockedPlan("ready", gate))
	waitSignal(t, gate.started)
	base := iwBase(key, caller, 64)
	if got := s.Stats().Bytes; got != base {
		t.Fatalf("pre-wait bytes = %d, want %d", got, base)
	}

	results := make(chan iwResult, 1)
	go func() {
		r, err := s.PollWhenReady(context.Background(), caller, h, InitialReadyGrace)
		results <- iwResult{r: r, err: err}
	}()
	// The transient control reservation proves the waiter is actually parked on
	// the entry's completion channel, not simply racing a finished entry.
	eventually(t, func() bool { return s.Stats().Bytes == base+initialWaitBytes })
	gate.open()

	got := iwAwait(t, results)
	if got.err != nil {
		t.Fatalf("completion during grace: %v", got.err)
	}
	if got.r.State != Ready || string(got.r.Payload) != "ready" {
		t.Fatalf("completion during grace returned %+v", got.r)
	}
	stats := s.Stats()
	if stats.Deliveries != 1 || stats.Entries != 1 || stats.Handles != 1 {
		t.Fatalf("completion accounting %+v", stats)
	}
	if want := iwDeliveryBytes(key, len("ready")); stats.DeliveryBytes != want {
		t.Fatalf("delivery bytes = %d, want %d", stats.DeliveryBytes, want)
	}
	got.r.Release()
	if stats := s.Stats(); stats.Deliveries != 0 || stats.DeliveryBytes != 0 {
		t.Fatalf("release left %+v", stats)
	}
}

// Slow work must return the ordinary Preparing result after the fixed clamp,
// even for a caller-supplied hour, and must not move the request deadline. One
// waiter per handle keeps the control charge single.
func TestPollWhenReadyClampsSlowWorkWithoutRaisingTimeout(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "initial", Basis: "basis", Canonical: "slow"}
	caller := Caller("caller")
	h := submit(t, s, caller, key, iwBlockedPlan("slow", gate))
	waitSignal(t, gate.started)
	base := iwBase(key, caller, 64)
	expiry, err := s.LeaseExpiry(context.Background(), caller, h)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	r, err := s.PollWhenReady(context.Background(), caller, h, time.Hour)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("slow work: %v", err)
	}
	if r.State != Preparing || r.Payload != nil || r.delivery == nil {
		t.Fatalf("slow work returned %+v", r)
	}
	if elapsed < InitialReadyGrace/2 {
		t.Fatalf("grace clamp was not applied: returned after %v", elapsed)
	}
	if elapsed > 10*InitialReadyGrace {
		t.Fatalf("an hour grace escaped the %v clamp: %v", InitialReadyGrace, elapsed)
	}
	if r.OriginalExpiry != expiry {
		t.Fatalf("initial wait changed the delivery deadline: %v != %v", r.OriginalExpiry, expiry)
	}
	if again, err := s.LeaseExpiry(context.Background(), caller, h); err != nil || !again.Equal(expiry) {
		t.Fatalf("initial wait changed the lease: %v, %v", again, err)
	}
	if stats := s.Stats(); stats.Bytes != base+iwDeliveryBytes(key, 0) || stats.Deliveries != 1 {
		t.Fatalf("slow work accounting %+v", stats)
	}
	r.Release()
	if stats := s.Stats(); stats.Bytes != base || stats.Deliveries != 0 {
		t.Fatalf("preparing release left %+v", stats)
	}

	// A second concurrent wait on the same private handle must not park again,
	// must not hold a second grace, and must not take a second control charge.
	results := make(chan iwResult, 1)
	go func() {
		rr, ee := s.PollWhenReady(context.Background(), caller, h, InitialReadyGrace)
		results <- iwResult{r: rr, err: ee}
	}()
	eventually(t, func() bool { return s.Stats().Bytes == base+initialWaitBytes })
	secondStart := time.Now()
	second, err := s.PollWhenReady(context.Background(), caller, h, time.Hour)
	if secondElapsed := time.Since(secondStart); secondElapsed >= InitialReadyGrace/2 {
		t.Fatalf("second waiter blocked for %v", secondElapsed)
	}
	if err != nil {
		t.Fatalf("second waiter: %v", err)
	}
	if second.State != Preparing || second.delivery == nil {
		t.Fatalf("second waiter returned %+v", second)
	}
	if stats := s.Stats(); stats.Bytes != base+initialWaitBytes+iwDeliveryBytes(key, 0) {
		t.Fatalf("second waiter charged another control reservation: %+v", stats)
	}
	second.Release()
	if stats := s.Stats(); stats.Bytes != base+initialWaitBytes {
		t.Fatalf("second waiter release lost the first reservation: %+v", stats)
	}

	gate.open()
	got := iwAwait(t, results)
	if got.err != nil {
		t.Fatalf("first waiter after completion: %v", got.err)
	}
	if got.r.delivery == nil {
		t.Fatal("first waiter returned no delivery")
	}
	got.r.Release()
	if stats := s.Stats(); stats.Deliveries != 0 {
		t.Fatalf("waiter release left %+v", stats)
	}
}

// A foreign caller and an expired lease are stale before any control charge is
// taken, and neither disturbs the legitimate caller's live handle.
func TestPollWhenReadyRejectsWrongCallerAndExpiredHandleWithoutCharge(t *testing.T) {
	t.Run("wrong caller", func(t *testing.T) {
		s := storeFor(t, Config{Workers: 1})
		gate := newIWGate()
		t.Cleanup(gate.open)
		key := Key{Namespace: "initial", Basis: "basis", Canonical: "private"}
		h := submit(t, s, "caller", key, iwBlockedPlan("private", gate))
		waitSignal(t, gate.started)
		base := iwBase(key, "caller", 64)

		r, err := s.PollWhenReady(context.Background(), "intruder", h, InitialReadyGrace)
		if !errors.Is(err, ErrStale) {
			t.Fatalf("foreign caller = %v, want stale", err)
		}
		if r.delivery != nil || r.Payload != nil {
			t.Fatalf("foreign caller received %+v", r)
		}
		if stats := s.Stats(); stats.Bytes != base || stats.Deliveries != 0 || stats.Handles != 1 {
			t.Fatalf("foreign caller leaked or charged: %+v", stats)
		}
		own, err := s.Poll(context.Background(), "caller", h)
		if err != nil || own.State != Preparing {
			t.Fatalf("legitimate handle after foreign wait: %+v, %v", own, err)
		}
		own.Release()
		if stats := s.Stats(); stats.Bytes != base || stats.Deliveries != 0 {
			t.Fatalf("legitimate poll left %+v", stats)
		}
	})

	t.Run("expired lease", func(t *testing.T) {
		clock := &testClock{at: time.Unix(1000, 0)}
		s := storeFor(t, Config{Workers: 1, Now: clock.now, OriginalTTL: time.Second})
		gate := newIWGate()
		t.Cleanup(gate.open)
		key := Key{Namespace: "initial", Basis: "basis", Canonical: "expired"}
		h := submit(t, s, "caller", key, iwBlockedPlan("expired", gate))
		waitSignal(t, gate.started)
		clock.add(2 * time.Second)

		r, err := s.PollWhenReady(context.Background(), "caller", h, InitialReadyGrace)
		if !errors.Is(err, ErrStale) {
			t.Fatalf("expired lease = %v, want stale", err)
		}
		if r.delivery != nil || r.Payload != nil {
			t.Fatalf("expired lease received %+v", r)
		}
		stats := s.Stats()
		if stats.Handles != 0 || stats.Deliveries != 0 {
			t.Fatalf("expired lease left %+v", stats)
		}
		// The abandoned producer may already be unwinding; either its reserved
		// work or nothing at all is charged, never a transient control byte.
		if got := stats.Bytes; got != 0 && got != iwEntryBytes(key)+64 {
			t.Fatalf("expired wait left %d bytes, want 0 or %d", got, iwEntryBytes(key)+64)
		}
		eventually(t, func() bool {
			st := s.Stats()
			return st.Bytes == 0 && st.Running == 0
		})
	})
}

// Cancelling the only handle this caller owns wakes its parked waiter as stale
// while another caller still owns the shared computation and can use it.
func TestCancelOfOwnHandleWakesWaiterWhileSharedEntryStaysOwned(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "initial", Basis: "basis", Canonical: "shared"}
	plan := iwBlockedPlan("shared", gate)

	a := submit(t, s, "a", key, plan)
	waitSignal(t, gate.started)
	b := submit(t, s, "b", key, plan)
	entryBytes := iwEntryBytes(key)
	base := entryBytes + 64 + iwHandleBytes("a") + iwHandleBytes("b")
	if got := s.Stats().Bytes; got != base {
		t.Fatalf("shared pre-wait bytes = %d, want %d", got, base)
	}

	results := make(chan iwResult, 1)
	go func() {
		r, err := s.PollWhenReady(context.Background(), "a", a, InitialReadyGrace)
		results <- iwResult{r: r, err: err}
	}()
	eventually(t, func() bool { return s.Stats().Bytes == base+initialWaitBytes })

	if err := s.Cancel(context.Background(), "a", a); err != nil {
		t.Fatal(err)
	}
	got := iwAwait(t, results)
	if !errors.Is(got.err, ErrStale) {
		t.Fatalf("cancelled handle wait = %v, want stale", got.err)
	}
	if got.r.delivery != nil {
		t.Fatalf("stale wait returned a delivery: %+v", got.r)
	}
	stats := s.Stats()
	if stats.Handles != 1 || stats.Entries != 1 || stats.Running != 1 {
		t.Fatalf("shared entry misretired: %+v", stats)
	}
	if want := entryBytes + 64 + iwHandleBytes("b"); stats.Bytes != want {
		t.Fatalf("bytes after own cancel = %d, want %d", stats.Bytes, want)
	}
	if _, err := s.Poll(context.Background(), "a", a); !errors.Is(err, ErrStale) {
		t.Fatalf("cancelled handle survived: %v", err)
	}

	gate.open()
	rb := ready(t, s, "b", b)
	if rb.Key != key || string(rb.Payload) != "shared" {
		t.Fatalf("other caller lost the shared computation: %+v", rb)
	}
	rb.Release()
	if stats := s.Stats(); stats.Entries != 1 || stats.Handles != 1 {
		t.Fatalf("other caller's handle did not survive: %+v", stats)
	}
}

// CancelNamespace releases only its own adapter namespace: the wrong namespace
// neither wakes nor retires the owned lease, and the right one then does.
func TestCancelNamespaceInitialWaiterWakesOnlyForOwnNamespace(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "actions", Basis: "basis", Canonical: "cancel"}
	caller := Caller("caller")
	h := submit(t, s, caller, key, iwBlockedPlan("action", gate))
	waitSignal(t, gate.started)
	base := iwBase(key, caller, 64)

	results := make(chan iwResult, 1)
	go func() {
		r, err := s.PollWhenReady(context.Background(), caller, h, InitialReadyGrace)
		results <- iwResult{r: r, err: err}
	}()
	eventually(t, func() bool { return s.Stats().Bytes == base+initialWaitBytes })

	if err := s.CancelNamespace(context.Background(), caller, h, "browse"); err != nil {
		t.Fatalf("wrong namespace cancel: %v", err)
	}
	stats := s.Stats()
	if stats.Handles != 1 || stats.Entries != 1 || stats.Running != 1 {
		t.Fatalf("wrong namespace retired the lease: %+v", stats)
	}
	if stats.Bytes != base+initialWaitBytes {
		t.Fatalf("wrong namespace disturbed the waiter: %+v", stats)
	}

	if err := s.CancelNamespace(context.Background(), caller, h, "actions"); err != nil {
		t.Fatalf("own namespace cancel: %v", err)
	}
	got := iwAwait(t, results)
	if !errors.Is(got.err, ErrStale) {
		t.Fatalf("own namespace wait = %v, want stale", got.err)
	}
	stats = s.Stats()
	if stats.Handles != 0 || stats.Entries != 0 || stats.Deliveries != 0 {
		t.Fatalf("own namespace cancel left %+v", stats)
	}
	eventually(t, func() bool {
		st := s.Stats()
		return st.Bytes == 0 && st.Running == 0
	})
}

// Invalidate wakes a parked waiter as stale and keeps the unwinding work charged
// until the cooperative producer actually exits.
func TestInvalidateWakesInitialWaiterStale(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "initial", Basis: "basis", Canonical: "invalidated"}
	caller := Caller("caller")
	h := submit(t, s, caller, key, iwBlockedPlan("invalid", gate))
	waitSignal(t, gate.started)
	base := iwBase(key, caller, 64)

	results := make(chan iwResult, 1)
	go func() {
		r, err := s.PollWhenReady(context.Background(), caller, h, InitialReadyGrace)
		results <- iwResult{r: r, err: err}
	}()
	eventually(t, func() bool { return s.Stats().Bytes == base+initialWaitBytes })

	s.Invalidate(key)
	got := iwAwait(t, results)
	if !errors.Is(got.err, ErrStale) {
		t.Fatalf("invalidated wait = %v, want stale", got.err)
	}
	if got.r.delivery != nil {
		t.Fatalf("invalidated wait returned a delivery: %+v", got.r)
	}
	stats := s.Stats()
	if stats.Handles != 0 || stats.Entries != 0 || stats.Deliveries != 0 {
		t.Fatalf("invalidate left %+v", stats)
	}
	eventually(t, func() bool {
		st := s.Stats()
		return st.Bytes == 0 && st.Running == 0
	})
}

// Close wakes a parked waiter with ErrClosed and drains all charges once the
// cancelled producer returns.
func TestCloseWakesInitialWaiterClosed(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "initial", Basis: "basis", Canonical: "closed"}
	caller := Caller("caller")
	h := submit(t, s, caller, key, iwBlockedPlan("closed", gate))
	waitSignal(t, gate.started)
	base := iwBase(key, caller, 64)

	results := make(chan iwResult, 1)
	go func() {
		r, err := s.PollWhenReady(context.Background(), caller, h, InitialReadyGrace)
		results <- iwResult{r: r, err: err}
	}()
	eventually(t, func() bool { return s.Stats().Bytes == base+initialWaitBytes })

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	got := iwAwait(t, results)
	if !errors.Is(got.err, ErrClosed) {
		t.Fatalf("closed wait = %v, want closed", got.err)
	}
	if got.r.delivery != nil || got.r.Payload != nil {
		t.Fatalf("closed wait returned %+v", got.r)
	}
	eventually(t, func() bool { return s.Stats() == Stats{} })
}

// Request cancellation wins over the grace: the waiter returns the context
// error, leaves the lease untouched, and returns its control reservation.
func TestContextCancelWakesInitialWaiter(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "initial", Basis: "basis", Canonical: "context"}
	caller := Caller("caller")
	h := submit(t, s, caller, key, iwBlockedPlan("context", gate))
	waitSignal(t, gate.started)
	base := iwBase(key, caller, 64)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan iwResult, 1)
	go func() {
		r, err := s.PollWhenReady(ctx, caller, h, InitialReadyGrace)
		results <- iwResult{r: r, err: err}
	}()
	eventually(t, func() bool { return s.Stats().Bytes == base+initialWaitBytes })

	cancel()
	got := iwAwait(t, results)
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancelled request = %v, want context.Canceled", got.err)
	}
	if got.r.delivery != nil {
		t.Fatalf("cancelled request returned a delivery: %+v", got.r)
	}
	stats := s.Stats()
	if stats.Bytes != base || stats.Deliveries != 0 || stats.Handles != 1 || stats.Entries != 1 {
		t.Fatalf("cancelled request disturbed the lease: %+v", stats)
	}

	gate.open()
	ready(t, s, caller, h).Release()
}

// The initial wait must never submit, produce, or run the plan factory on the
// request goroutine; it only observes the existing private handle.
func TestPollWhenReadyDoesNotCreateHandlesOrRunWorkOnRequestGoroutine(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "initial", Basis: "basis", Canonical: "factory"}
	caller := Caller("caller")
	var factories, steps atomic.Int32
	plan := Plan{InitialBytes: 64, New: func(context.Context, *Budget) (Computation, error) {
		factories.Add(1)
		return &testWork{bytes: 64, step: func(ctx context.Context, _ *Budget) (StepResult, error) {
			steps.Add(1)
			gate.enter()
			select {
			case <-gate.release:
				return StepResult{Done: true, Payload: []byte("ready")}, nil
			case <-ctx.Done():
				return StepResult{}, ctx.Err()
			}
		}}, nil
	}}
	h := submit(t, s, caller, key, plan)
	waitSignal(t, gate.started)
	if factories.Load() != 1 || steps.Load() != 1 {
		t.Fatalf("factories=%d steps=%d before wait", factories.Load(), steps.Load())
	}

	r, err := s.PollWhenReady(context.Background(), caller, h, InitialReadyGrace)
	if err != nil {
		t.Fatalf("initial wait for factory: %v", err)
	}
	if r.State != Preparing || r.delivery == nil {
		t.Fatalf("initial wait for factory returned %+v", r)
	}
	r.Release()
	stats := s.Stats()
	if stats.Handles != 1 || stats.Entries != 1 || stats.Deliveries != 0 {
		t.Fatalf("initial wait created or dropped demand: %+v", stats)
	}
	if factories.Load() != 1 || steps.Load() != 1 {
		t.Fatalf("initial wait ran more work: factories=%d steps=%d", factories.Load(), steps.Load())
	}

	gate.open()
	ready(t, s, caller, h).Release()
	if factories.Load() != 1 {
		t.Fatalf("completion rebuilt the plan: factories=%d", factories.Load())
	}
}

// The fixed control reservation exists only while the caller is parked, and a
// cancelled run keeps the work's own reservation charged until the blocked
// producer unwinds. Neither state may look like an empty store.
func TestInitialWaitControlChargeAndUnwindingWorkStayAccounted(t *testing.T) {
	s := storeFor(t, Config{Workers: 1, MaxBytes: 4096})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "initial", Basis: "basis", Canonical: "memory"}
	caller := Caller("caller")
	entryBytes, handleBytes := iwEntryBytes(key), iwHandleBytes(caller)
	base := entryBytes + handleBytes + 64
	if base+initialWaitBytes > s.MaxBytes() {
		t.Fatalf("test charge %d exceeds the %d cap", base+initialWaitBytes, s.MaxBytes())
	}
	plan := Plan{InitialBytes: 64, New: func(context.Context, *Budget) (Computation, error) {
		return &testWork{bytes: 64, step: func(context.Context, *Budget) (StepResult, error) {
			gate.enter()
			<-gate.release // Deliberately pin the already-cancelled worker's unwind.
			return StepResult{}, context.Canceled
		}}, nil
	}}
	h := submit(t, s, caller, key, plan)
	waitSignal(t, gate.started)
	if stats := s.Stats(); stats.Bytes != base || stats.Running != 1 || stats.Queued != 0 {
		t.Fatalf("blocked worker accounting %+v, want bytes %d", stats, base)
	}

	results := make(chan iwResult, 1)
	go func() {
		r, err := s.PollWhenReady(context.Background(), caller, h, InitialReadyGrace)
		results <- iwResult{r: r, err: err}
	}()
	eventually(t, func() bool { return s.Stats().Bytes == base+initialWaitBytes })

	got := iwAwait(t, results)
	if got.err != nil || got.r.State != Preparing {
		t.Fatalf("parked wait on blocked work = %+v, %v", got.r, got.err)
	}
	if got.r.delivery == nil {
		t.Fatal("ordinary preparing delivery was not charged")
	}
	got.r.Release()
	if stats := s.Stats(); stats.Bytes != base || stats.Deliveries != 0 {
		t.Fatalf("wait and release left %+v", stats)
	}

	if err := s.Cancel(context.Background(), caller, h); err != nil {
		t.Fatal(err)
	}
	stats := s.Stats()
	if stats.Handles != 0 || stats.Entries != 0 || stats.Running != 1 {
		t.Fatalf("cancel of blocked work %+v", stats)
	}
	if want := entryBytes + 64; stats.Bytes != want {
		t.Fatalf("unwinding work bytes = %d, want %d", stats.Bytes, want)
	}

	gate.open()
	eventually(t, func() bool {
		st := s.Stats()
		return st.Bytes == 0 && st.Running == 0
	})
}

// CancelNamespace's caller and namespace checks and the release they guard must
// stay one atomic step: no interleaving of a foreign caller or a foreign adapter
// namespace may drop the owner's live demand.
func TestCancelNamespaceNeverReleasesForeignDemandAtomically(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "actions", Basis: "basis", Canonical: "atomic"}
	plan := iwBlockedPlan("atomic", gate)
	owner := submit(t, s, "owner", key, plan)
	waitSignal(t, gate.started)
	other := submit(t, s, "other", key, plan)

	if err := s.CancelNamespace(context.Background(), "owner", owner, ""); !errors.Is(err, ErrStale) {
		t.Fatalf("empty namespace = %v, want stale", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Right namespace, foreign caller.
			_ = s.CancelNamespace(context.Background(), "other", owner, "actions")
		}()
		go func() {
			defer wg.Done()
			// Right caller, foreign adapter namespace.
			_ = s.CancelNamespace(context.Background(), "owner", owner, "browse")
		}()
	}
	wg.Wait()
	stats := s.Stats()
	if stats.Handles != 2 || stats.Entries != 1 || stats.Running != 1 {
		t.Fatalf("foreign namespace or caller released demand: %+v", stats)
	}

	gate.open()
	ready(t, s, "owner", owner).Release()
	ready(t, s, "other", other).Release()
}

// WaitPreparedForTest is the HTTP fixture's wait on the real worker behind one
// private handle. It runs after the handler has returned: the ordinary Poll that
// follows still takes the singly charged Ready delivery, and the wait's own
// transient control reservation is gone by then. No deadline is added, so a
// caller context with none still completes when the worker finishes.
func TestWaitPreparedForTestWaitsForCompletion(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "initial", Basis: "basis", Canonical: "http-ready"}
	caller := Caller("caller")
	h := submit(t, s, caller, key, iwBlockedPlan("ready", gate))
	waitSignal(t, gate.started)
	base := iwBase(key, caller, 64)

	waited := make(chan iwResult, 1)
	go func() { waited <- iwResult{err: s.WaitPreparedForTest(context.Background(), h)} }()
	// The same control charge as PollWhenReady proves the fixture is parked on
	// the entry's completion channel rather than racing a finished entry.
	eventually(t, func() bool { return s.Stats().Bytes == base+initialWaitBytes })

	gate.open()
	if got := iwAwait(t, waited); got.err != nil {
		t.Fatalf("preparation wait: %v", got.err)
	}
	// Waiting is not reading: it takes no delivery and returns no payload.
	stats := s.Stats()
	if stats.Deliveries != 0 || stats.DeliveryBytes != 0 {
		t.Fatalf("preparation wait took a delivery: %+v", stats)
	}
	payloadCap := stats.Bytes - iwEntryBytes(key) - iwHandleBytes(caller)
	if payloadCap < int64(len("ready")) || payloadCap > 64 {
		t.Fatalf("completed entry reservation = %d, want the control charge released", payloadCap)
	}
	got, err := s.Poll(context.Background(), caller, h)
	if err != nil {
		t.Fatalf("poll after wait: %v", err)
	}
	if got.State != Ready || string(got.Payload) != "ready" {
		t.Fatalf("poll after wait returned %+v", got)
	}
	if stats := s.Stats(); stats.Deliveries != 1 || stats.DeliveryBytes != iwDeliveryBytes(key, len("ready")) ||
		stats.Bytes != base-int64(64)+payloadCap+iwDeliveryBytes(key, len("ready")) {
		t.Fatalf("delivery accounting %+v", stats)
	}
	got.Release()
	if after := s.Stats(); after.Bytes != iwEntryBytes(key)+iwHandleBytes(caller)+payloadCap || after.Deliveries != 0 || after.DeliveryBytes != 0 {
		t.Fatalf("release left %+v", after)
	}
}

// Cancelling the fixture (its own deadline or teardown) releases the transient
// control reservation and leaves the worker, entry and lease exactly as the
// ordinary poll path would find them.
func TestWaitPreparedForTestCancellationReleasesControl(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "initial", Basis: "basis", Canonical: "http-cancel"}
	caller := Caller("caller")
	h := submit(t, s, caller, key, iwBlockedPlan("cancel", gate))
	waitSignal(t, gate.started)
	base := iwBase(key, caller, 64)

	ctx, cancel := context.WithCancel(context.Background())
	waited := make(chan iwResult, 1)
	go func() { waited <- iwResult{err: s.WaitPreparedForTest(ctx, h)} }()
	eventually(t, func() bool { return s.Stats().Bytes == base+initialWaitBytes })

	cancel()
	got := iwAwait(t, waited)
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancelled wait = %v, want context.Canceled", got.err)
	}
	if stats := s.Stats(); stats.Bytes != base || stats.Handles != 1 || stats.Entries != 1 || stats.Deliveries != 0 {
		t.Fatalf("cancelled wait disturbed the lease or leaked control: %+v", stats)
	}

	gate.open()
	ready(t, s, caller, h).Release()
	eventually(t, func() bool { return s.Stats().Running == 0 })
}

// Retiring the handle under a parked fixture wakes it as stale and the control
// reservation may never survive its waiter. The unwinding worker keeps only its
// own entry charge until it actually returns.
func TestWaitPreparedForTestRetirementWakesStale(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	gate := newIWGate()
	t.Cleanup(gate.open)
	key := Key{Namespace: "initial", Basis: "basis", Canonical: "http-retire"}
	caller := Caller("caller")
	h := submit(t, s, caller, key, iwBlockedPlan("retire", gate))
	waitSignal(t, gate.started)
	base := iwBase(key, caller, 64)

	waited := make(chan iwResult, 1)
	go func() { waited <- iwResult{err: s.WaitPreparedForTest(context.Background(), h)} }()
	eventually(t, func() bool { return s.Stats().Bytes == base+initialWaitBytes })

	if err := s.Cancel(context.Background(), caller, h); err != nil {
		t.Fatal(err)
	}
	got := iwAwait(t, waited)
	if !errors.Is(got.err, ErrStale) {
		t.Fatalf("retired wait = %v, want stale", got.err)
	}
	// The control reservation is gone; the cancelled worker may still be
	// unwinding its own entry charge, but 0 is also accepted.
	if stats := s.Stats(); stats.Handles != 0 || stats.Entries != 0 ||
		(stats.Bytes != 0 && stats.Bytes != iwEntryBytes(key)+64) {
		t.Fatalf("retired wait left %+v, want no control and no lease", stats)
	}

	gate.open()
	eventually(t, func() bool {
		st := s.Stats()
		return st.Bytes == 0 && st.Running == 0
	})
}

// A finished handle costs no wait and no control charge, an unknown or foreign
// handle reveals nothing, and parking a fixture never admits a foreign caller.
func TestWaitPreparedForTestRefusesUnknownAndForeignAccess(t *testing.T) {
	t.Run("unknown", func(t *testing.T) {
		s := storeFor(t, Config{Workers: 1})
		if err := s.WaitPreparedForTest(context.Background(), ""); !errors.Is(err, ErrStale) {
			t.Fatalf("empty handle = %v, want stale", err)
		}
		if err := s.WaitPreparedForTest(context.Background(), "missing"); !errors.Is(err, ErrStale) {
			t.Fatalf("unknown handle = %v, want stale", err)
		}
		if stats := s.Stats(); stats.Bytes != 0 || stats.Handles != 0 || stats.Entries != 0 {
			t.Fatalf("unknown handle was charged: %+v", stats)
		}
	})

	t.Run("finished", func(t *testing.T) {
		s := storeFor(t, Config{Workers: 1})
		caller := Caller("caller")
		key := Key{Namespace: "initial", Basis: "basis", Canonical: "http-finished"}
		h := submit(t, s, caller, key, simplePlan("done"))
		ready(t, s, caller, h)
		before := s.Stats()
		if err := s.WaitPreparedForTest(context.Background(), h); err != nil {
			t.Fatalf("finished handle: %v", err)
		}
		if after := s.Stats(); after.Bytes != before.Bytes || after.Deliveries != before.Deliveries {
			t.Fatalf("finished handle charged a wait: %+v -> %+v", before, after)
		}
	})

	t.Run("foreign caller", func(t *testing.T) {
		s := storeFor(t, Config{Workers: 1})
		gate := newIWGate()
		t.Cleanup(gate.open)
		key := Key{Namespace: "initial", Basis: "basis", Canonical: "http-foreign"}
		h := submit(t, s, "caller", key, iwBlockedPlan("foreign", gate))
		waitSignal(t, gate.started)
		base := iwBase(key, "caller", 64)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		waited := make(chan iwResult, 1)
		go func() { waited <- iwResult{err: s.WaitPreparedForTest(ctx, h)} }()
		eventually(t, func() bool { return s.Stats().Bytes == base+initialWaitBytes })

		if _, err := s.Poll(context.Background(), "intruder", h); !errors.Is(err, ErrStale) {
			t.Fatalf("foreign poll while a fixture is parked = %v, want stale", err)
		}
		if stats := s.Stats(); stats.Bytes != base+initialWaitBytes || stats.Deliveries != 0 {
			t.Fatalf("foreign poll reached the preparation or its delivery: %+v", stats)
		}

		gate.open()
		if got := iwAwait(t, waited); got.err != nil {
			t.Fatalf("preparation wait: %v", got.err)
		}
		ready(t, s, "caller", h).Release()
		eventually(t, func() bool { return s.Stats().Running == 0 })
	})
}
