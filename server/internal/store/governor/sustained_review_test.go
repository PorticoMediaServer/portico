package governor

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestForegroundAdmissionLockUsesEarliestBound(t *testing.T) {
	for _, mode := range []string{"cancel", "allowance", "ready", "caller_deadline", "handoff", "handoff_expired"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				gi, a, clock := foregroundSetup(t)
				g := gi.(*ledger)
				q := foregroundQ(clock)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				duration := 40 * time.Millisecond
				if mode == "ready" {
					duration = 10 * time.Millisecond
					q.ReadyDeadline = clock.now().Add(duration)
				}
				if mode == "caller_deadline" {
					duration = 5 * time.Millisecond
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, duration)
					defer stop()
				}
				g.mu.Lock()
				locked := true
				defer func() {
					if locked {
						g.mu.Unlock()
					}
				}()
				done := make(chan Decision, 1)
				go func() { done <- a.AdmitForeground(ctx, q) }()
				synctest.Wait()
				select {
				case d := <-done:
					t.Fatal("did not wait", d)
				default:
				}
				if mode == "handoff_expired" {
					clock.ms.Add(41)
					g.mu.Unlock()
					locked = false
					d := <-done
					if d.Grant != nil || d.Refusal == nil || d.Refusal.Code != Deadline || d.Refusal.Cause != CauseDeadline {
						t.Fatal("post-handoff expiry admitted", d)
					}
					return
				}
				if mode == "handoff" {
					clock.ms.Add(10)
					g.mu.Unlock()
					locked = false
					d := <-done
					if d.Grant == nil || d.Refusal != nil || !d.Grant.deadline.Equal(q.ProtectedDeadline) {
						t.Fatal("admission bound replaced lifetime", d)
					}
					if e := a.CheckForeground(d.Grant, q); e != nil {
						t.Fatal(e)
					}
					retire(t, a, d.Grant)
					return
				}
				if mode == "cancel" {
					cancel()
				} else {
					clock.ms.Add(duration.Milliseconds())
					time.Sleep(duration)
				}
				synctest.Wait()
				select {
				case d := <-done:
					if d.Grant != nil || d.Refusal == nil || d.Refusal.Code != Deadline {
						t.Fatal(d)
					}
				default:
					t.Fatal("admission waited past original caller/ready/allowance bound")
				}
				if len(g.live) != 0 || g.used[Foreground] != (Amounts{}) {
					t.Fatal("refused admission retained custody")
				}
			})
		})
	}
}

func TestWALAdmissionHandoffObservesCallerAndEarliestDeadline(t *testing.T) {
	for _, branch := range []string{"writer", "checkpoint"} {
		for _, mode := range []string{"cancel", "deadline"} {
			t.Run(branch+"/"+mode, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					gi, a, clock, w := checkpointSetup(t)
					g := gi.(*ledger)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					r := checkpointWriter(clock, Foreground)
					isCP := branch == "checkpoint"
					if isCP {
						r = checkpointRequest(clock)
					}
					until := clock.now().Add(10 * time.Millisecond)
					w.mu.Lock()
					locked := true
					defer func() {
						if locked {
							w.mu.Unlock()
						}
					}()
					done := make(chan Decision, 1)
					go func() { done <- g.tryReservedContext(ctx, until, r, isCP, nil, nil, nil, nil) }()
					synctest.Wait()
					if mode == "cancel" {
						cancel()
					} else {
						clock.ms.Add(10)
						time.Sleep(10 * time.Millisecond)
					}
					synctest.Wait()
					select {
					case d := <-done:
						if d.Grant != nil || d.Refusal == nil || d.Refusal.Code != Deadline {
							t.Fatal(d)
						}
					default:
						t.Fatal("WAL handoff ignored caller bound")
					}
					if w.writer || w.checkpoint || len(g.live) != 0 {
						t.Fatal("refused handoff reserved native/grant authority")
					}
					_ = a
				})
			})
		}
	}
}

func TestCheckpointHoldStartsAfterAdmissionHandoff(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	f.sample(t)
	// Force a10ms WAL-gate handoff under protocol time. The following real native
	// CP has a5ms holding forecast; pre-admission waiting must not fence it.
	original := f.g.(*ledger).config.Now
	calls := 0
	f.g.(*ledger).config.Now = func() time.Time {
		calls++
		// The deterministic gate timer advances time when admission has to wait.
		return original()
	}
	f.wal.mu.timer = func(time.Duration) (<-chan time.Time, func()) {
		f.clock.ms.Add(10)
		f.wal.mu.Unlock()
		return make(chan time.Time), func() {}
	}
	// Hold only after queue creation: its clock callback arms the gate for the
	// later reserveCheckpointContext call, not the earlier ticket insertion.
	f.g.(*ledger).config.Now = func() time.Time {
		calls++
		now := original()
		if f.wal.pendingCheckpoint != nil && f.wal.pendingCheckpoint.signalled {
			// This clock runs at ledger admission entry before the WAL handoff; no goroutine/scheduling assumption.
			f.wal.mu.Lock()
			f.g.(*ledger).config.Now = original
		}
		return now
	}
	r := concurrentCPRequest(f)
	r.MaxHold = 5 * time.Millisecond
	before := f.clock.now()
	out, err := f.cp.Step(t.Context(), r)
	if err != nil || out.Refusal != nil || !out.PhysicalSafe || out.Started.Before(before.Add(10*time.Millisecond)) || out.PhysicalReturned.Sub(out.Started) > r.MaxHold || f.cp.unqualified {
		t.Fatal("admission wait consumed native holding", out, err)
	}
	if calls == 0 {
		t.Fatal("control did not execute")
	}
}
