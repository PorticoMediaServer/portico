package governor

import (
	"errors"
	"testing"
	"time"
)

// The held mutex deterministically reproduces Wave5's admitted-reader failure.
// Virtual deadline delivery proves the bound; no sleeps or physical I/O timing.
func TestAdmittedCheckWaitsForLedgerAndRevalidates(t *testing.T) {
	for _, change := range []string{"none", "cancel", "epoch", "retired", "request"} {
		t.Run(change, func(t *testing.T) {
			gi, a, clock := setup(t)
			g := gi.(*ledger)
			r := request(clock, Foreground)
			p := grant(t, g, r)
			entered := make(chan time.Duration, 1)
			expiry := make(chan time.Time)
			g.mu.timer = func(d time.Duration) (<-chan time.Time, func()) { entered <- d; return expiry, func() {} }
			g.mu.Lock()
			done := make(chan error, 1)
			expected := r
			if change == "request" {
				expected.Owner++
			}
			go func() { done <- a.Check(p, expected) }()
			select {
			case remaining := <-entered:
				if remaining != time.Second {
					t.Error("deadline renewed", remaining)
				}
			case err := <-done:
				g.mu.Unlock()
				t.Fatal("admitted check refused contention", err)
			}
			select {
			case err := <-done:
				g.mu.Unlock()
				t.Fatal("check returned while ledger held", err)
			default:
			}
			switch change {
			case "cancel":
				gi.Cancel(p)
			case "epoch":
				a.EnterStatic()
			case "retired":
				delete(g.live, p.id)
				g.counts[r.Class]--
				for k, n := range r.Need {
					g.used[r.Class][k] -= n
				}
			}
			g.mu.Unlock()
			err := <-done
			if change == "none" && err != nil {
				t.Fatal(err)
			}
			if change != "none" && !errors.Is(err, ErrGrant) {
				t.Fatal("state changed while waiting", err)
			}
			if errors.Is(err, ErrBusy) {
				t.Fatal("contention escaped as validation error")
			}
			if change != "retired" {
				retire(t, a, p)
			}
		})
	}
}
func TestAdmittedCheckExpiryUsesOriginalGrantDeadlineAndKeepsCustody(t *testing.T) {
	gi, a, clock := setup(t)
	g := gi.(*ledger)
	r := request(clock, Foreground)
	p := grant(t, g, r)
	entered := make(chan time.Duration, 1)
	expiry := make(chan time.Time, 1)
	g.mu.timer = func(d time.Duration) (<-chan time.Time, func()) { entered <- d; return expiry, func() {} }
	clock.ms.Store(750)
	g.mu.Lock()
	done := make(chan error, 1)
	// Even an invalid caller-supplied later deadline cannot extend lock waiting.
	extended := r
	extended.Deadline = extended.Deadline.Add(time.Hour)
	go func() { done <- a.Check(p, extended) }()
	select {
	case remaining := <-entered:
		if remaining != 250*time.Millisecond {
			t.Error("wrong remaining deadline", remaining)
		}
	case err := <-done:
		g.mu.Unlock()
		t.Fatal("did not wait", err)
	}
	clock.ms.Store(1000)
	expiry <- clock.now()
	err := <-done
	if !errors.Is(err, ErrGrant) || errors.Is(err, ErrBusy) {
		g.mu.Unlock()
		t.Fatal(err)
	}
	if g.used[r.Class] != r.Need || g.find(p) == nil {
		g.mu.Unlock()
		t.Fatal("expiry refunded physical custody")
	}
	g.mu.Unlock()
	retire(t, a, p)
}
func TestAdmittedCheckRejectsExpiryAfterLockHandoff(t *testing.T) {
	gi, a, clock := setup(t)
	g := gi.(*ledger)
	r := request(clock, Foreground)
	p := grant(t, g, r)
	entered := make(chan struct{})
	never := make(chan time.Time)
	g.mu.timer = func(time.Duration) (<-chan time.Time, func()) { close(entered); return never, func() {} }
	g.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- a.Check(p, r) }()
	select {
	case <-entered:
	case err := <-done:
		g.mu.Unlock()
		t.Fatal("did not wait", err)
	}
	clock.ms.Store(1000)
	g.mu.Unlock()
	if err := <-done; err != ErrGrant {
		t.Fatal("expired authority accepted after mutex wait", err)
	}
	retire(t, a, p)
}

func TestAdmittedForegroundCheckUsesSameBoundedLock(t *testing.T) {
	gi, a, clock := foregroundSetup(t)
	g := gi.(*ledger)
	q := foregroundQ(clock)
	p := admitLive(t, a, q)
	entered := make(chan struct{})
	never := make(chan time.Time)
	g.mu.timer = func(time.Duration) (<-chan time.Time, func()) { close(entered); return never, func() {} }
	g.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- a.CheckForeground(p, q) }()
	select {
	case <-entered:
	case err := <-done:
		g.mu.Unlock()
		t.Fatal("LIVE revalidation refused contention", err)
	}
	g.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if a.ForegroundUsage().Live != 1 {
		t.Fatal("check changed live custody")
	}
	retire(t, a, p)
}
