package writer

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestWCDispatcherPressureMaintenanceRefreshesBeforeAdmission(t *testing.T) {
	w, d, _, keeper := wcComponentFixture(t, wcFixtureOptions{kind: "progress", actorTier: "member", extraMemory: 2 << 20, maxGrants: 5, maintenance: true, pressureBytes: 32 << 10})
	// Only this deterministic tiny functional control chooses a lower pressure
	// point. The canonical measurement constructor passes zero: its limits stay.
	r := w.effective
	r.maintenance = &checkpointMaintenance{request: keeper.request("passive"), results: keeper.results}
	before, err := governor.ReadWAL(r.path)
	if err != nil || before.ActiveBytes == 0 {
		t.Fatal(before, err)
	}
	command, err := d.Capture(t.Context(), 0, 5, 100, "playing")
	if err != nil {
		t.Fatal(err)
	}
	result, err := w.AcceptProgress(t.Context(), d.Disclosure(), command)
	if err != nil || result.Outcome.State != Applied || result.Outcome.Evidence == nil {
		t.Fatal(result, err)
	}
	select {
	case record := <-keeper.results:
		if record.Error != "" || !record.Result.Recovery || record.Result.Deferred || record.Result.NativeStarted.IsZero() || record.Result.After.PhysicalBytes != 0 || !record.Result.PhysicalSafe {
			t.Fatal(record)
		}
		if !record.Result.PhysicalReturned.Before(result.Outcome.Evidence.HoldStartedAt) {
			t.Fatal("maintenance was charged as command HOLD")
		}
	default:
		t.Fatal("dispatcher did not compose pressure recovery")
	}
	risk := result.Outcome.Evidence.WALBefore
	if risk == nil || risk.State.ActiveBytes != 0 {
		t.Fatal("old pre-reset anchor reused", risk)
	}
	// The following call remains functional, not a native/replay retry.
	if _, err := w.CurrentProgress(t.Context(), d.Disclosure()); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := keeper.close(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointRefreshKeepsOriginalAdmissionDeadline(t *testing.T) {
	// Exercise exact deadline retention independently of OS/native timing.
	w, path, clock := testExecutor(t)
	_, _, wal, request := governedFixture(t, w.now)
	if err := refreshWriterWAL(path, wal, w.now); err != nil {
		t.Fatal(err)
	}
	r := &effectiveRuntime{executor: w, path: path, wal: wal}
	request.Deadline = clock.Add(5 * time.Millisecond)
	original := request.Deadline
	if _, err := r.refreshAfterCheckpoint(t.Context(), &request); err != nil {
		t.Fatal(err)
	}
	if request.Deadline != original {
		t.Fatal("maintenance renewed admission deadline")
	}
}

func TestMaintenanceDeferralsKeepNativeAndCadenceOutcomesDistinct(t *testing.T) {
	native := checkpointRecord(governor.CheckpointResult{Deferred: true, Busy: 1, Frames: -1, Backfilled: -1, PhysicalSafe: true}, nil)
	if native.failed() || native.Deferral != deferralNativeBusy || native.Result.Frames != -1 {
		t.Fatal(native)
	}
	cadence := checkpointRecord(governor.CheckpointResult{}, governor.ErrCheckpointCadence)
	if cadence.failed() || cadence.Deferral != deferralCadence || cadence.Error == "" || cadence.Result.PhysicalSafe {
		t.Fatal(cadence)
	}
	unsafe := checkpointRecord(governor.CheckpointResult{Deferred: true}, nil)
	if !unsafe.failed() {
		t.Fatal("native deferral excused uncertain physical cleanup")
	}
}
