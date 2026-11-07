package writer

// Disposable branch-only group C probe. Exclude this file and the research SQL
// from publication slices. This is neither schema adoption nor an IA issuer.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func iaConfigurationDraftFixture(t *testing.T, source bool) (*executor, iaConfigurationReference, *stmt.Registry) {
	t.Helper()
	w, _, _ := testExecutor(t)
	sqlBytes, err := os.ReadFile("../../../research/p2ia-native/configuration.sql")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(sqlBytes)
	if hex.EncodeToString(hash[:]) != "84a50db58b27e3e797cec564d948424fab444bf2a015ca2dbaf889ac9f10bf4f" {
		t.Fatal("draft configuration SQL changed")
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
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
	registry := nativeInventoryFixtureRegistry(t, w, iaConfigurationHistoryDefinitions(), "ia_configuration_native.go", "ia_configuration_statements.go", "ia_configuration_draft_probe_test.go")
	return w, q, registry
}

func TestIAConfigurationDraftNativeOriginalReplayAckAndNoRefund(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(map[bool]string{false: "library", true: "source"}[source], func(t *testing.T) {
			w, q, registry := iaConfigurationDraftFixture(t, source)
			turn := scanLifecycleProtocolTurn(t, w, registry, q.original.purpose)
			h, o, err := lookupIAConfiguration(t.Context(), turn, q)
			if err != nil || h.decision != receiptReplay || h.result.sources[0].configuredRoot != "/a/../b" || h.result.sources[0].resolvedRoot != "/b" || o.rows != 2 {
				t.Fatal("original native roots/cohort lost", h, o, err)
			}
			beforeRows, beforeBytes := o.rows, o.bytes
			floor, err := acknowledgeIAConfiguration(t.Context(), turn, q)
			if err != nil || floor != 1 {
				t.Fatal(floor, err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var count, bytes, due, slot int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_retained_count,receipt_retained_bytes,receipt_reclaim_due_ms,reclaim_slot_bytes FROM catalog_configuration_operations`).Scan(&count, &bytes, &due, &slot); err != nil {
				t.Fatal(err)
			}
			if count != beforeRows || bytes != beforeBytes || due != 123 || slot != 0 {
				t.Fatal("ACK refunded data or invented a slot", count, bytes, due, slot)
			}
			// Fixture damage tests the ACK boundary, not a claimed GC operation. A
			// missing child must never be decoded into a replayed partial result.
			if _, err := w.db.ExecContext(t.Context(), `DELETE FROM catalog_configuration_sources`); err != nil {
				t.Fatal(err)
			}
			turn = scanLifecycleProtocolTurn(t, w, registry, q.original.purpose)
			h, o, err = lookupIAConfiguration(t.Context(), turn, q)
			if err != nil || h.decision != receiptRetired || len(h.result.sources) != 0 || o.acknowledged != 1 {
				t.Fatal("retired body decoded", h, o, err)
			}
			if floor, err := acknowledgeIAConfiguration(t.Context(), turn, q); err != nil || floor != 1 {
				t.Fatal("ACK retry changed floor", floor, err)
			}
		})
	}
}

func TestIAConfigurationDraftNativeDigestAndScopeRefusals(t *testing.T) {
	w, q, registry := iaConfigurationDraftFixture(t, true)
	turn := scanLifecycleProtocolTurn(t, w, registry, q.original.purpose)
	bad := q
	bad.original.libraryKey = "other"
	if h, o, err := lookupIAConfiguration(t.Context(), turn, bad); !errors.Is(err, ErrDisclosureDenied) || h.decision != 0 || o.exists {
		t.Fatal("foreign scope disclosed owner/result", h, o, err)
	}
	turn.transaction.rollback(nil)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_configuration_sources SET root_identity='replacement',retained_bytes=retained_bytes+10`); err != nil {
		t.Fatal(err)
	}
	turn = scanLifecycleProtocolTurn(t, w, registry, q.original.purpose)
	if h, o, err := lookupIAConfiguration(t.Context(), turn, q); !errors.Is(err, ErrResolveRequired) || h.decision != 0 || o.exists {
		t.Fatal("changed observed root replayed", h, o, err)
	}
}

func TestIAConfigurationDraftNativeAckRollbackAndFinish(t *testing.T) {
	w, q, registry := iaConfigurationDraftFixture(t, false)
	turn := scanLifecycleProtocolTurn(t, w, registry, q.original.purpose)
	if _, err := acknowledgeIAConfiguration(t.Context(), turn, q); err != nil {
		t.Fatal(err)
	}
	turn.transaction.rollback(errors.New("later effect failed"))
	var floor int64
	var due sql.NullInt64
	if err := w.db.QueryRowContext(t.Context(), `SELECT acknowledged_command_sequence,receipt_reclaim_due_ms FROM catalog_configuration_operations`).Scan(&floor, &due); err != nil {
		t.Fatal(err)
	}
	if floor != 0 || due.Valid {
		t.Fatal("rollback retained ACK/due", floor, due)
	}
	turn = scanLifecycleProtocolTurn(t, w, registry, q.original.purpose)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if h, o, err := lookupIAConfiguration(ctx, turn, q); !errors.Is(err, context.Canceled) || h.decision != 0 || len(h.result.sources) != 0 || o.exists {
		t.Fatal("cancelled output escaped", h, o, err)
	}
}

func TestIAConfigurationDraftNativeFullKeyMutants(t *testing.T) {
	for _, mutant := range []bool{false, true} {
		t.Run(map[bool]string{false: "baseline", true: "loaded_mutants"}[mutant], func(t *testing.T) {
			w, q, registry := iaConfigurationDraftFixture(t, false)
			if mutant {
				defs := iaConfigurationHistoryDefinitions()
				for i := range defs {
					switch defs[i].ID {
					case iaConfigurationHeaderPoint:
						defs[i].SQL = strings.Replace(defs[i].SQL, "command_incarnation=?", "command_incarnation>=?", 1)
						// Admit the deliberately weakened SEARCH prefix so the
						// native full-key falsifier, rather than only Capture,
						// has to detect the production SQL mutation.
						defs[i].Plan.Seeks = []string{"original_operation_id=?"}
					case iaConfigurationAckCAS:
						defs[i].SQL = strings.Replace(defs[i].SQL, "original_request_digest=?", "original_request_digest>=?", 1)
					}
				}
				registry = nativeInventoryFixtureRegistry(t, w, defs, "ia_configuration_native.go", "ia_configuration_statements.go", "ia_configuration_draft_probe_test.go")
			}
			turn := scanLifecycleProtocolTurn(t, w, registry, q.original.purpose)
			key, _ := iaConfigurationHeaderKey(q)
			key[1] = make([]byte, 16)
			var op, request, digest []byte
			var version, count, bytes int64
			err := iaRetirementPoint(t.Context(), turn, iaConfigurationHeaderPoint, key, &op, &request, &version, &count, &digest, &bytes)
			if !mutant && !errors.Is(err, sql.ErrNoRows) || mutant && err != nil {
				t.Fatal("header incarnation mutant was not distinguished", err)
			}
			turn.transaction.rollback(nil)
			turn = scanLifecycleProtocolTurn(t, w, registry, q.original.purpose)
			_, o, _ := iaConfigurationHistoryFixture(t, false)
			key, _ = iaConfigurationHeaderKey(q)
			args := append([]any{int64(123)}, key...)
			args = append(args, make([]byte, 32), q.original.libraryID, q.original.libraryKey, o.rows, o.bytes)
			err = iaRetirementOneEffect(t.Context(), turn, iaConfigurationAckCAS, args...)
			if !mutant && !errors.Is(err, ErrConflict) || mutant && err != nil {
				t.Fatal("ACK request mutant was not distinguished", err)
			}
		})
	}
}
