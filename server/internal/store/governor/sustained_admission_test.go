package governor

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// Each historical pre-native failure has its own boundary. No scheduler sleeps
// or native elapsed-time assumption: the token is deliberately held, then given
// back while the request retains its ORIGINAL budget.
func TestAdmissionContentionWaitsForOriginalDeadline(t *testing.T) {
	for _, branch := range []string{"ledger", "writer_gate", "checkpoint_gate", "checkpoint_queue", "checkpointer", "restart_risk"} {
		t.Run(branch, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				gi, a, clock := setup(t)
				g := gi.(*ledger)
				w := gate(t)
				g.config.WAL = w
				publish(t, w, walSample(4, 4), clock.now())
				r := request(clock, Foreground)
				r.Deadline = clock.now().Add(time.Second)
				cp := &Checkpointer{authority: a, wal: w, closed: true}
				lock := &w.mu
				if branch == "ledger" {
					lock = &g.mu
				}
				if branch == "checkpointer" {
					lock = &cp.mu
				}
				lock.Lock()
				locked := true
				defer func() {
					if locked {
						lock.Unlock()
					}
				}()
				type result struct {
					grant   *Grant
					refusal *Refusal
					code    RefusalCode
					err     error
					ticket  *checkpointTicket
				}
				done := make(chan result, 1)
				go func() {
					var out result
					switch branch {
					case "ledger":
						d := g.Try(r)
						out.grant, out.refusal = d.Grant, d.Refusal
					case "writer_gate":
						wr := checkpointWriter(clock, Foreground)
						out.code = w.reserve(wr, clock.now())
					case "checkpoint_gate":
						out.code, _ = w.reserveCheckpoint(nil, clock.now(), r.Deadline)
					case "checkpoint_queue":
						out.ticket, out.err = a.queueCheckpointMode(r.Deadline, 50*time.Millisecond, false)
					case "checkpointer":
						_, out.err = cp.Step(context.Background(), r)
					case "restart_risk":
						_, out.err = w.RestartRiskContext(context.Background(), clock.now(), r.Deadline)
					}
					done <- out
				}()
				synctest.Wait()
				select {
				case out := <-done:
					t.Fatal("contention became refusal", out)
				default:
				}
				lock.Unlock()
				locked = false
				out := <-done
				if branch == "checkpointer" {
					if !errors.Is(out.err, ErrCheckpointClosed) {
						t.Fatal(out)
					}
				} else if out.err != nil || out.refusal != nil || out.code != 0 {
					t.Fatal(out)
				}
				if out.grant != nil {
					retire(t, a, out.grant)
				}
				if out.ticket != nil {
					a.cancelCheckpointTicket(out.ticket)
				}
				if branch == "writer_gate" {
					w.returned()
				}
				if branch == "checkpoint_gate" {
					w.checkpointReturned()
				}
			})
		})
	}
	t.Run("expiry", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			gi, a, clock := setup(t)
			g := gi.(*ledger)
			r := request(clock, Foreground)
			g.mu.Lock()
			defer g.mu.Unlock()
			done := make(chan Decision, 1)
			go func() { done <- g.Try(r) }()
			synctest.Wait()
			clock.ms.Add(1000)
			time.Sleep(time.Second)
			out := <-done
			if out.Grant != nil || out.Refusal == nil || out.Refusal.Code != Deadline || out.Refusal.Cause != CauseDeadline {
				t.Fatal(out)
			}
			if g.counts[Foreground] != 0 || g.used[Foreground] != (Amounts{}) {
				t.Fatal("deadline minted custody")
			}
			_ = a
		})
	})
	t.Run("queue_cancel", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			gi, a, clock := setup(t)
			w := gate(t)
			gi.(*ledger).config.WAL = w
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w.mu.Lock()
			defer w.mu.Unlock()
			done := make(chan error, 1)
			go func() {
				_, e := a.queueCheckpointContext(ctx, clock.now().Add(time.Second), 50*time.Millisecond, false)
				done <- e
			}()
			synctest.Wait()
			cancel()
			synctest.Wait()
			select {
			case e := <-done:
				if !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
			default:
				t.Fatal("queue cancel waited for token")
			}
		})
	})
}

func TestWALSampleBoundedResamplingAndGeneration(t *testing.T) {
	ctx := context.Background()
	now := func() time.Time { return time.Unix(1, 0) }
	calls := 0
	state, err := sampleWAL(ctx, func() (WALState, error) {
		calls++
		if calls < 3 {
			return WALState{}, ErrWALObservation
		}
		return walSample(4, 4), nil
	}, now)
	if err != nil || calls != 3 || state.Frames != 4 {
		t.Fatal(calls, state, err)
	}
	calls = 0
	_, err = sampleWAL(ctx, func() (WALState, error) { calls++; return WALState{}, ErrWALObservation })
	if !errors.Is(err, ErrWALObservation) || calls != walSampleAttempts {
		t.Fatal("persistent bad sample not bounded", calls, err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	calls = 0
	_, err = sampleWAL(cancelCtx, func() (WALState, error) { calls++; cancel(); return WALState{}, ErrWALObservation })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal(calls, err)
	}
	w := gate(t)
	token := w.Observation()
	_, err = sampleWAL(ctx, func() (WALState, error) { w.returned(); return walSample(4, 4), nil })
	if err != nil {
		t.Fatal(err)
	}
	if w.observationCurrent(token) || !errors.Is(w.Publish(token, state, time.Now()), ErrWALObservation) {
		t.Fatal("resampling rebased a stale generation")
	}
}

func TestConcurrentCheckpointCannotReplaceWriterRiskAnchor(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	f.sample(t)
	before, err := f.wal.RestartRisk(f.clock.now())
	if err != nil {
		t.Fatal(err)
	}
	f.clock.ms.Add(1)
	out, err := f.cp.Step(t.Context(), concurrentCPRequest(f))
	if err != nil || out.Refusal != nil || !out.PhysicalSafe {
		t.Fatal(out, err)
	}
	after, err := f.wal.RestartRisk(f.clock.now())
	if err != nil || before != after {
		t.Fatal("CP telemetry changed pending writer forecast", before, after, err)
	}
}

func TestConcurrentCheckpointSampleRaceAndPersistentInvalid(t *testing.T) {
	for _, race := range []bool{true, false} {
		t.Run(map[bool]string{true: "writer_generation", false: "persistent_invalid"}[race], func(t *testing.T) {
			f := newKeeperFixtureMode(t, true)
			f.bind(t)
			f.sample(t)
			var writer *Grant
			if race {
				writer = grant(t, f.g, concurrentWriter(t, f))
			}
			// Exhausted bounded resampling at a controlled source; the real native
			// PASSIVE still runs in the writer-generation case under its hard envelope.
			f.cp.readWAL = func(context.Context, string) (WALState, error) { return WALState{}, ErrWALObservation }
			out, err := f.cp.Step(t.Context(), concurrentCPRequest(f))
			if race {
				if err != nil || out.Refusal != nil || !out.PhysicalSafe || out.NativeStarted.IsZero() || out.BeforeUnavailable == "" || out.SampleUnavailable == "" {
					t.Fatal(out, err)
				}
				if f.a.ledger.state.Load()&1 != 0 {
					t.Fatal("normal sample race fenced")
				}
				retire(t, f.a, writer)
				f.sample(t)
			} else {
				if !errors.Is(err, ErrWALObservation) || f.a.ledger.state.Load()&1 == 0 || !out.NativeStarted.IsZero() {
					t.Fatal("persistent invalid sample not fenced before SQL", out, err)
				}
			}
		})
	}
}

func TestAdmissionRefusalsKeepTypedBudgetCauses(t *testing.T) {
	gi, a, clock := setup(t)
	g := gi.(*ledger)
	r := request(clock, Foreground)
	r.Need[Reader] = g.config.Partitions[Foreground][Reader] + 1
	d := g.Try(r)
	if d.Refusal == nil || d.Refusal.Code != Capacity || d.Refusal.Cause != CausePartition {
		t.Fatal(d)
	}
	w := gate(t)
	g.config.WAL = w
	r = checkpointWriter(clock, Foreground)
	d = g.Try(r)
	if d.Refusal == nil || d.Refusal.Cause != CauseWALSample {
		t.Fatal(d)
	}
	publish(t, w, walSample(4, 4), clock.now())
	p := grant(t, g, r)
	d = g.Try(r)
	if d.Refusal == nil || d.Refusal.Cause != CauseWriterActive {
		t.Fatal(d)
	}
	retire(t, a, p)
}

func TestCheckpointLifetimeValidationUsesOfferDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gi, a, clock := setup(t)
		g := gi.(*ledger)
		r := request(clock, Recovery)
		p := grant(t, g, r)
		until := clock.now().Add(10 * time.Millisecond)
		g.mu.Lock()
		locked := true
		defer func() {
			if locked {
				g.mu.Unlock()
			}
		}()
		done := make(chan error, 1)
		go func() { done <- a.checkContextUntil(context.Background(), p, r, time.Time{}, until) }()
		synctest.Wait()
		clock.ms.Add(10)
		time.Sleep(10 * time.Millisecond)
		if e := <-done; !errors.Is(e, context.DeadlineExceeded) {
			t.Fatal(e)
		}
		if g.find(p) == nil || g.used[Recovery] != r.Need {
			t.Fatal("offer expiry retired keeper lifetime")
		}
		g.mu.Unlock()
		locked = false
		if e := a.Check(p, r); e != nil {
			t.Fatal("short offer revoked lifetime", e)
		}
		retire(t, a, p)
	})
}
