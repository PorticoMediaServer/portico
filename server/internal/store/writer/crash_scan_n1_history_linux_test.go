package writer

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Child-bearing N1 history after restart. The accepting receipt keeps its
// exact cohort while ACK advances only the owner floor and a retired Resolve
// serves no drained payload. These are process-crash cells for the public
// ScanDriver.ResolveScan/AckScan rebind, not another accepting-body matrix.
func n1HistoryReceipt(source n1DriverCrashSource, reference ReceiptReference) ScanReceiptRequest {
	return ScanReceiptRequest{RunID: source.Input.RunID, LibraryID: source.Input.LibraryID,
		LibraryKey: source.Input.LibraryKey, Receipt: reference}
}

func n1HistoryFloor(t *testing.T, ctx context.Context, reader inventoryCrashReader, runID string) (ack, last int64) {
	t.Helper()
	if err := reader.QueryRowContext(ctx, `SELECT acknowledged_command_sequence,last_command_sequence FROM inventory_runs WHERE job_id=?`, runID).Scan(&ack, &last); err != nil {
		t.Fatal(err)
	}
	return ack, last
}

func n1HistoryEvidence(t *testing.T, e *ExecutionEvidence) {
	t.Helper()
	if e == nil || e.Purpose != InventoryAccept || e.Class != governor.ScanMinimum || e.Durability != Normal ||
		e.Commit != CommitSucceeded || e.CommitAttemptedAt == nil || e.CommitReturnedAt == nil || e.PhysicalReturnedAt == nil || e.ReturnedAt.IsZero() {
		t.Fatal("missing ACK frame evidence", e)
	}
}

// crashN1HistoryFixture commits one real child-bearing N1 receipt, then stops
// at one ACK or post-ACK Resolve boundary. Expected state is the frozen input
// and raw original receipt, never a writer result.
func crashN1HistoryFixture(t *testing.T, witness *crashWitness) {
	t.Helper()
	d, preparation, command, original := n1ComposedDriverPrepared(t)
	e, path := d.opening.executor, d.opening.binding.path
	clock := e.now()
	c, ok := command.(*inventoryAcceptCommand)
	if !ok || c == nil || !c.valid {
		t.Fatal("N1 history fixture lost its compiled original command")
	}
	source := n1DriverCrashSource{Input: n1DriverInputFrom(c)}
	source.Input.ClockMS = clock.UnixMilli()
	source.Baseline = n1DriverCaptureBaseline(t, t.Context(), e.db, source.Input)
	initial, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	source.Initial = initial
	witness.Path, witness.Reference, witness.N1Driver = path, original.Receipt, &source
	// Commit the one real child beyond any tested ACK boundary.
	out, err := preparation.Accept(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := checkN1DriverCommitted(t.Context(), e.db, source)
	if err != nil {
		t.Fatal(err)
	}
	source.Journal = &journal
	want, err := n1DriverExpectedResult(source, journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverResult(want, out); err != nil {
		t.Fatal(err)
	}
	if err = preparation.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Publish the saved result once so AckScan has the same-original handle.
	if _, err = d.ResolveScan(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	if witness.Case.Operation == "retired" {
		if _, err = d.AckScan(t.Context(), original); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]string{}
	for _, entry := range d.writer.effective.registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	seen := map[string]bool{}
	armed := false
	installCrashDriverAt(t, e, path, witness, clock, func(query string) {
		if !armed {
			return
		}
		id, ok := ids[strings.TrimSpace(query)]
		if !ok || seen[id] {
			return
		}
		seen[id] = true
		phase := "native_" + id
		source.Trace = append(source.Trace, phase)
		crashPoint(t, witness, phase)
	})
	armed = true
	if witness.Case.Operation == "ack" {
		ack, err := d.AckScan(t.Context(), original)
		armed = false
		if witness.Case.CommitFault != "" {
			if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || ack != (AckResult{}) || !e.ambiguous || !d.writer.effective.fenced {
				t.Fatal("N1 ACK uncertain COMMIT reported success or escaped quarantine", ack, err)
			}
			witness.Unknown = true
			crashPoint(t, witness, "uncertain_return")
			t.Fatal("N1 ACK uncertain boundary was never reached", witness.Case)
		}
		if err != nil {
			t.Fatal("actual N1 ACK refused", err)
		}
		if ack.AcknowledgedThrough != source.Input.IdentitySequence {
			t.Fatal("N1 ACK floor", ack)
		}
		n1HistoryEvidence(t, ack.Evidence)
	} else {
		resolved, err := d.ResolveScan(t.Context(), original)
		armed = false
		if err != nil {
			t.Fatal("retired N1 Resolve refused", err)
		}
		if resolved.Outcome.State != ResultRetired || resolved.Outcome.Replayed || resolved.Digest != (ResultDigest{}) ||
			len(resolved.Members) != 0 || resolved.RunID != "" || resolved.ResultCount != 0 {
			t.Fatal("retired N1 Resolve surfaced a drained payload", resolved)
		}
	}
	if witness.Case.Boundary == "trace" {
		return
	}
	crashPoint(t, witness, "api_return")
	t.Fatal("N1 history boundary was never reached", witness.Case)
}

// verifyN1HistoryCrash rebuilds the SAME public composed driver on the killed
// child's canonical file, resolves the original, ACKs it twice and proves the
// second ACK is write-free.
func verifyN1HistoryCrash(t *testing.T, witness crashWitness) {
	t.Helper()
	source := witness.N1Driver
	if source == nil || witness.Case.Domain != "wc_n1_history" || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("N1 history witness lost its original source")
	}
	if err := checkN1DriverReference(source.Input, witness.Reference); err != nil {
		t.Fatal(err)
	}
	d := n1ComposedDriverOnExisting(t, witness.Path)
	e := d.opening.executor
	ack, last := n1HistoryFloor(t, t.Context(), e.db, source.Input.RunID)
	if last != source.Input.IdentitySequence {
		t.Fatal("restart changed the accepted receipt sequence", last)
	}
	// The frozen Accept baseline predates ACK. Only the acknowledged floor is
	// allowed to differ when the tested ACK committed before the crash.
	committed := *source
	if ack == source.Input.IdentitySequence {
		committed.Baseline.RunAck = ack
	}
	journal, err := checkN1DriverCommitted(t.Context(), e.db, committed)
	if err != nil {
		t.Fatal(err)
	}
	if source.Journal != nil {
		if err := compareInventoryCrashJournal(*source.Journal, journal); err != nil {
			t.Fatal(err)
		}
	}
	req := n1HistoryReceipt(*source, witness.Reference)
	got, err := d.ResolveScan(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if ack == source.Input.IdentitySequence {
		want := InventoryResult{Outcome: ReceiptOutcome{State: ResultRetired, Reference: witness.Reference}}
		got.Outcome.Evidence = nil
		if !reflect.DeepEqual(want, got) {
			t.Fatal("restart retired Resolve changed", got, want)
		}
	} else {
		want, err := n1DriverExpectedResult(*source, journal)
		if err != nil {
			t.Fatal(err)
		}
		want.Outcome.Replayed = true
		if got.Outcome.Evidence == nil {
			t.Fatal("restart replay lacked actual frame evidence")
		}
		got.Outcome.Evidence = nil
		if !reflect.DeepEqual(want, got) {
			t.Fatal("restart replay changed the saved N1 payload", got, want)
		}
	}
	first, err := d.AckScan(t.Context(), req)
	if err != nil || first.AcknowledgedThrough != source.Input.IdentitySequence {
		t.Fatal("restart ACK", first, err)
	}
	n1HistoryEvidence(t, first.Evidence)
	settled, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.AckScan(t.Context(), req)
	if err != nil || second.AcknowledgedThrough != first.AcknowledgedThrough {
		t.Fatal("repeated restart ACK", second, err)
	}
	n1HistoryEvidence(t, second.Evidence)
	after, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(settled, after); err != nil {
		t.Fatal("repeated ACK wrote state", err)
	}
	retired, err := d.ResolveScan(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if retired.Outcome.State != ResultRetired || retired.Digest != (ResultDigest{}) || len(retired.Members) != 0 {
		t.Fatal("ACKed original still served its cohort", retired)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// runN1HistoryTrace discovers one realistic boundary per executed statement in
// this fixture, for each tested operation, without an exhaustive repeat.
func runN1HistoryTrace(t *testing.T) (ackPhases, retiredPhases []string) {
	t.Helper()
	d, preparation, command, original := n1ComposedDriverPrepared(t)
	e, path := d.opening.executor, d.opening.binding.path
	clock := e.now()
	if _, err := preparation.Accept(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if err := preparation.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ResolveScan(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, entry := range d.writer.effective.registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	witness := crashWitness{Case: crashCase{Domain: "wc_n1_history", Operation: "ack", Boundary: "trace"}}
	trace := func(op string, run func() error) []string {
		seen := map[string]bool{}
		armed := false
		installCrashDriverAt(t, e, path, &witness, clock, func(query string) {
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
	ackPhases = trace("ack", func() error { _, err := d.AckScan(t.Context(), original); return err })
	retiredPhases = trace("retired", func() error {
		_, err := d.ResolveScan(t.Context(), original)
		return err
	})
	closeN1DriverCrashFixture(t, d)
	return ackPhases, retiredPhases
}

func TestCrashReplayN1ChildBearingHistory(t *testing.T) {
	ackPhases, retiredPhases := runN1HistoryTrace(t)
	ackPhases = append([]string{"before_begin", "after_begin"}, ackPhases...)
	ackPhases = append(ackPhases, "before_commit", "after_commit", "physical_return", "api_return")
	for _, phase := range ackPhases {
		t.Run("ack/"+phase, func(t *testing.T) {
			verifyN1HistoryCrash(t, runCrashChild(t, crashCase{"wc_n1_history", "ack", phase, ""}))
		})
	}
	for _, fault := range []string{"before", "after"} {
		t.Run("ack/uncertain_return/"+fault, func(t *testing.T) {
			verifyN1HistoryCrash(t, runCrashChild(t, crashCase{"wc_n1_history", "ack", "uncertain_return", fault}))
		})
	}
	for _, phase := range append(retiredPhases, "api_return") {
		t.Run("retired/"+phase, func(t *testing.T) {
			verifyN1HistoryCrash(t, runCrashChild(t, crashCase{"wc_n1_history", "retired", phase, ""}))
		})
	}
}

// The parent-only oracle must reject a changed child cohort even when the
// header and floor still look valid.
func TestN1HistoryOracleRejectsCohortChange(t *testing.T) {
	d, preparation, command, original := n1ComposedDriverPrepared(t)
	e := d.opening.executor
	c := command.(*inventoryAcceptCommand)
	source := n1DriverCrashSource{Input: n1DriverInputFrom(c)}
	source.Input.ClockMS = e.now().UnixMilli()
	source.Baseline = n1DriverCaptureBaseline(t, t.Context(), e.db, source.Input)
	initial, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	source.Initial = initial
	if _, err := preparation.Accept(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if err := preparation.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ResolveScan(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AckScan(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	source.Baseline.RunAck = source.Input.IdentitySequence
	if _, err := checkN1DriverCommitted(t.Context(), e.db, source); err != nil {
		t.Fatal(err)
	}
	journal, err := readN1DriverJournal(t.Context(), e.db, source.Input)
	if err != nil {
		t.Fatal(err)
	}
	if len(journal.Children) != 1 {
		t.Fatal("control fixture did not retain one child")
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for name, query := range map[string]string{
		"child_object":   `UPDATE scan_command_results SET object_id='CORRUPTED-OBJECT' WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
		"filename_title": `UPDATE catalog_entities SET title='CORRUPTED-TITLE' WHERE id=?`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := conn.ExecContext(t.Context(), "SAVEPOINT n1_history_oracle"); err != nil {
				t.Fatal(err)
			}
			var result sql.Result
			switch name {
			case "child_object":
				result, err = conn.ExecContext(t.Context(), query, source.Input.RunID, source.Input.IdentityIncarnation, source.Input.IdentitySequence)
			default:
				result, err = conn.ExecContext(t.Context(), query, journal.Children[0].SubjectID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if rows, err := result.RowsAffected(); err != nil || rows != 1 {
				t.Fatal("control did not mutate one raw row", rows, err)
			}
			if _, err := checkN1DriverCommitted(t.Context(), conn, source); err == nil {
				t.Fatal("changed child cohort escaped the raw oracle", name)
			}
			if _, err := conn.ExecContext(t.Context(), "ROLLBACK TO n1_history_oracle"); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(t.Context(), "RELEASE n1_history_oracle"); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
