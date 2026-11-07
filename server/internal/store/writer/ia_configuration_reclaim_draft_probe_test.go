package writer

// Branch-only draft probes. Exclude this file/research SQL from publication.
// Reopen models a lost return; it is not the physical unknown-COMMIT suite.
import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"os"
	"strings"
	"testing"
)

func iaConfigurationReclaimDraftDefinitions() []stmt.Definition {
	defs := iaConfigurationReceiptReclaimDefinitions()
	for _, d := range iaConfigurationHistoryDefinitions() {
		if d.ID == iaConfigurationOwnerPoint {
			defs = append(defs, d)
		}
	}
	return defs
}
func iaConfigurationReclaimDraftFixture(t *testing.T, source bool) (*executor, string, iaReceiptReclaimReference, *stmt.Registry) {
	t.Helper()
	w, path, _ := testExecutor(t)
	sqlBytes, err := os.ReadFile("../../../research/p2ia-native/retirement-reclaim.sql")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(sqlBytes)
	if hex.EncodeToString(hash[:]) != "77f3f17b68d4e79a2e0d7832c7bfb4168bc8ab261d30a54d7df50989f3c5b2e1" {
		t.Fatal("draft configuration SQL changed")
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("DROP TABLE catalog_removal_progress")
	exec(string(sqlBytes))
	q, o, h := iaConfigurationHistoryFixture(t, source)
	r := h.result
	var originalSource, name, kind any
	if source {
		originalSource = q.original.sourceID
	} else {
		name, kind = r.libraryName, r.libraryKind
	}
	command, _ := iaConfigurationResultKindName(q.original.purpose)
	exec(`INSERT INTO libraries(id,name,kind,root) VALUES('lib','Library','movie','/b')`)
	exec(`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'lib','/b')`)
	exec(`INSERT INTO catalog_configuration_operations(original_operation_id,original_request_digest,command_incarnation,command_kind,original_library_id,original_library_key,
original_source_id,last_command_sequence,receipt_retained_count,receipt_retained_bytes) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		q.original.operationID[:], q.request[:], q.identity.Incarnation[:], command, q.original.libraryID, q.original.libraryKey, originalSource, o.last, o.rows, o.bytes)
	exec(`INSERT INTO catalog_configuration_receipts(original_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,
result_version,accepted_revision,applied_ms,result_library_name,result_library_kind,result_count,result_digest,retained_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		q.original.operationID[:], q.identity.Incarnation[:], int64(1), q.identity.OperationID[:], q.request[:], command, int64(1), r.acceptedRevision, r.appliedMS, name, kind, r.resultCount, h.digest[:], h.headerBytes)
	for i, s := range r.sources {
		exec(`INSERT INTO catalog_configuration_sources(original_operation_id,command_incarnation,command_sequence,command_kind,ordinal,source_id,source_name,source_incarnation,
source_generation,configured_root,resolved_root,root_identity,source_kind,classification,enabled,health,follow_symlinks,interval_seconds,identity_confirmed,
next_scan_at,last_complete_at,last_progress_at,retained_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			q.original.operationID[:], q.identity.Incarnation[:], int64(1), command, s.ordinal, s.id, s.name, s.incarnation, s.generation, s.configuredRoot, s.resolvedRoot, s.rootIdentity,
			s.kind, s.classification, s.enabled, s.health, s.followSymlinks, s.intervalSeconds, s.identityConfirmed, s.nextScanAt, s.lastCompleteAt, s.lastProgressAt, h.sourceBytes[i])
	}
	exec(`UPDATE catalog_configuration_operations SET acknowledged_command_sequence=1,receipt_reclaim_due_ms=100`)
	gc := iaReceiptReclaimReference{purpose: IAConfigurationReceiptReclaim, configuration: q.original, identity: CommandIdentity{Incarnation: q.identity.Incarnation, Sequence: 2, OperationID: [16]byte{3}}, request: RequestDigest{4}}
	registry := iaRetirementReclaimDraftRegistry(t, w, iaConfigurationReclaimDraftDefinitions(), "ia_configuration_reclaim_native.go", "ia_configuration_reclaim_statements.go", "ia_configuration_reclaim_draft_probe_test.go", "../../../research/p2ia-native/retirement-reclaim.sql")
	return w, path, gc, registry
}

func TestIAConfigurationReclaimClosedGrammar(t *testing.T) {
	defs := iaConfigurationReceiptReclaimDefinitions()
	if len(defs) != 15 {
		t.Fatal("closed105 inventory", len(defs))
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) > 0 {
			t.Fatal(d.ID, issues)
		}
		if strings.Contains(d.SQL, "job_lease") || strings.Contains(d.SQL, "DELETE FROM catalog_configuration_operations") {
			t.Fatal("cleanup borrowed lease/deleted owner", d.ID)
		}
	}
}

func TestIAConfigurationReclaimDraftNativeChildHeaderLostReturn(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(map[bool]string{false: "library", true: "source"}[source], func(t *testing.T) {
			w, path, q, registry := iaConfigurationReclaimDraftFixture(t, source)
			turn := scanLifecycleProtocolTurn(t, w, registry, q.purpose)
			h, err := applyIAConfigurationReceiptReclaim(t.Context(), turn, q, iaReceiptReclaimSlot{})
			if err != nil || h.decision != receiptFresh || h.facts.removedKind != "source" || h.facts.removedOrdinal != 0 || h.facts.noNext || h.facts.rows != 1 || h.owner.slot.retainedBytes != 243 {
				t.Fatal("bounded source cleanup", h, err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			w = iaRetirementReclaimReopen(t, w, path)
			turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
			replay, err := applyIAConfigurationReceiptReclaim(t.Context(), turn, q, iaReceiptReclaimSlot{})
			if err != nil || replay.decision != receiptReplay || replay.facts != h.facts || replay.digest != h.digest {
				t.Fatal("lost return became fresh/zero/partial", replay, err)
			}
			bad := q
			bad.request[0]++
			if out, err := lookupIAConfigurationReceiptReclaim(t.Context(), turn, bad); !errors.Is(err, ErrConflict) || out.owner.exists {
				t.Fatal("request conflict before ACK", out, err)
			}
			if floor, err := acknowledgeIAConfigurationReceiptReclaim(t.Context(), turn, q); err != nil || floor != 2 {
				t.Fatal(floor, err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var rows, bytes, slot int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_retained_count,receipt_retained_bytes,reclaim_slot_bytes FROM catalog_configuration_operations`).Scan(&rows, &bytes, &slot); err != nil || rows != 1 || bytes != h.facts.bytes || slot != 243 {
				t.Fatal("ACK refunded data/slot", rows, bytes, slot, err)
			}
			old := q
			q.identity.Sequence++
			q.identity.OperationID = [16]byte{5}
			q.request = RequestDigest{6}
			turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
			end, err := applyIAConfigurationReceiptReclaim(t.Context(), turn, q, h.owner.slot)
			if err != nil || end.decision != receiptFresh || end.facts.removedKind != "header" || end.facts.removedOrdinal != -1 || !end.facts.noNext || end.facts.rows != 0 || end.facts.bytes != 0 || end.owner.slot.retainedBytes != 235 {
				t.Fatal("bounded header cleanup", end, err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			w = iaRetirementReclaimReopen(t, w, path)
			turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
			endReplay, err := lookupIAConfigurationReceiptReclaim(t.Context(), turn, q)
			if err != nil || endReplay.decision != receiptReplay || endReplay.facts != end.facts || endReplay.digest != end.digest {
				t.Fatal("terminal saved result lost", endReplay, err)
			}
			retired, err := lookupIAConfigurationReceiptReclaim(t.Context(), turn, old)
			if err != nil || retired.decision != receiptRetired || retired.facts != (iaReceiptReclaimResult{}) || retired.digest != (ResultDigest{}) {
				t.Fatal("newer slot decoded as older result", retired, err)
			}
			if floor, err := acknowledgeIAConfigurationReceiptReclaim(t.Context(), turn, q); err != nil || floor != 3 {
				t.Fatal(floor, err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_configuration_operations SET reclaim_removed_bytes=reclaim_removed_bytes+1`); err != nil {
				t.Fatal(err)
			}
			turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
			retired, err = lookupIAConfigurationReceiptReclaim(t.Context(), turn, q)
			if err != nil || retired.decision != receiptRetired || retired.facts != (iaReceiptReclaimResult{}) {
				t.Fatal("ACKed body decoded", retired, err)
			}
		})
	}
}

func TestIAConfigurationReclaimDraftNativeRollbackScopeAndMissing(t *testing.T) {
	w, _, q, registry := iaConfigurationReclaimDraftFixture(t, true)
	defs := iaConfigurationReclaimDraftDefinitions()
	for i := range defs {
		if defs[i].ID == iaConfigurationGCStoreID(true, 0) {
			at := strings.Index(defs[i].SQL, " WHERE ")
			defs[i].SQL = defs[i].SQL[:at] + strings.Replace(defs[i].SQL[at:], "last_command_sequence=?", "last_command_sequence<?", 1)
		}
	}
	fault := iaRetirementReclaimDraftRegistry(t, w, defs, "ia_configuration_reclaim_native.go", "ia_configuration_reclaim_statements.go", "ia_configuration_reclaim_draft_probe_test.go")
	turn := scanLifecycleProtocolTurn(t, w, fault, q.purpose)
	if out, err := applyIAConfigurationReceiptReclaim(t.Context(), turn, q, iaReceiptReclaimSlot{}); !errors.Is(err, sql.ErrNoRows) || out.owner.exists {
		t.Fatal("stale CAS returned deleted prefix", out, err)
	}
	turn.transaction.rollback(errors.New("stale CAS"))
	var rows, children, slot int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_retained_count,(SELECT count(*) FROM catalog_configuration_sources),reclaim_slot_bytes FROM catalog_configuration_operations`).Scan(&rows, &children, &slot); err != nil || rows != 2 || children != 1 || slot != 0 {
		t.Fatal("failed CAS kept deletion/accounting", rows, children, slot, err)
	}
	turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
	bad := q
	bad.configuration.sourceID = "replacement"
	if out, err := applyIAConfigurationReceiptReclaim(t.Context(), turn, bad, iaReceiptReclaimSlot{}); !errors.Is(err, ErrConflict) || out.owner.exists {
		t.Fatal("source scope drift admitted", out, err)
	}
	turn.transaction.rollback(nil)
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM catalog_configuration_sources`); err != nil {
		t.Fatal(err)
	}
	turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
	if out, err := applyIAConfigurationReceiptReclaim(t.Context(), turn, q, iaReceiptReclaimSlot{}); !errors.Is(err, ErrConflict) || out.owner.exists {
		t.Fatal("missing child rebuilt result", out, err)
	}
	turn.transaction.rollback(nil)
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM catalog_configuration_receipts; UPDATE catalog_configuration_operations SET receipt_retained_count=0,receipt_retained_bytes=0`); err != nil {
		t.Fatal(err)
	}
	turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
	if out, err := applyIAConfigurationReceiptReclaim(t.Context(), turn, q, iaReceiptReclaimSlot{}); !errors.Is(err, ErrResolveRequired) || out.owner.exists {
		t.Fatal("unknown prefix became fresh empty result", out, err)
	}
}

func TestIAConfigurationReclaimDraftNativeSQLMutants(t *testing.T) {
	for _, mutant := range []bool{false, true} {
		t.Run(map[bool]string{false: "baseline", true: "loaded_mutants"}[mutant], func(t *testing.T) {
			w, _, q, registry := iaConfigurationReclaimDraftFixture(t, true)
			if mutant {
				defs := iaConfigurationReclaimDraftDefinitions()
				for i := range defs {
					if defs[i].ID == iaConfigurationGCSourceFirst {
						defs[i].SQL = strings.Replace(defs[i].SQL, "ordinal>?", "ordinal>=?", 1)
					}
					if defs[i].ID == iaConfigurationGCStoreID(true, 0) {
						defs[i].SQL = strings.Replace(defs[i].SQL, "original_request_digest=?", "original_request_digest>=?", 1)
					}
				}
				registry = iaRetirementReclaimDraftRegistry(t, w, defs, "ia_configuration_reclaim_native.go", "ia_configuration_reclaim_statements.go", "ia_configuration_reclaim_draft_probe_test.go")
			}
			turn := scanLifecycleProtocolTurn(t, w, registry, q.purpose)
			// Exact native boundary on the actual surviving ordinal: a cursor0
			// continuation must not select source0 again. No plan lint substitute.
			var ordinal, bytes int64
			err := iaRetirementPoint(t.Context(), turn, iaConfigurationGCSourceFirst, append(iaConfigurationGCKey(q), int64(0)), &ordinal, &bytes)
			if !mutant && !errors.Is(err, sql.ErrNoRows) || mutant && (err != nil || ordinal != 0) {
				t.Fatal("exclusive ordinal mutant survived", ordinal, err)
			}
			turn.transaction.rollback(nil)
			turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
			h, err := lookupIAConfigurationReceiptReclaim(t.Context(), turn, q)
			if err != nil {
				t.Fatal(err)
			}
			// A correctly formed candidate slot is deliberately paired with a
			// different OLD original-request binding in the raw compiled CAS.
			first, err := firstIAConfigurationGCUnit(t.Context(), turn, q, h.owner, h.owner.cursor)
			if err != nil {
				t.Fatal(err)
			}
			in := iaReceiptReclaimInput{reference: q, owner: h.owner, first: first, appliedMS: 100}
			v, err := prepareIAReceiptReclaimUnit(in)
			if err != nil {
				t.Fatal(err)
			}
			v.noNext = false
			digest, err := canonicalResultDigest(q.identity, q.request, iaConfigurationReceiptReclaimResult{q.configuration, v})
			if err != nil {
				t.Fatal(err)
			}
			saved := iaReceiptReclaimSlot{present: true, identity: q.identity, request: q.request, purpose: q.purpose, version: 1, resultCount: 1, facts: v, digest: digest}
			saved.retainedBytes, err = iaReceiptReclaimSlotBytes(q.purpose, v)
			if err != nil {
				t.Fatal(err)
			}
			bad := q
			bad.configuration.requestDigest = RequestDigest{}
			shape, args := iaConfigurationGCStoreArguments(bad, h.owner, saved)
			var charge int64
			err = iaRetirementPoint(t.Context(), turn, iaConfigurationGCStoreID(true, shape), args, &charge)
			if !mutant && !errors.Is(err, sql.ErrNoRows) || mutant && (err != nil || charge != 243) {
				t.Fatal("full OLD original-request mutant survived", charge, err)
			}
		})
	}
}
