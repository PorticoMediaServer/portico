package governor

import (
	"context"
	"errors"
	"testing"
	"time"
)

func concurrentCPRequest(f *keeperFixture) Request {
	r := nativeCheckpointRequest(f.g)
	r.MaxHold = 5 * time.Second
	r.Deadline = f.g.(*ledger).config.Now().Add(10 * time.Second)
	r.Need[IOBytes] = 2 * f.wal.policy.HardBytes
	return r
}
func concurrentWriter(t *testing.T, f *keeperFixture) Request {
	t.Helper()
	r := checkpointWriter(f.clock, Foreground)
	risk, err := f.wal.RestartRisk(f.clock.now())
	if err != nil {
		t.Fatal(err)
	}
	if !risk.Possible || risk.HeaderBytes != 32 {
		t.Fatal("missing conservative reset risk", risk)
	}
	r.RestartRisk = risk
	return r
}
func TestConcurrentPassiveDoesNotExcludeWriterAndKeepsExactRisk(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	f.sample(t)
	r := concurrentWriter(t, f)
	cp := f.a.tryCheckpoint(concurrentCPRequest(f))
	if cp.Grant == nil {
		t.Fatal(cp.Refusal)
	}
	p := grant(t, f.g, r)
	refused(t, f.g, r, Capacity)
	retire(t, f.a, p)
	f.sample(t) // a real post-physical-W anchor is legal DURING PASSIVE
	stale := r
	r = concurrentWriter(t, f)
	refused(t, f.g, stale, FailSafe)
	missing := r
	missing.RestartRisk = WALRestartRisk{}
	refused(t, f.g, missing, FailSafe)
	p = grant(t, f.g, r)
	retire(t, f.a, p)
	retire(t, f.a, cp.Grant)
	f.sample(t)
	if f.cp.pool.Stats().OpenConnections != 1 || f.k.native.closed {
		t.Fatal("lost keeper")
	}
	out, err := f.cp.Step(t.Context(), concurrentCPRequest(f))
	if err != nil || out.Refusal != nil || !out.PhysicalSafe || out.EligibleInterval != 50*time.Millisecond {
		t.Fatal(out, err)
	}
}
func TestConcurrentPassiveIOPairDoesNotWaiveOtherStreams(t *testing.T) {
	for _, kind := range []string{"scan", "helper"} {
		for _, cpFirst := range []bool{true, false} {
			name := kind + "/stream-first"
			if cpFirst {
				name = kind + "/checkpoint-first"
			}
			t.Run(name, func(t *testing.T) {
				f := newKeeperFixtureMode(t, true)
				f.bind(t)
				f.sample(t)
				cpR := concurrentCPRequest(f)
				cpR.IO = []DeviceIO{{DeviceID: "database", Class: Sequential, Outstanding: 1, Bytes: cpR.Need[IOBytes]}}
				other := Request{Key: JobKey{Domain: 1, ID: kind}, Owner: 1, Class: ScanMinimum, Need: Amounts{MemoryBytes: 64 << 10, IOBytes: 4096}, IO: []DeviceIO{{DeviceID: "database", Class: Seeking, Outstanding: 1, Bytes: 4096}}, Deadline: f.clock.now().Add(time.Second)}
				if kind == "helper" {
					other.Need[Helper] = 1
				}
				if cpFirst {
					cp := f.a.tryCheckpoint(cpR)
					if cp.Grant == nil {
						t.Fatal(cp.Refusal)
					}
					// Prove the permitted W/CP pair, then physically retire W BEFORE the
					// excluded stream test. Only CP's own device custody can deny it now.
					r := concurrentWriter(t, f)
					r.Need[IOBytes] = 4096
					r.IO = other.IO
					writer := grant(t, f.g, r)
					retire(t, f.a, writer)
					decision := f.g.Try(other)
					if decision.Grant != nil {
						retire(t, f.a, decision.Grant)
						retire(t, f.a, cp.Grant)
						t.Fatal("stream ignored CP device custody")
					}
					if decision.Refusal == nil || decision.Refusal.Code != Capacity {
						t.Fatal(decision)
					}
					retire(t, f.a, cp.Grant)
				} else {
					stream := grant(t, f.g, other)
					decision := f.a.tryCheckpoint(cpR)
					if decision.Grant != nil || decision.Refusal == nil || decision.Refusal.Code != Capacity {
						if decision.Grant != nil {
							retire(t, f.a, decision.Grant)
						}
						retire(t, f.a, stream)
						t.Fatal("CP ignored existing stream", decision)
					}
					retire(t, f.a, stream)
				}
			})
		}
	}
}

func TestRecoveryTruncateDrainsPhysicalHoldingsAndActuallyResumes(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	s, err := ReadWAL(f.path)
	if err != nil {
		t.Fatal(err)
	}
	// Exact-prefix hard cap is test-only, not a production profile change.
	oldPolicy := f.wal.policy
	t.Cleanup(func() { f.wal.policy = oldPolicy })
	f.wal.policy.HardBytes = s.PhysicalBytes
	f.wal.policy.ReserveBytes = 32
	f.sample(t)
	r := concurrentWriter(t, f)
	refused(t, f.g, r, Capacity)
	read := snapshotRequest()
	read.Deadline = f.clock.now().Add(time.Second)
	loan := grant(t, f.g, read)
	f.g.Cancel(loan)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = f.cp.RecoverWAL(ctx, concurrentCPRequest(f)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if f.g.(*ledger).resetOwner != nil {
		t.Fatal("cancelled recovery barrier leaked")
	}
	if f.a.Usage()[Foreground][Reader] != 1 {
		t.Fatal("cancel refunded old reader")
	}
	retire(t, f.a, loan)
	out, err := f.cp.RecoverWAL(t.Context(), concurrentCPRequest(f))
	if err != nil || out.Refusal != nil || !out.Recovery || out.After.PhysicalBytes != 0 || !out.PhysicalSafe {
		t.Fatal(out, err)
	}
	r = concurrentWriter(t, f)
	r.Need[WALBytes] = 4096
	p := grant(t, f.g, r)
	if _, err = f.writer.ExecContext(t.Context(), "UPDATE source SET value='resumed'"); err != nil {
		t.Fatal(err)
	}
	retire(t, f.a, p)
}

func TestConcurrentPassiveNativeErrorCancelAndPanicCustody(t *testing.T) {
	for _, fault := range []string{"error", "cancel", "panic"} {
		t.Run(fault, func(t *testing.T) {
			counted := &snapshotConnector{}
			f := newKeeperFixtureMode(t, true, counted)
			f.bind(t)
			f.sample(t)
			native := f.k.native
			switch fault {
			case "error":
				counted.checkpointError.Store(true)
				out, err := f.cp.Step(t.Context(), concurrentCPRequest(f))
				if !errors.Is(err, checkpointInjectedError) || !out.PhysicalSafe || native.closed || f.cp.pending != nil || f.k.flags.Load()&keeperBroken != 0 {
					t.Fatal(out, err)
				}
			case "cancel":
				counted.checkpointCancelEntered = make(chan struct{})
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error, 1)
				go func() { _, err := f.cp.Step(ctx, concurrentCPRequest(f)); done <- err }()
				<-counted.checkpointCancelEntered
				if f.a.Usage()[Recovery][CPUMilli] != 1000 {
					t.Fatal("early CP refund")
				}
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if f.cp.pending != nil || native.closed || f.k.flags.Load()&keeperBroken != 0 {
					t.Fatal("known cancel lost healthy keeper")
				}
			case "panic":
				counted.checkpointPanic.Store(true)
				var primary any
				func() { defer func() { primary = recover() }(); _, _ = f.cp.Step(t.Context(), concurrentCPRequest(f)) }()
				if primary != checkpointInjectedPanic || f.cp.pending == nil || native.closed || f.k.flags.Load()&keeperBroken == 0 {
					t.Fatal("panic lost raw custody/fence", primary)
				}
				if f.a.Usage()[Recovery][CPUMilli] != 1000 || f.a.Retired(f.resources.Lifetime) != ErrRetirement {
					t.Fatal("panic refunded custody")
				}
				counted.checkpointPanic.Store(false)
			}
			if counted.opened.Load() != 1 {
				t.Fatal("unfunded replacement", counted.opened.Load())
			}
		})
	}
}

func TestRecoveryBarrierWaitsForPhysicalRetirementNotCancel(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	f.sample(t)
	read := snapshotRequest()
	read.Deadline = f.clock.now().Add(time.Second)
	loan := grant(t, f.g, read)
	f.g.Cancel(loan)
	entered := make(chan struct{})
	original := f.cp.deadlineContext
	f.cp.deadlineContext = func(ctx context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
		close(entered)
		return context.WithCancel(ctx)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		release, err := f.cp.beginRecovery(ctx, f.clock.now().Add(time.Second))
		if release != nil {
			release()
		}
		done <- err
	}()
	<-entered
	refused(t, f.g, concurrentWriter(t, f), Capacity)
	refused(t, f.g, read, Capacity)
	select {
	case err := <-done:
		t.Fatal("cancelled reader treated as retired", err)
	default:
	}
	retire(t, f.a, loan)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.cp.deadlineContext = original
	if f.g.(*ledger).resetOwner != nil {
		t.Fatal("barrier leaked")
	}
}

func TestRecoveryDoesNotWaitForConsumerAfterPhysicalSnapshotClose(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	// Tiny exact-cap fixture; no product policy mutation.
	state, err := ReadWAL(f.path)
	if err != nil {
		t.Fatal(err)
	}
	old := f.wal.policy
	t.Cleanup(func() { f.wal.policy = old })
	f.wal.policy.HardBytes = state.PhysicalBytes
	f.wal.policy.ReserveBytes = 32
	snapshots, _, _ := snapshotReaderDriver(t, f.path, f.g, f.a)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := snapshots.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error {
			if e := snapshotQueryOne(scope); e != nil {
				return e
			}
			close(entered)
			<-release
			return nil
		})
		done <- err
	}()
	<-entered
	if err = snapshots.Close(); err != nil {
		t.Fatal(err)
	}
	if f.a.Usage()[Foreground][Reader] != 1 {
		t.Fatal("consumer custody refunded")
	}
	out, err := f.cp.RecoverWAL(t.Context(), concurrentCPRequest(f))
	close(release)
	<-done
	if err != nil || out.Refusal != nil || out.After.PhysicalBytes != 0 {
		t.Fatal("closed native snapshot blocked reset", out, err)
	}
}

func TestRecoverySpaceRefusalBelowThresholdAndUnusedRecoveryRejected(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	s, err := ReadWAL(f.path)
	if err != nil {
		t.Fatal(err)
	}
	old := f.wal.policy
	t.Cleanup(func() { f.wal.policy = old })
	f.wal.policy.HardBytes = s.PhysicalBytes + (20 << 10)
	f.wal.policy.ReserveBytes = 16 << 10
	if s.ActiveBytes >= f.wal.policy.HardBytes-f.wal.policy.ReserveBytes {
		t.Fatal("fixture not below threshold")
	}
	out, err := f.cp.RecoverWAL(t.Context(), concurrentCPRequest(f))
	if !errors.Is(err, ErrCheckpointBudget) || !out.NativeStarted.IsZero() {
		t.Fatal("unneeded reset permitted", out, err)
	}
	f.sample(t)
	r := concurrentWriter(t, f)
	r.Need[WALBytes] = 24 << 10
	refused(t, f.g, r, Capacity)
	out, err = f.cp.RecoverWAL(t.Context(), concurrentCPRequest(f))
	if err != nil || out.Refusal != nil || out.After.PhysicalBytes != 0 {
		t.Fatal("legal command wedged below threshold", out, err)
	}
	r = concurrentWriter(t, f)
	r.Need[WALBytes] = 24 << 10
	p := grant(t, f.g, r)
	if _, err = f.writer.ExecContext(t.Context(), "UPDATE source SET value='below-threshold-resume'"); err != nil {
		t.Fatal(err)
	}
	retire(t, f.a, p)
}

func TestConcurrentCheckpointTicketStartsUnderContinuousWriterCustody(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	for i := 0; i < 3; i++ {
		f.sample(t)
		writer := grant(t, f.g, concurrentWriter(t, f))
		out, err := f.cp.Step(t.Context(), concurrentCPRequest(f))
		if err != nil || out.Refusal != nil || !out.PhysicalSafe || out.NativeStarted.IsZero() || out.SampleUnavailable == "" {
			t.Fatal("CP starved behind active writer", out, err)
		}
		if !f.wal.writer {
			t.Fatal("CP released someone else's writer custody")
		}
		retire(t, f.a, writer)
		f.clock.ms.Add(50)
	}
}

func TestConcurrentContextConstructionPanicDoesNotStrandAdmittedUnit(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	f.sample(t)
	original := f.cp.deadlineContext
	calls := 0
	f.cp.deadlineContext = func(ctx context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
		calls++
		if calls == 2 {
			panic("CP context construction")
		}
		return original(ctx, deadline)
	}
	var primary any
	func() { defer func() { primary = recover() }(); _, _ = f.cp.Step(t.Context(), concurrentCPRequest(f)) }()
	f.cp.deadlineContext = original
	if primary != "CP context construction" || f.cp.pending != nil || f.a.Usage()[Recovery][CPUMilli] != 0 || f.k.flags.Load()&keeperBroken == 0 {
		t.Fatal(primary, f.cp.pending, f.a.Usage())
	}
}
