package writer

// Disposable research-branch probe, excluded from publication slices. It
// installs only the exact IA-owned draft removal objects in an independent
// canonical fixture. INT still owns adoption; this is not a schema embed.

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
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
	defs := append(iaRetirementHistoryDefinitions(), iaRetirementBeginDefinitions()...)
	if mutate != nil {
		mutate(defs)
	}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "ia_retirement_draft_probe_test.go", "ia_retirement_statements.go")
	return w, q, registry
}

func TestIARetirementDraftNativeLateCancellationClearsOwner(t *testing.T) {
	w, q, registry := iaRetirementDraftFixture(t, nil)
	turn := scanLifecycleProtocolTurn(t, w, registry, IARetireSource)
	delete(turn.steps, iaRetirementReceiptPoint)
	delete(turn.steps, iaRetirementBodyPoint)
	wrong := q
	wrong.libraryID++
	if got, err := lookupIARetirement(t.Context(), turn, wrong); !errors.Is(err, ErrDisclosureDenied) || got != (iaRetirementHistory{}) {
		t.Fatal("wrong native library reached header or result", got, err)
	}
	turn.transaction.rollback(nil)
	var sequence int64
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"caller", "turn"} {
		t.Run(mode, func(t *testing.T) {
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config)
			if err != nil {
				t.Fatal(err)
			}
			caller, cancel := context.WithCancel(t.Context())
			defer cancel()
			cancelRead := cancel
			w.openPool(connector, func(conn driver.Conn) driver.Conn {
				return &inventoryCancelReadConn{writerDriverConn: conn.(writerDriverConn), target: "SELECT accepted_revision,applied_ms,result_phase", point: "close", cancel: func() { cancelRead() }}
			})
			turn := scanLifecycleProtocolTurn(t, w, registry, IARetireSource)
			if mode == "turn" {
				cancelRead = turn.transaction.cancel
			}
			got, err := lookupIARetirement(caller, turn, q)
			if !errors.Is(err, context.Canceled) || got != (iaRetirementHistory{}) {
				t.Fatal("late cancellation returned result or owner", got, err)
			}
			turn.transaction.rollback(err)
		})
	}
}

func TestIARetirementDraftNativeHistoryAckAndReclaim(t *testing.T) {
	w, q, registry := iaRetirementDraftFixture(t, nil)
	turn := scanLifecycleProtocolTurn(t, w, registry, IARetireSource)
	got, err := lookupIARetirement(t.Context(), turn, q)
	owner := got.owner
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
	got, err = lookupIARetirement(t.Context(), turn, q)
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
						// Test-only weaker plan expectation lets the compiled mutant
						// reach the native effect falsifier, rather than dying at
						// registration on the removed leading-key predicate.
						defs[i].Plan.Seeks = []string{"original_operation_id=? AND command_incarnation=? AND result_kind=?"}
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

func iaRetirementDraftFreshInput(t *testing.T, w *executor, purpose CommandID) *iaRetirementCommand {
	t.Helper()
	for _, q := range []string{`DELETE FROM catalog_removal_receipts`, `DELETE FROM catalog_removal_progress`,
		`UPDATE libraries SET configuration_present=1,configuration_revision=5,configuration_metadata_revision=9`,
		`INSERT INTO library_sources(id,library_id,configured_root,root,generation,incarnation,root_identity,identity_confirmed) VALUES('source','library','/media/original','/media/original',3,'original','device:inode',1)`} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	id, r := iaRetirementRequestFixture()
	if purpose == IARetireLibrary {
		r.scope.sourceID, r.scope.sourceIncarnation, r.scope.sourceGeneration, r.scope.rootIdentity = "", "", 0, ""
		r.libraryName, r.confirmation = "Library", " Library "
	}
	input, err := newIARetirementCommand(purpose, id, r)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestIARetirementDraftNativeBeginFencesAndHistory(t *testing.T) {
	for _, purpose := range []CommandID{IARetireLibrary, IARetireSource} {
		t.Run(map[CommandID]string{IARetireLibrary: "library", IARetireSource: "source"}[purpose], func(t *testing.T) {
			w, _, registry := iaRetirementDraftFixture(t, nil)
			input := iaRetirementDraftFreshInput(t, w, purpose)
			turn := scanLifecycleProtocolTurn(t, w, registry, purpose)
			if turn.transaction.settings.Synchronous != 2 {
				t.Fatal("BEGIN denial is not FULL")
			}
			got, err := applyIARetirementBegin(t.Context(), turn, input)
			if err != nil || got.acceptedRevision != 1 || got.state != "retiring" || got.phase != 0 {
				t.Fatal(got, err)
			}
			q := iaRetirementReference{kind: got.original.kind, target: got.original.target, libraryID: 7, libraryKey: "library", originalOperation: input.identity.OperationID, identity: input.identity, request: input.digest, purpose: purpose}
			history, err := lookupIARetirement(t.Context(), turn, q)
			if err != nil || history.decision != receiptReplay || history.result != got {
				t.Fatal("fresh fence erased historical original", history, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var retired, generation, enabled, revision, due int64
			var health string
			if err = w.db.QueryRowContext(t.Context(), `SELECT retired FROM catalog_libraries WHERE id=7`).Scan(&retired); err != nil {
				t.Fatal(err)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT generation,enabled,health FROM library_sources WHERE id='source'`).Scan(&generation, &enabled, &health); err != nil {
				t.Fatal(err)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT configuration_revision FROM libraries WHERE id='library'`).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT job_due_ms FROM catalog_removal_progress`).Scan(&due); err != nil {
				t.Fatal(err)
			}
			if revision != 6 || due != got.appliedMS {
				t.Fatal("configuration/scheduling", revision, due, got.appliedMS)
			}
			if purpose == IARetireLibrary {
				if retired != 1 || generation != 3 || enabled != 1 || health != "ready" {
					t.Fatal("library BEGIN fanned out", retired, generation, enabled, health)
				}
			} else if retired != 0 || generation != 4 || enabled != 0 || health != "removing" {
				t.Fatal("source denial fence", retired, generation, enabled, health)
			}
		})
	}
}

func TestIARetirementDraftNativeBeginRollback(t *testing.T) {
	w, _, registry := iaRetirementDraftFixture(t, nil)
	input := iaRetirementDraftFreshInput(t, w, IARetireSource)
	// Deliberate test-only native failure after all fresh fences and driver
	// insertion. The candidate itself has zero application triggers.
	if _, err := w.db.ExecContext(t.Context(), `CREATE TRIGGER ia_probe_reject_header BEFORE INSERT ON catalog_removal_receipts BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	turn := scanLifecycleProtocolTurn(t, w, registry, IARetireSource)
	if got, err := applyIARetirementBegin(t.Context(), turn, input); err == nil || got != (iaRetirementResult{}) {
		t.Fatal("failed BEGIN released output", got, err)
	}
	turn.transaction.rollback(nil)
	var revision, generation, enabled, drivers int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT configuration_revision FROM libraries WHERE id='library'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT generation,enabled FROM library_sources WHERE id='source'`).Scan(&generation, &enabled); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_removal_progress`).Scan(&drivers); err != nil || revision != 5 || generation != 3 || enabled != 1 || drivers != 0 {
		t.Fatal("BEGIN prefix survived rollback", revision, generation, enabled, drivers, err)
	}
}
