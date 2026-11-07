package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Native receipt-reader populations only, not production creation, source
// observation, permission, exclusion or a qualified command/resource recipe.
func scanLifecycleProtocolFixture(t *testing.T) (*executor, map[string]ScanReceiptRequest, *stmt.Registry) {
	t.Helper()
	w, _, _ := testExecutor(t)
	populations := seedScanReceiptCohorts(t, w)
	queries := make(map[string]ScanReceiptRequest)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range populations {
		q := ScanReceiptRequest{RunID: "scan-" + p.name, LibraryID: 7, LibraryKey: "scan-library"}
		q.Receipt.Identity.Sequence = 1
		copy(q.Receipt.Identity.OperationID[:], p.operation)
		v := scanRunLifecycleResult{Original: scanRunOriginalScope{RunID: q.RunID, LibraryID: 7, LibraryKey: "scan-library",
			SourceID: "scan-source-" + p.name, SourceGeneration: 1, SourceIncarnation: "source-incarnation",
			RootIdentity: "root", ResolvedRoot: []byte("/media/" + p.name), PolicyRevision: 1, ScanMode: "full"},
			AcceptedRunRevision: 1, AppliedMS: 100, RootIdentity: "observed-directory", RootRevision: "observed-revision"}
		if p.name == "replay" {
			var operation, digest []byte
			if err := w.db.QueryRowContext(t.Context(), `SELECT create_operation_id,create_request_digest FROM inventory_runs WHERE job_id=?`, q.RunID).Scan(&operation, &digest); err != nil {
				t.Fatal(err)
			}
			copy(v.Original.CreateOperationID[:], operation)
			copy(v.Original.CreateRequestDigest[:], digest)
		} else {
			v.Original.CreateOperationID = q.Receipt.Identity.OperationID
			copy(v.Original.CreateRequestDigest[:], p.digest)
			exec(`UPDATE inventory_runs SET create_operation_id=?,create_request_digest=? WHERE job_id=?`, p.operation, p.digest, q.RunID)
		}
		queries[p.name] = q
		if p.last == 0 {
			continue
		}
		exec(`DELETE FROM scan_command_results WHERE run_id=?`, q.RunID)
		kind := "inventory_create"
		if p.name == "replay" {
			kind, q.Receipt.Identity.Sequence = "inventory_claim", 2
			v.AcceptedRunRevision, v.LeaseOwner, v.LeaseGeneration, v.LeaseExpiresMS = 2, 17, 4, 5000
			v.AcknowledgedSequence, v.Discovered, v.Processed = 1, 19, 23
			v.RootCursor, v.RootPages = "cookie:32", 2
			exec(`UPDATE inventory_runs SET job_revision=2,last_command_sequence=2,acknowledged_command_sequence=1 WHERE job_id=?`, q.RunID)
			queries[p.name] = q
		}
		actualRequest := RequestDigest{}
		copy(actualRequest[:], p.digest)
		var digest ResultDigest
		var err error
		if kind == "inventory_create" {
			digest, err = inventoryCreateResultDigest(q.Receipt.Identity, actualRequest, inventoryCreateResult{v})
		} else {
			digest, err = inventoryClaimResultDigest(q.Receipt.Identity, actualRequest, inventoryClaimResult{v})
		}
		if err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE scan_command_receipts SET command_sequence=?,command_kind=?,accepted_revision=?,
source_revision=NULL,next_cursor='',result_count=0,result_digest=?,
result_lease_owner=?,result_lease_generation=?,result_lease_expires_ms=?,result_acknowledged_sequence=?,
result_discovered=?,result_processed=?,result_root_identity=?,result_root_revision=?,result_root_cursor=?,result_root_pages=?,result_root_skipped=?
WHERE run_id=?`, q.Receipt.Identity.Sequence, kind, v.AcceptedRunRevision, digest[:], v.LeaseOwner, v.LeaseGeneration, v.LeaseExpiresMS,
			v.AcknowledgedSequence, v.Discovered, v.Processed, v.RootIdentity, v.RootRevision, v.RootCursor, v.RootPages, v.RootSkipped, q.RunID)
	}
	defs := append(scanRunLifecycleHistoryDefinitions(), scanHistoryByteDefinitions()...)
	for _, def := range scanReceiptDefinitions() {
		switch def.ID {
		case scanReceiptLookup, scanReceiptAck, scanReceiptReclaimKey, scanReceiptReclaimHeader:
			defs = append(defs, def)
		}
	}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "scan_lifecycle_protocol_test.go", "scan_receipts_test.go")
	return w, queries, registry
}

func scanLifecycleProtocolTurn(t *testing.T, w *executor, registry *stmt.Registry, kind CommandID) *commandTurn {
	t.Helper()
	return scanLifecycleExecutionTurn(t, w, registry, kind, commandMemoryBudget{4 << 20, 4 << 20})
}

func TestScanLifecycleHistoryAndBoundedAck(t *testing.T) {
	w, requests, registry := scanLifecycleProtocolFixture(t)
	for _, tc := range []struct {
		name string
		kind CommandID
	}{{"hit", InventoryCreate}, {"replay", InventoryClaim}, {"stale", InventoryCreate}} {
		t.Run(tc.name, func(t *testing.T) {
			q := requests[tc.name]
			turn := scanLifecycleProtocolTurn(t, w, registry, tc.kind)
			lookup, err := lookupScanRunLifecycle(t.Context(), turn, q, tc.kind)
			if err != nil || lookup.decision != receiptReplay || lookup.result.Original.RunID != q.RunID || lookup.result.AppliedMS != 100 {
				t.Fatal(lookup, err)
			}
			if tc.kind == InventoryClaim && (lookup.result.LeaseOwner != 17 || lookup.result.LeaseGeneration != 4 || lookup.result.RootCursor != "cookie:32" || lookup.result.AcknowledgedSequence != 1) {
				t.Fatal("reconstructed current facts instead of saved claim", lookup)
			}
			floor, err := acknowledgeScanRunLifecycle(t.Context(), turn, q, tc.kind)
			if err != nil || floor != q.Receipt.Identity.Sequence {
				t.Fatal(floor, err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			turn = scanLifecycleProtocolTurn(t, w, registry, tc.kind)
			retired, err := lookupScanRunLifecycle(t.Context(), turn, q, tc.kind)
			if err != nil || retired.decision != receiptRetired || !reflect.DeepEqual(retired.result, scanRunLifecycleResult{}) {
				t.Fatal("ACK resurrected saved scalar result", retired, err)
			}
			if floor, err := acknowledgeScanRunLifecycle(t.Context(), turn, q, tc.kind); err != nil || floor != q.Receipt.Identity.Sequence {
				t.Fatal("ACK retry", floor, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

func TestScanLifecycleHistoryRefusesIncompleteAndMissingOwner(t *testing.T) {
	w, requests, registry := scanLifecycleProtocolFixture(t)
	for _, tc := range []struct {
		name string
		edit func(*ScanReceiptRequest)
		want error
	}{{"conflict", nil, ErrConflict}, {"missing", nil, ErrResolveRequired},
		{"hit", func(q *ScanReceiptRequest) { q.RunID = "no-old-owner" }, ErrOwnerUnavailable},
		{"hit", func(q *ScanReceiptRequest) { q.Receipt.RequestDigest[0] = 1 }, ErrConflict}} {
		turn := scanLifecycleProtocolTurn(t, w, registry, InventoryCreate)
		q := requests[tc.name]
		if tc.edit != nil {
			tc.edit(&q)
		}
		got, err := lookupScanRunLifecycle(t.Context(), turn, q, InventoryCreate)
		if !errors.Is(err, tc.want) || !reflect.DeepEqual(got, scanLifecycleReceiptLookup{}) {
			t.Fatal("invalid history became fresh/payload", got, err)
		}
		turn.transaction.rollback(err)
	}
	turn := scanLifecycleProtocolTurn(t, w, registry, InventoryCreate)
	q := requests["hit"]
	if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, `INSERT INTO scan_command_results
SELECT run_id,command_incarnation,command_sequence,0,0,0,'unexpected-child',7,31,41,1 FROM scan_command_receipts WHERE run_id=?`, q.RunID); err != nil {
		t.Fatal(err)
	}
	got, err := lookupScanRunLifecycle(t.Context(), turn, q, InventoryCreate)
	if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(got, scanLifecycleReceiptLookup{}) {
		t.Fatal("zero-member lifecycle ignored an actual child", got, err)
	}
	turn.transaction.rollback(err)
}

func TestScanLifecycleLateCancellationClearsProof(t *testing.T) {
	w, requests, registry := scanLifecycleProtocolFixture(t)
	var seq int64
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"caller", "turn"} {
		t.Run(mode, func(t *testing.T) {
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			base, err := sqliteconn.Connector(path, w.config)
			if err != nil {
				t.Fatal(err)
			}
			caller, cancel := context.WithCancel(t.Context())
			defer cancel()
			cancelRead := cancel
			w.openPool(base, func(conn driver.Conn) driver.Conn {
				return &inventoryCancelReadConn{writerDriverConn: conn.(writerDriverConn), target: "FROM scan_command_results", point: "close", cancel: func() { cancelRead() }}
			})
			turn := scanLifecycleProtocolTurn(t, w, registry, InventoryCreate)
			if mode == "turn" {
				cancelRead = turn.transaction.cancel
			}
			got, err := lookupScanRunLifecycle(caller, turn, requests["hit"], InventoryCreate)
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, scanLifecycleReceiptLookup{}) {
				t.Fatal("late cancellation returned scalar proof", got, err)
			}
			turn.transaction.rollback(err)
		})
	}
}

func TestScanLifecycleAckDeleteFailureRollsBackFloor(t *testing.T) {
	w, requests, registry := scanLifecycleProtocolFixture(t)
	q := requests["hit"]
	turn := scanLifecycleProtocolTurn(t, w, registry, InventoryCreate)
	// Deliberate native failure AFTER the floor CAS. This is a fault fixture,
	// never a schema/plan qualification or an admissible production trigger.
	if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, `CREATE TEMP TRIGGER fail_lifecycle_header_delete
BEFORE DELETE ON main.scan_command_receipts BEGIN SELECT RAISE(ABORT,'lifecycle delete fault'); END`); err != nil {
		t.Fatal(err)
	}
	floor, err := acknowledgeScanRunLifecycle(t.Context(), turn, q, InventoryCreate)
	if err == nil || floor != 0 {
		t.Fatal("failed scalar cleanup reported ACK", floor, err)
	}
	var pendingFloor int64
	if readErr := turn.transaction.conn.QueryRowContext(turn.transaction.ctx, `SELECT acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`, q.RunID).Scan(&pendingFloor); readErr != nil || pendingFloor != q.Receipt.Identity.Sequence {
		t.Fatal("fault did not occur after the floor CAS", pendingFloor, readErr)
	}
	turn.transaction.rollback(err)
	var actual, headers int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`, q.RunID).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts WHERE run_id=?`, q.RunID).Scan(&headers); err != nil || actual != 0 || headers != 1 {
		t.Fatal("floor/header pair was not rolled back", actual, headers, err)
	}
}
