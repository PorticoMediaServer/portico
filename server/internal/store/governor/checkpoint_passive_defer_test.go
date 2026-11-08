package governor

import (
	"errors"
	"testing"
	"time"
)

func deferFixture(t *testing.T, max time.Duration) *keeperFixture {
	t.Helper()
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	f.sample(t)
	f.wal.policy.PassiveDeferMax = max
	return f
}

func deferredStep(t *testing.T, f *keeperFixture) (CheckpointResult, error) {
	t.Helper()
	f.clock.ms.Add(60) // beyond the 50 ms minimum start spacing every time
	return f.cp.Step(t.Context(), concurrentCPRequest(f))
}

// With the experiment on, a normal PASSIVE start is deferred while a Foreground
// writer is in flight, for at most PassiveDeferMax per episode, then proceeds.
func TestPassiveStartDefersForAFlightForegroundWriterAndIsBounded(t *testing.T) {
	f := deferFixture(t, 100*time.Millisecond)
	w := grant(t, f.g, concurrentWriter(t, f))
	out, err := deferredStep(t, f)
	if !errors.Is(err, ErrCheckpointForeground) || !out.Started.IsZero() || !out.NativeStarted.IsZero() {
		t.Fatal("PASSIVE must defer, with no grant and no native call", out, err)
	}
	out, err = deferredStep(t, f) // 60 ms into the episode: still inside the 100 ms bound
	if !errors.Is(err, ErrCheckpointForeground) || !out.NativeStarted.IsZero() {
		t.Fatal("still inside the bound", out, err)
	}
	out, err = deferredStep(t, f) // 120 ms: the bound is spent, so the PASSIVE starts anyway
	if err != nil || out.Refusal != nil || !out.PhysicalSafe || out.NativeStarted.IsZero() {
		t.Fatal("after the bound the PASSIVE must start (debt still converges)", out, err)
	}
	retire(t, f.a, w)
	f.sample(t)
	// Under a steady Foreground stream the bound holds whatever the offer cadence:
	// an offer arriving long after the first deferral is never deferred again.
	w = grant(t, f.g, concurrentWriter(t, f))
	if out, err = deferredStep(t, f); !errors.Is(err, ErrCheckpointForeground) {
		t.Fatal("a fresh episode starts with a deferral", out, err)
	}
	retire(t, f.a, w)
	f.clock.ms.Add(5000) // the caller's next offer is far later than the bound
	f.sample(t)
	w = grant(t, f.g, concurrentWriter(t, f)) // the stream is still busy
	if out, err = deferredStep(t, f); err != nil || out.NativeStarted.IsZero() {
		t.Fatal("an offer past the bound must start, never starve behind a steady stream", out, err)
	}
	retire(t, f.a, w)
}

// The deferral never applies when it is off, to scan/background writers, to an
// idle writer lane, or to pressure recovery.
func TestPassiveStartIsNotDeferredWhenOffOrNotForegroundOrRecovery(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		f := deferFixture(t, 0)
		w := grant(t, f.g, concurrentWriter(t, f))
		if out, err := deferredStep(t, f); err != nil || out.NativeStarted.IsZero() {
			t.Fatal("default policy must not defer", out, err)
		}
		retire(t, f.a, w)
	})
	t.Run("background writer", func(t *testing.T) {
		f := deferFixture(t, 100*time.Millisecond)
		r := concurrentWriter(t, f)
		r.Class = Background
		w := grant(t, f.g, r)
		if out, err := deferredStep(t, f); err != nil || out.NativeStarted.IsZero() {
			t.Fatal("a background writer must not defer the PASSIVE", out, err)
		}
		retire(t, f.a, w)
	})
	t.Run("idle writer lane", func(t *testing.T) {
		f := deferFixture(t, 100*time.Millisecond)
		if out, err := deferredStep(t, f); err != nil || out.NativeStarted.IsZero() {
			t.Fatal("no writer in flight, no deferral", out, err)
		}
	})
	t.Run("recovery ticket", func(t *testing.T) {
		f := deferFixture(t, 100*time.Millisecond)
		w := grant(t, f.g, concurrentWriter(t, f))
		f.clock.ms.Add(60)
		ticket, err := f.a.queueCheckpointContext(t.Context(), f.clock.now().Add(time.Second), 50*time.Millisecond, true)
		if err != nil {
			t.Fatal("pressure recovery must never be deferred for a foreground writer", err)
		}
		f.a.cancelCheckpointTicket(ticket)
		retire(t, f.a, w)
	})
}

func TestPassiveDeferMaxPolicyValidation(t *testing.T) {
	p := Phase1aWALPolicy()
	p.PassiveDeferMax = 250 * time.Millisecond
	if _, err := NewWALGate(p); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []time.Duration{-1, 6 * time.Second} {
		q := p
		q.PassiveDeferMax = bad
		if _, err := NewWALGate(q); err == nil {
			t.Fatal("accepted PassiveDeferMax", bad)
		}
	}
	q := p
	q.ConcurrentPassive, q.CheckpointMaxHold = false, 0
	if _, err := NewWALGate(q); err == nil {
		t.Fatal("deferral requires concurrent PASSIVE")
	}
}

// A clock that steps backward counts as the bound spent, and a frozen clock is
// bounded by the offer-count backstop: the deferral can never be extended or become
// unbounded on an unusual injected clock.
func TestPassiveDeferralIsBoundedOnBackwardAndFrozenClocks(t *testing.T) {
	t.Run("backward step", func(t *testing.T) {
		f := deferFixture(t, 100*time.Millisecond)
		w := grant(t, f.g, concurrentWriter(t, f))
		if out, err := deferredStep(t, f); !errors.Is(err, ErrCheckpointForeground) {
			t.Fatal("first offer defers", out, err)
		}
		f.clock.ms.Add(-10_000) // the clock steps back well before the episode start
		if out, err := f.cp.Step(t.Context(), concurrentCPRequest(f)); errors.Is(err, ErrCheckpointForeground) {
			t.Fatal("a backward step must count as the bound spent, never extend the deferral", out, err)
		}
		retire(t, f.a, w)
	})
	t.Run("frozen clock", func(t *testing.T) {
		f := deferFixture(t, 100*time.Millisecond)
		w := grant(t, f.g, concurrentWriter(t, f))
		now := f.clock.now()
		deferred := 0
		for ; deferred <= maxPassiveDeferOffers+1; deferred++ {
			ticket, err := f.a.queueCheckpointContext(t.Context(), now.Add(time.Second), 50*time.Millisecond, false)
			if err == nil {
				f.a.cancelCheckpointTicket(ticket)
				break
			}
			if !errors.Is(err, ErrCheckpointForeground) {
				t.Fatal(err)
			}
		}
		if deferred != maxPassiveDeferOffers {
			t.Fatalf("a frozen clock must be bounded by the offer backstop: deferred %d times, want %d", deferred, maxPassiveDeferOffers)
		}
		retire(t, f.a, w)
	})
}
