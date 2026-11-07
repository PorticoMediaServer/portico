package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Existing native receipt populations and actual complete Capture; virtual
// clock/resource forecasts only. The declared copy reserve is structural
// fixture input, never allocated live bytes or an installed production budget.
func scanLifecycleDispatchFixture(t *testing.T) (*Writer, *executor, *governor.Authority, map[string]ScanReceiptRequest, effectivePlan) {
	t.Helper()
	e, requests, _ := scanLifecycleProtocolFixture(t)
	var seq int64
	var name, path string
	if err := e.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	p := effectiveFixturePlan()
	p.need[governor.MemoryBytes] = 8 << 20
	history := p
	history.steps = nil
	history.scanLifecycleMemory = commandMemoryBudget{4 << 20, 4 << 20}
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	for _, def := range scanLifecycleHistoryCommandDefinitions() {
		history.steps = append(history.steps, statementStep{def.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
		defs = append(defs, def)
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, e.config, defs, "scan_lifecycle_dispatch_test.go", "scan_lifecycle_protocol_test.go", "scan_receipts_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	p.registration, history.registration = golden.Receipt(), golden.Receipt()
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = p.need
	}
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 2, Now: e.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	r, err := newEffectiveRuntime(e, path, g, a, wal, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := composeScanLifecycleHistory(t.Context(), r, history, history); err != nil {
		t.Fatal(err)
	}
	return &Writer{effective: r}, e, a, requests, history
}

func TestScanLifecycleDispatchSavedHistoryAndAtomicAck(t *testing.T) {
	w, e, a, requests, _ := scanLifecycleDispatchFixture(t)
	// Retired library, old lease and changed current root never filter a saved
	// scalar result. Current opaque principal and native library scope still do.
	if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_libraries SET retired=1 WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		kind CommandID
	}{{"hit", InventoryCreate}, {"replay", InventoryClaim}} {
		q := requests[tc.name]
		cap := inventoryTestDisclosure(t, w, q)
		out, ack, err := w.effective.runScanLifecycleHistory(t.Context(), w, cap, q, tc.kind, false)
		if err != nil || out.Outcome.State != Applied || !out.Outcome.Replayed || out.Outcome.Reference != q.Receipt ||
			out.Value.Original.RunID != q.RunID || out.Value.AppliedMS != 100 || out.Digest == (ResultDigest{}) || !reflect.DeepEqual(ack, AckResult{}) {
			t.Fatal(tc, out, ack, err)
		}
		v := out.Outcome.Evidence
		if v == nil || v.Purpose != tc.kind || v.Class != governor.ScanMinimum || v.Durability != Normal || v.Commit != CommitSucceeded || v.PhysicalReturnedAt == nil {
			t.Fatal("missing actual same-purpose physical evidence", v)
		}
		if tc.kind == InventoryClaim && (out.Value.LeaseOwner != 17 || out.Value.LeaseGeneration != 4 || out.Value.RootCursor != "cookie:32" || out.Value.AcknowledgedSequence != 1) {
			t.Fatal("saved claim was replaced by current head", out)
		}
		// No returned original backing can edit the native immutable receipt.
		out.Value.Original.ResolvedRoot[0] = '!'
		_, ack, err = w.effective.runScanLifecycleHistory(t.Context(), w, cap, q, tc.kind, true)
		if err != nil || ack.AcknowledgedThrough != q.Receipt.Identity.Sequence || ack.Evidence == nil {
			t.Fatal(tc, ack, err)
		}
		var floor, count, anchor int64
		if err := e.db.QueryRowContext(t.Context(), `SELECT acknowledged_command_sequence,length(create_request_digest) FROM inventory_runs WHERE job_id=?`, q.RunID).Scan(&floor, &anchor); err != nil {
			t.Fatal(err)
		}
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts WHERE run_id=?`, q.RunID).Scan(&count); err != nil || floor != q.Receipt.Identity.Sequence || count != 0 || anchor != 32 {
			t.Fatal("ACK did not retain owner and atomically drain scalar header", floor, count, anchor, err)
		}
		out, _, err = w.effective.runScanLifecycleHistory(t.Context(), w, cap, q, tc.kind, false)
		if err != nil || out.Outcome.State != ResultRetired || !reflect.DeepEqual(out.Value, scanRunLifecycleResult{}) || out.Digest != (ResultDigest{}) {
			t.Fatal("floor resurrected Applied", out, err)
		}
	}
	if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("history/ACK did not physically retire")
	}
}

func TestScanLifecycleDispatchClosedScopePlansAndMiss(t *testing.T) {
	w, e, a, requests, plan := scanLifecycleDispatchFixture(t)
	r := w.effective
	q := requests["hit"]
	cap := inventoryTestDisclosure(t, w, q)
	for _, tc := range []struct {
		q    ScanReceiptRequest
		c    *ScanDisclosure
		k    CommandID
		want error
	}{
		{q, nil, InventoryCreate, ErrDisclosureDenied},
		{q, cap, InventoryAccept, ErrInvalid},
		{q, cap, InventoryClaim, ErrConflict},
		{requests["missing"], inventoryTestDisclosure(t, w, requests["missing"]), InventoryCreate, ErrResolveRequired},
	} {
		out, ack, err := r.runScanLifecycleHistory(t.Context(), w, tc.c, tc.q, tc.k, false)
		if !errors.Is(err, tc.want) || !reflect.DeepEqual(out, scanLifecycleCommandResult{}) || !reflect.DeepEqual(ack, AckResult{}) {
			t.Fatal(tc, out, ack, err)
		}
	}
	// Absent original owner is unavailable, never creation permission or a new
	// source/run/lease. Native job scope is independently required first.
	missing := q
	missing.RunID = "no-run"
	if out, _, err := r.runScanLifecycleHistory(t.Context(), w, inventoryTestDisclosure(t, w, missing), missing, InventoryCreate, false); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, scanLifecycleCommandResult{}) {
		t.Fatal(out, err)
	}
	oldPlans := r.lifecyclePlans
	r.lifecyclePlans = [2]*effectivePlan{}
	registry := r.registry
	bad := plan
	bad.registration = nil
	if err := composeScanLifecycleHistory(t.Context(), r, bad, plan); !errors.Is(err, stmt.ErrUnqualified) || r.registry != registry || r.lifecyclePlans != ([2]*effectivePlan{}) {
		t.Fatal("unproved registry installed", err)
	}
	bad = plan
	bad.scanLifecycleMemory = commandMemoryBudget{scanByteFixedWorkspace - 1, uint64(bad.need[governor.MemoryBytes]) - scanByteFixedWorkspace + 1}
	if err := composeScanLifecycleHistory(t.Context(), r, bad, plan); err == nil || r.registry != registry || r.lifecyclePlans != ([2]*effectivePlan{}) {
		t.Fatal("underfunded fixed byte-preflight workspace installed", err)
	}
	r.lifecyclePlans = oldPlans
	if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("invalid lifecycle attempt retained authority")
	}
}

func TestScanLifecycleDispatchLateCancellationRedactsAndAckRollsBack(t *testing.T) {
	// Two actual boundaries, not a combinatorial sweep: final receipt close
	// before terminal, and native COMMIT completed before caller cancellation.
	for _, boundary := range []string{"receipt_close", "after_commit"} {
		for _, ack := range []bool{false, true} {
			t.Run(boundary+map[bool]string{false: "/resolve", true: "/ack"}[ack], func(t *testing.T) {
				w, e, a, requests, _ := scanLifecycleDispatchFixture(t)
				q := requests["hit"]
				cap := inventoryTestDisclosure(t, w, q)
				if err := e.db.Close(); err != nil {
					t.Fatal(err)
				}
				connector, err := sqliteconn.Connector(w.effective.path, e.config, sqliteconn.ExistingFile)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if boundary == "after_commit" {
					installFaultPool(e, &faultConnector{Connector: connector, afterNativeCommit: cancel})
				} else {
					e.openPool(connector, func(c driver.Conn) driver.Conn {
						return &inventoryReceiptCloseConn{writerDriverConn: c.(writerDriverConn), hook: cancel}
					})
				}
				primeEffectiveFixturePool(t, e)
				out, floor, err := w.effective.runScanLifecycleHistory(ctx, w, cap, q, InventoryCreate, ack)
				if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, scanLifecycleCommandResult{}) || !reflect.DeepEqual(floor, AckResult{}) {
					t.Fatal("ended lifetime published payload/floor", out, floor, err)
				}
				if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
					t.Fatal("late cancellation lost physical custody")
				}
				var actualFloor, headers int64
				if err := e.db.QueryRowContext(t.Context(), `SELECT acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`, q.RunID).Scan(&actualFloor); err != nil {
					t.Fatal(err)
				}
				if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts WHERE run_id=?`, q.RunID).Scan(&headers); err != nil {
					t.Fatal(err)
				}
				wantFloor := int64(0)
				if ack && boundary == "after_commit" {
					wantFloor = 1
				}
				if actualFloor != wantFloor || headers != 1-wantFloor {
					t.Fatal("redaction invented rollback or partial ACK", actualFloor, headers, wantFloor)
				}
			})
		}
	}
}
