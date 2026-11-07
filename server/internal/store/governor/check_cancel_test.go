package governor

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestAdmittedCheckCancellationWakesWithoutLedgerHandoff(t *testing.T) {
	for _, source := range []string{"grant", "context", "context-deadline"} {
		t.Run(source, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				gi, a, clock := setup(t)
				g := gi.(*ledger)
				r := request(clock, Foreground)
				p := grant(t, g, r)
				never := make(chan time.Time)
				g.mu.timer = func(time.Duration) (<-chan time.Time, func()) { return never, func() {} }
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if source == "context-deadline" {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, 10*time.Millisecond)
					defer stop()
				}
				g.mu.Lock()
				locked := true
				defer func() {
					if locked {
						g.mu.Unlock()
					}
				}()
				done := make(chan error, 1)
				go func() {
					if source == "grant" {
						done <- a.Check(p, r)
					} else {
						done <- a.CheckContext(ctx, p, r)
					}
				}()
				synctest.Wait()
				select {
				case err := <-done:
					t.Fatal("check did not wait", err)
				default:
				}
				want := error(ErrGrant)
				switch source {
				case "grant":
					gi.Cancel(p)
					gi.Cancel(p) // notification closes exactly once
				case "context":
					cancel()
					want = context.Canceled
				case "context-deadline":
					time.Sleep(10 * time.Millisecond)
					want = context.DeadlineExceeded
				}
				synctest.Wait()
				select {
				case err := <-done:
					if !errors.Is(err, want) {
						t.Fatal(err)
					}
				default:
					t.Fatal("cancellation waits for ledger handoff")
				}
				if g.used[r.Class] != r.Need || g.find(p) == nil {
					t.Fatal("cancel refunded physical custody")
				}
				g.mu.Unlock()
				locked = false
				retire(t, a, p)
			})
		})
	}
}

func TestReadScopeQueryContextCancelsItsValidationWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gi, a, clock := setup(t)
		g := gi.(*ledger)
		r := request(clock, Foreground)
		p := grant(t, g, r)
		never := make(chan time.Time)
		g.mu.timer = func(time.Duration) (<-chan time.Time, func()) { return never, func() {} }
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		scope := &ReadScope{ctx: ctx, authority: a, grant: p, expected: r}
		g.mu.Lock()
		locked := true
		defer func() {
			if locked {
				g.mu.Unlock()
			}
		}()
		done := make(chan error, 1)
		go func() { _, err := scope.Query("not-reached"); done <- err }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		default:
			t.Fatal("Query cancellation waits for ledger handoff")
		}
		if scope.calls != 0 || scope.conn != nil || g.find(p) == nil {
			t.Fatal("cancel started SQL or released custody")
		}
		g.mu.Unlock()
		locked = false
		retire(t, a, p)
	})
}
