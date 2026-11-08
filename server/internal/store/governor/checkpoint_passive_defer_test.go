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
	// A later busy writer begins a NEW episode: the old first deferral never waives it.
	f.clock.ms.Add(5000)
	f.sample(t)
	w = grant(t, f.g, concurrentWriter(t, f))
	if out, err = deferredStep(t, f); !errors.Is(err, ErrCheckpointForeground) {
		t.Fatal("a stale episode must not waive a new deferral", out, err)
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
