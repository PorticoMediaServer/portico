package writer

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Post-crash public scan lifecycle ACK rebind. A committed Create receipt is
// ACKed once per real boundary, then the SAME original is resolved and ACKed
// again through a rebuilt public ScanDriver. The saved receipt is not lost and
// the repeated ACK writes nothing.
func scanAckFloor(t *testing.T, ctx context.Context, reader inventoryCrashReader, runID string) (ack, last int64) {
	t.Helper()
	if err := reader.QueryRowContext(ctx, `SELECT acknowledged_command_sequence,last_command_sequence FROM inventory_runs WHERE job_id=?`, runID).Scan(&ack, &last); err != nil {
		t.Fatal(err)
	}
	return ack, last
}

func crashScanAckFixture(t *testing.T, witness *crashWitness) {
	t.Helper()
	f := scanIssuerRuntime(t)
	d := f.driver
	clock := d.opening.executor.now()
	create, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	reference, err := create.Reference()
	if err != nil {
		t.Fatal(err)
	}
	result, err := create.Apply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := result.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	saved.Outcome.Evidence = nil
	witness.Path, witness.ScanRun, witness.ScanSaved = d.opening.binding.path, &reference, &saved
	witness.ScanClockMS = clock.UnixMilli()
	ids := map[string]string{}
	for _, entry := range d.writer.effective.registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	seen := map[string]bool{}
	armed := false
	installCrashDriverAt(t, d.opening.executor, witness.Path, witness, clock, func(query string) {
		if !armed {
			return
		}
		id, ok := ids[strings.TrimSpace(query)]
		if !ok || seen[id] {
			return
		}
		seen[id] = true
		phase := "native_" + id
		crashPoint(t, witness, phase)
	})
	switch witness.Case.Operation {
	case "ack":
		armed = true
		ack, err := create.Ack(t.Context())
		armed = false
		if witness.Case.CommitFault != "" {
			if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || ack.AcknowledgedThrough != 0 || !d.opening.executor.ambiguous || !d.writer.effective.fenced {
				t.Fatal("scan ACK uncertain COMMIT reported success or escaped quarantine", ack, err)
			}
			witness.Unknown = true
			crashPoint(t, witness, "uncertain_return")
			t.Fatal("scan ACK uncertain boundary was never reached", witness.Case)
		}
		if err != nil {
			t.Fatal("actual scan ACK refused", err)
		}
		if ack.AcknowledgedThrough != reference.Scan.Receipt.Identity.Sequence {
			t.Fatal("scan ACK floor", ack)
		}
	case "retired":
		if _, err := create.Ack(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := create.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		op, err := d.OpenRun(t.Context(), reference)
		if err != nil {
			t.Fatal(err)
		}
		armed = true
		retired, err := op.Resolve(t.Context())
		armed = false
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := retired.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := retired.Close(); err != nil {
			t.Fatal(err)
		}
		if snapshot.Outcome.State != ResultRetired || snapshot.Digest != (ResultDigest{}) || snapshot.RunID != "" || snapshot.LeaseOwner != 0 {
			t.Fatal("retired scan Resolve surfaced saved values", snapshot)
		}
		if err := op.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unsupported scan ACK operation", witness.Case)
	}
	if witness.Case.Boundary == "trace" {
		return
	}
	crashPoint(t, witness, "api_return")
	t.Fatal("scan ACK boundary was never reached", witness.Case)
}

// reopenScanAck rebuilds the public issuer driver on the killed child's file
// without seeding or replaying initialization SQL.
func reopenScanAck(t *testing.T, path string) *scanDriverFixture {
	t.Helper()
	owner := scanOpeningOwner(t, path)
	opening := openScanFixture(t, path, owner)
	f := scanDriverFixtureOnOpening(t, opening, owner, scanRunCreateEffects{}, []scanDriverStatementFixture{scanIssuerRuntimeFixture}, false)
	*f.clock = crashClock
	_, plan, ready := scanIssuerRuntimePlans(t, f)
	if err := configureScanIssuer(t.Context(), f.driver, plan, plan, ready, time.Second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeScanAckFixture(t, f) })
	return f
}

func closeScanAckFixture(t *testing.T, f *scanDriverFixture) {
	t.Helper()
	if err := f.driver.Close(context.Background()); err != nil {
		t.Error(err)
	}
	if err := f.driver.opening.close(); err != nil && !errors.Is(err, ErrBusy) {
		t.Error(err)
	}
	if err := f.driver.owner.Close(); err != nil {
		t.Error(err)
	}
}

func verifyScanAckCrash(t *testing.T, witness crashWitness) {
	t.Helper()
	if witness.ScanRun == nil || witness.ScanSaved == nil || witness.Case.Domain != "scan_issuer_ack" ||
		witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("scan ACK witness lost its original reference")
	}
	f := reopenScanAck(t, witness.Path)
	*f.clock = time.UnixMilli(witness.ScanClockMS)
	d := f.driver
	reference := *witness.ScanRun
	floor, last := scanAckFloor(t, t.Context(), d.opening.executor.db, reference.Scan.RunID)
	if last < reference.Scan.Receipt.Identity.Sequence {
		t.Fatal("restart lost the committed scan receipt", last, reference.Scan.Receipt.Identity.Sequence)
	}
	op, err := d.OpenRun(t.Context(), reference)
	if err != nil {
		t.Fatal(err)
	}
	result, err := op.Resolve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got, err := result.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if floor >= reference.Scan.Receipt.Identity.Sequence {
		want := ScanRunSnapshot{Outcome: ReceiptOutcome{State: ResultRetired, Reference: reference.Scan.Receipt}}
		got.Outcome.Evidence = nil
		if !reflect.DeepEqual(want, got) {
			t.Fatal("restart retired scan Resolve changed", got, want)
		}
	} else {
		want := *witness.ScanSaved
		want.Outcome.Replayed = true
		if got.Outcome.Evidence == nil {
			t.Fatal("scan replay lacked actual frame evidence")
		}
		got.Outcome.Evidence = nil
		if !reflect.DeepEqual(want, got) {
			t.Fatal("restart scan replay changed the saved receipt", got, want)
		}
	}
	if _, err = op.Ack(t.Context()); err != nil {
		t.Fatal("restart scan ACK", err)
	}
	if err = op.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	settled, err := n1DriverCheckpoint(t.Context(), d.opening.executor.db)
	if err != nil {
		t.Fatal(err)
	}
	// The same original ACKs idempotently after the floor exists.
	op, err = d.OpenRun(t.Context(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = op.Ack(t.Context()); err != nil {
		t.Fatal("repeated restart scan ACK", err)
	}
	if err = op.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := n1DriverCheckpoint(t.Context(), d.opening.executor.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(settled, after); err != nil {
		t.Fatal("repeated scan ACK wrote state", err)
	}
	op, err = d.OpenRun(t.Context(), reference)
	if err != nil {
		t.Fatal(err)
	}
	result, err = op.Resolve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	retired, err := result.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if retired.Outcome.State != ResultRetired || retired.Digest != (ResultDigest{}) || retired.RunID != "" || retired.LeaseOwner != 0 {
		t.Fatal("ACKed scan original still served saved values", retired)
	}
	if err = op.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func runScanAckTrace(t *testing.T) (ackPhases, retiredPhases []string) {
	t.Helper()
	f := scanIssuerRuntime(t)
	d := f.driver
	clock := d.opening.executor.now()
	create, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	reference, err := create.Reference()
	if err != nil {
		t.Fatal(err)
	}
	result, err := create.Apply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, entry := range d.writer.effective.registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	witness := crashWitness{Case: crashCase{Domain: "scan_issuer_ack", Operation: "ack", Boundary: "trace"}}
	trace := func(run func() error) []string {
		seen := map[string]bool{}
		armed := false
		installCrashDriverAt(t, d.opening.executor, d.opening.binding.path, &witness, clock, func(query string) {
			if !armed {
				return
			}
			if id, ok := ids[strings.TrimSpace(query)]; ok && !seen[id] {
				seen[id] = true
			}
		})
		armed = true
		err := run()
		armed = false
		if err != nil {
			t.Fatal(err)
		}
		phases := make([]string, 0, len(seen))
		for id := range seen {
			phases = append(phases, "native_"+id)
		}
		return phases
	}
	ackPhases = trace(func() error { _, err := create.Ack(t.Context()); return err })
	if err := create.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	retiredPhases = trace(func() error {
		op, err := d.OpenRun(t.Context(), reference)
		if err != nil {
			return err
		}
		res, err := op.Resolve(t.Context())
		if err != nil {
			return err
		}
		if err := res.Close(); err != nil {
			return err
		}
		return op.Close(t.Context())
	})
	closeScanAckFixture(t, f)
	return ackPhases, retiredPhases
}

func TestCrashReplayScanAckAfterRestart(t *testing.T) {
	ackPhases, retiredPhases := runScanAckTrace(t)
	ackPhases = append([]string{"before_begin", "after_begin"}, ackPhases...)
	ackPhases = append(ackPhases, "before_commit", "after_commit", "physical_return", "api_return")
	for _, phase := range ackPhases {
		t.Run("ack/"+phase, func(t *testing.T) {
			verifyScanAckCrash(t, runCrashChild(t, crashCase{"scan_issuer_ack", "ack", phase, ""}))
		})
	}
	for _, fault := range []string{"before", "after"} {
		t.Run("ack/uncertain_return/"+fault, func(t *testing.T) {
			verifyScanAckCrash(t, runCrashChild(t, crashCase{"scan_issuer_ack", "ack", "uncertain_return", fault}))
		})
	}
	for _, phase := range append(retiredPhases, "api_return") {
		t.Run("retired/"+phase, func(t *testing.T) {
			verifyScanAckCrash(t, runCrashChild(t, crashCase{"scan_issuer_ack", "retired", phase, ""}))
		})
	}
}
