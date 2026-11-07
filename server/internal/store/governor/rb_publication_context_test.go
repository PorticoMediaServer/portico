package governor

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRBPublicationAdmittedContextAndOriginalDeadline(t *testing.T) {
	for _, change := range []string{"none", "context", "grant", "expiry", "extended-request"} {
		t.Run(change, func(t *testing.T) {
			gi, a, clock := rbPreparationFixture(t)
			g := gi.(*ledger)
			r := rbWriter(clock, ScanMinimum, 7)
			p := grant(t, g, r)
			defer retire(t, a, p)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered := make(chan time.Duration, 1)
			expiry := make(chan time.Time, 1)
			g.mu.timer = func(d time.Duration) (<-chan time.Time, func()) { entered <- d; return expiry, func() {} }
			g.mu.Lock()
			locked := true
			defer func() {
				if locked {
					g.mu.Unlock()
				}
			}()
			expected := r
			if change == "extended-request" {
				expected.Deadline = expected.Deadline.Add(time.Hour)
			}
			done := make(chan error, 1)
			go func() { done <- a.CheckPublicationContext(ctx, p, expected, PublicationFootprint{{Library: 7}}) }()
			select {
			case remaining := <-entered:
				if remaining != time.Minute {
					t.Error("caller extended original deadline", remaining)
				}
			case err := <-done:
				t.Fatal("admitted validation refused contention", err)
			}
			want := error(nil)
			switch change {
			case "context":
				cancel()
				want = context.Canceled
			case "grant":
				gi.Cancel(p)
				want = ErrGrant
			case "expiry":
				clock.ms.Store(60000)
				expiry <- clock.now()
				want = ErrGrant
			case "extended-request":
				want = ErrGrant
			}
			if change == "none" || change == "extended-request" {
				g.mu.Unlock()
				locked = false
			}
			if err := <-done; !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			if locked {
				if g.find(p) == nil || g.used[r.Class] != r.Need {
					t.Fatal("validation refunded physical custody")
				}
				g.mu.Unlock()
				locked = false
			}
		})
	}
}

func TestRBPublicationMixedCompositionKeepsUndeclaredGlobal(t *testing.T) {
	g, a, clock := rbPreparationFixture(t)
	_ = rbBegin(t, a, clock, 1, 7)
	// An undeclared phase1 command in an ENABLED phase2 governor conflicts even
	// when its implementation happens to touch another library. No exemption is
	// inferred from its command ID, caller, or absent resolution marker.
	r := rbWriter(clock, ScanMinimum, 8)
	r.Footprint = nil
	refused(t, g, r, Conflict)
	r.Footprint = []Scope{{Library: 8}}
	p := grant(t, g, r)
	if err := a.CheckPublication(p, r, nil); !errors.Is(err, ErrPublicationFootprint) {
		t.Fatal("narrow unresolved effects accepted", err)
	}
	if err := a.CheckPublication(p, r, PublicationFootprint{{Library: 8}}); err != nil {
		t.Fatal(err)
	}
	retire(t, a, p)
}
