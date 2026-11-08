package governor

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

func pressureWriter(t *testing.T, f *keeperFixture) Request {
	t.Helper()
	f.sample(t)
	r := concurrentWriter(t, f)
	s, err := ReadWAL(f.path)
	if err != nil {
		t.Fatal(err)
	}
	// Cross the unchanged pressure point with a prospective whole-command append.
	r.Need[WALBytes] = f.wal.policy.ActivePressureBytes - s.ActiveBytes + 1
	return r
}

func TestPrepareWriterPressureResetsBeforeAdmission(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	next := pressureWriter(t, f)
	deadline := next.Deadline
	before := f.a.Usage()
	out, err := f.service.PrepareWriter(t.Context(), concurrentCPRequest(f), next)
	if err != nil || out.Refusal != nil || !out.Recovery || out.Deferred || out.NativeStarted.IsZero() || !out.PhysicalSafe || out.After.ActiveBytes != 0 || out.After.PhysicalBytes != 0 {
		t.Fatal("pressure did not actually reset", out, err)
	}
	if f.a.Usage() != before || next.Deadline != deadline {
		t.Fatal("custody/deadline changed")
	}
	refused(t, f.g, next, FailSafe) // stale pre-reset generation confers no authority
	risk, err := f.wal.RestartRisk(f.clock.now())
	if err != nil {
		t.Fatal(err)
	}
	next.RestartRisk = risk
	p := grant(t, f.g, next)
	if _, err := f.writer.ExecContext(t.Context(), "UPDATE source SET value='after-pressure' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	retire(t, f.a, p)
	f.sample(t)
	var got string
	if err := f.writer.QueryRowContext(t.Context(), "SELECT value FROM source WHERE id=1").Scan(&got); err != nil || got != "after-pressure" {
		t.Fatal(got, err)
	}
}

func TestPrepareWriterSmallWALNoMaintenanceAndStaleRiskRefused(t *testing.T) {
	f := newKeeperFixtureMode(t, true)
	f.bind(t)
	f.sample(t)
	next := concurrentWriter(t, f)
	before := f.a.Usage()
	out, err := f.service.PrepareWriter(t.Context(), concurrentCPRequest(f), next)
	if err != nil || out.Recovery || !out.Started.IsZero() || f.a.Usage() != before {
		t.Fatal(out, err)
	}
	p := grant(t, f.g, next)
	retire(t, f.a, p)
	f.sample(t)
	if _, err := f.service.PrepareWriter(t.Context(), concurrentCPRequest(f), next); !errors.Is(err, ErrWALObservation) {
		t.Fatal("old risk was allowed", err)
	}
}

func TestPrepareWriterDrainCancellationKeepsPhysicalReaderCharged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newKeeperFixtureMode(t, true)
		f.bind(t)
		next := pressureWriter(t, f)
		reader := snapshotRequest()
		reader.Deadline = f.clock.now().Add(time.Second)
		p := grant(t, f.g, reader)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := f.service.PrepareWriter(ctx, concurrentCPRequest(f), next); done <- err }()
		synctest.Wait()
		if f.g.(*ledger).resetOwner == nil {
			t.Fatal("recovery did not drain")
		}
		f.g.Cancel(p)
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatal("cancellation refunded physical reader", err)
		default:
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if f.g.(*ledger).resetOwner != nil || f.a.Usage()[Foreground][Reader] != 1 {
			t.Fatal("barrier leaked or reader refunded")
		}
		retire(t, f.a, p)
	})
}

func TestCheckpointNativeBusyIsDeferredWithUnavailableCounts(t *testing.T) {
	for _, tuple := range [][3]int64{{1, -1, -1}, {1, 3, 2}, {0, -1, -1}, {1, -1, 0}, {0, 2, 3}} {
		t.Run(fmt.Sprint(tuple), func(t *testing.T) {
			faults := &snapshotConnector{checkpointTuple: &tuple}
			f := newKeeperFixtureMode(t, true, faults)
			f.bind(t)
			before := f.a.Usage()
			out, err := f.cp.Step(t.Context(), concurrentCPRequest(f))
			valid := tuple[0] == 1 && ((tuple[1] == -1 && tuple[2] == -1) || (tuple[1] >= 0 && tuple[2] >= 0 && tuple[2] <= tuple[1]))
			if valid && (err != nil || !out.Deferred || out.Frames != int(tuple[1]) || out.Backfilled != int(tuple[2])) {
				t.Fatal("native BUSY became corruption/backfill success", out, err)
			}
			if !valid && (!errors.Is(err, ErrWALObservation) || out.Deferred) {
				t.Fatal("malformed counts accepted", out, err)
			}
			if !out.PhysicalSafe || f.a.Usage() != before || f.cp.pending != nil || f.k.flags.Load()&keeperBroken != 0 {
				t.Fatal("lost physical custody/keeper", out, err)
			}
		})
	}
}

func TestPrepareWriterOriginalDeadlineBoundsCheckpointJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newKeeperFixtureMode(t, true)
		f.bind(t)
		next := pressureWriter(t, f)
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		next.Deadline, _ = ctx.Deadline()
		before := f.a.Usage()
		f.cp.mu.Lock()
		done := make(chan error, 1)
		go func() { _, err := f.service.PrepareWriter(ctx, concurrentCPRequest(f), next); done <- err }()
		synctest.Wait()
		time.Sleep(20 * time.Millisecond)
		err := <-done // must return BEFORE the checkpointer lock is released
		f.cp.mu.Unlock()
		if !errors.Is(err, context.DeadlineExceeded) || f.a.Usage() != before || f.g.(*ledger).resetOwner != nil {
			t.Fatal("original deadline was stretched", err)
		}
	})
}
