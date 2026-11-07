package writer

// Disposable research-branch probe, excluded from publication slices. It
// installs only the exact IA-owned draft removal objects in an independent
// canonical fixture. INT still owns adoption; this is not a schema embed.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func iaRetirementDraftFixture(t *testing.T, mutate func([]stmt.Definition)) (*executor, iaRetirementReference, *stmt.Registry) {
	t.Helper()
	w, _, _ := testExecutor(t)
	data, err := os.ReadFile("../../../research/p2ia-native/removal.sql")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != "c902d9c8a8f02d16117d802452a0b4b684023e165faff3d344e9429119dd1f19" {
		t.Fatal("draft SQL changed")
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("DROP TABLE catalog_removal_progress")
	exec(string(data))
	q, owner, header := iaRetirementHistoryFixture(t)
	exec(`INSERT INTO libraries(id,name,kind,root) VALUES('library','Library','movie','/media/original')`)
	exec(`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'library','/media/original')`)
	o := owner.original
	exec(`INSERT INTO catalog_removal_progress(kind,target,original_operation_id,original_request_digest,command_incarnation,
original_library_id,original_library_key,original_resolved_root,original_source_incarnation,original_source_generation,original_root_identity,
last_command_sequence,receipt_retained_count,receipt_retained_bytes,job_lease_owner,job_lease_generation,job_lease_expires_ms)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, o.kind, o.target, o.operationID[:], o.requestDigest[:], owner.incarnation[:], o.libraryID, o.libraryKey, o.resolvedRoot,
		o.sourceIncarnation, o.sourceGeneration, o.rootIdentity, owner.last, owner.retainedCount, owner.retainedBytes, 17, 2, 10000)
	r := header.result
	exec(`INSERT INTO catalog_removal_receipts(original_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,
result_version,accepted_revision,applied_ms,result_kind,result_target,result_phase,result_entity_kind,result_entity_cursor,result_object_cursor,
result_walk_removed,result_walk_kept,result_state,result_count,result_digest,retained_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.operationID[:], owner.incarnation[:], q.identity.Sequence, q.identity.OperationID[:], q.request[:], "retire_source", 1, r.acceptedRevision, r.appliedMS,
		o.kind, o.target, r.phase, r.entityKind, r.entityCursor, r.objectCursor, 0, 0, r.state, 0, header.digest[:], header.retainedBytes)
	defs := iaRetirementHistoryDefinitions()
	if mutate != nil {
		mutate(defs)
	}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "ia_retirement_draft_probe_test.go", "ia_retirement_statements.go")
	return w, q, registry
}

func TestIARetirementDraftNativeHistoryAckAndReclaim(t *testing.T) {
	w, q, registry := iaRetirementDraftFixture(t, nil)
	turn := scanLifecycleProtocolTurn(t, w, registry, IARetireSource)
	got, owner, err := lookupIARetirement(t.Context(), turn, q)
	if err != nil || got.decision != receiptReplay || got.result.original.resolvedRoot != "/media/original" || owner.retainedBytes != 233 {
		t.Fatal(got, owner, err)
	}
	ack, err := acknowledgeIARetirement(t.Context(), turn, q)
	if err != nil || ack != 1 {
		t.Fatal(ack, err)
	}
	var count, debt int64
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT receipt_retained_count,receipt_retained_bytes FROM catalog_removal_progress`).Scan(&count, &debt); err != nil || count != 1 || debt != 233 {
		t.Fatal("ACK refunded native debt", count, debt, err)
	}
	delete(turn.steps, iaRetirementBodyPoint)
	got, _, err = lookupIARetirement(t.Context(), turn, q)
	if err != nil || got.decision != receiptRetired || got.result != (iaRetirementResult{}) {
		t.Fatal("ACKed body was queried", got, err)
	}
	removed, err := reclaimIARetirement(t.Context(), turn, q)
	if err != nil || !removed {
		t.Fatal(removed, err)
	}
	if removed, err = reclaimIARetirement(t.Context(), turn, q); err != nil || removed {
		t.Fatal("reclaim retry", removed, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var last, acknowledged, reclaimed, lease, leaseGeneration, expiry, receipts int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT last_command_sequence,acknowledged_command_sequence,receipt_reclaim_sequence,receipt_retained_count,receipt_retained_bytes,job_lease_owner,job_lease_generation,job_lease_expires_ms FROM catalog_removal_progress`).Scan(&last, &acknowledged, &reclaimed, &count, &debt, &lease, &leaseGeneration, &expiry); err != nil {
		t.Fatal(err)
	}
	if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_removal_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if last != 1 || acknowledged != 1 || reclaimed != 1 || count != 0 || debt != 0 || receipts != 0 || lease != 17 || leaseGeneration != 2 || expiry != 10000 {
		t.Fatal("native retained driver/debt/lease", last, acknowledged, reclaimed, count, debt, receipts, lease, leaseGeneration, expiry)
	}
}

func TestIARetirementDraftNativeReclaimRollback(t *testing.T) {
	w, q, registry := iaRetirementDraftFixture(t, nil)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_removal_progress SET acknowledged_command_sequence=1`); err != nil {
		t.Fatal(err)
	}
	turn := scanLifecycleProtocolTurn(t, w, registry, IARetireStep)
	// Fail only the ledger CAS after the exact native deletion. This tests
	// whole-frame rollback; no partial refund or physical row loss may persist.
	delete(turn.steps, iaRetirementReclaimCAS)
	if removed, err := reclaimIARetirement(t.Context(), turn, q); !errors.Is(err, ErrInvalid) || removed {
		t.Fatal(removed, err)
	}
	turn.transaction.rollback(nil)
	var receipts, debt, reclaimed int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_removal_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_retained_bytes,receipt_reclaim_sequence FROM catalog_removal_progress`).Scan(&debt, &reclaimed); err != nil || receipts != 1 || debt != 233 || reclaimed != 0 {
		t.Fatal("partial reclaim survived rollback", receipts, debt, reclaimed, err)
	}
}

func TestIARetirementDraftNativeFullKeyMutants(t *testing.T) {
	for _, mutant := range []bool{false, true} {
		t.Run(map[bool]string{false: "production", true: "loaded_mutant"}[mutant], func(t *testing.T) {
			w, q, registry := iaRetirementDraftFixture(t, func(defs []stmt.Definition) {
				if !mutant {
					return
				}
				for i := range defs {
					if defs[i].ID == iaRetirementAckCAS {
						defs[i].SQL = strings.Replace(defs[i].SQL, "original_operation_id=?", "? IS NOT NULL", 1)
					}
				}
			})
			turn := scanLifecycleProtocolTurn(t, w, registry, IARetireSource)
			wrong := q
			wrong.originalOperation[15]++
			err := iaRetirementOneEffect(t.Context(), turn, iaRetirementAckCAS, append([]any{int64(1)}, append(iaRetirementKey(wrong), int64(1), int64(0))...)...)
			if mutant {
				if err != nil {
					t.Fatal("mutant did not load/reach falsifier", err)
				}
			} else if !errors.Is(err, ErrConflict) {
				t.Fatal("wrong original operation changed ACK", err)
			}
			turn.transaction.rollback(nil)
		})
	}
	for _, mutant := range []bool{false, true} {
		t.Run(map[bool]string{false: "receipt_production", true: "receipt_loaded_mutant"}[mutant], func(t *testing.T) {
			w, q, registry := iaRetirementDraftFixture(t, func(defs []stmt.Definition) {
				if !mutant {
					return
				}
				for i := range defs {
					if defs[i].ID == iaRetirementReclaimDelete {
						defs[i].SQL = strings.Replace(defs[i].SQL, "result_target=?", "? IS NOT NULL", 1)
					}
				}
			})
			turn := scanLifecycleProtocolTurn(t, w, registry, IARetireStep)
			wrong := q
			wrong.target = "replacement-source"
			err := iaRetirementOneEffect(t.Context(), turn, iaRetirementReclaimDelete, append(iaRetirementKey(wrong), int64(1), int64(233))...)
			if mutant {
				if err != nil {
					t.Fatal("mutant did not load/reach falsifier", err)
				}
			} else if !errors.Is(err, ErrConflict) {
				t.Fatal("wrong typed target deleted receipt", err)
			}
			turn.transaction.rollback(nil)
		})
	}
}
