package governor

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestCheckpointStepAbandonsTicketWithoutGateLock(t *testing.T) {
	for _, mode := range []string{"cancel", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newKeeperFixtureMode(t, true)
				f.bind(t)
				f.sample(t)
				g := f.g.(*ledger)
				original := g.config.Now
				held := make(chan struct{})
				// Queue succeeds first; hold the gate precisely before admission tries to
				// reserve it. Step must unwind its unused ticket BEFORE this lock is freed.
				g.config.Now = func() time.Time {
					now := original()
					if f.wal.pendingCheckpoint != nil && f.wal.pendingCheckpoint.signalled {
						f.wal.mu.Lock()
						g.config.Now = original
						close(held)
					}
					return now
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				r := concurrentCPRequest(f)
				type result struct {
					out CheckpointResult
					err error
				}
				done := make(chan result, 1)
				go func() { out, err := f.cp.Step(ctx, r); done <- result{out, err} }()
				<-held
				locked := true
				finished := false
				defer func() {
					if locked {
						f.wal.mu.Unlock()
					}
					if !finished {
						<-done
					}
				}()
				synctest.Wait()
				ticket := f.wal.pendingCheckpoint
				if ticket == nil {
					t.Fatal("control missed queued ticket")
				}
				if mode == "cancel" {
					cancel()
				} else {
					f.clock.ms.Add(50)
					time.Sleep(50 * time.Millisecond)
				}
				synctest.Wait()
				select {
				case got := <-done:
					finished = true
					if got.out.Refusal == nil || got.out.Refusal.Code != Deadline || !got.out.Started.IsZero() || !got.out.NativeStarted.IsZero() {
						t.Fatal("unexpected physical admission", got)
					}
				default:
					t.Fatal("unused-ticket cleanup blocked on the WAL gate")
				}
				if !ticket.cancelled.Load() || f.wal.checkpoint || f.wal.writer {
					t.Fatal("cleanup lost metadata or changed physical custody")
				}
				if f.a.Usage()[Recovery] != f.resources.LifetimeRequest.Need {
					t.Fatal("cleanup changed keeper custody")
				}
				f.wal.mu.Unlock()
				locked = false
				// A stale caller cannot consume cancelled metadata even before wall expiry;
				// pruning is lazy under the next gate owner and keeps pacing unchanged.
				code, cause := f.wal.reserveCheckpoint(ticket, f.clock.now(), r.Deadline)
				if code != Deadline || cause != CauseCancelled || f.wal.pendingCheckpoint != nil || f.wal.checkpoint {
					t.Fatal(code, cause)
				}
			})
		})
	}
}
