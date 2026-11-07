package writer

// Branch-only draft protocol probes, excluded with research SQL from product
// publication. No schema adoption, current-authority issuer or qualification.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func iaRetirementReclaimDraftDefinitions() []stmt.Definition {
	defs := iaRetirementReceiptReclaimDefinitions()
	for _, d := range iaRetirementHistoryDefinitions() {
		if d.ID == iaRetirementOwnerPoint {
			defs = append(defs, d)
		}
	}
	return defs
}

func iaRetirementReclaimDraftFixture(t *testing.T, source bool) (*executor, string, iaReceiptReclaimReference, *stmt.Registry) {
	t.Helper()
	w, path, _ := testExecutor(t)
	data, err := os.ReadFile("../../../research/p2ia-native/retirement-reclaim.sql")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != "8a577e6643eba63b35cb02dd22bc6270fe6cb654f7875777ad9c1737d1b1296d" {
		t.Fatal("draft retirement SQL changed")
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("DROP TABLE catalog_removal_progress")
	exec(string(data))
	id, request, r := iaRetirementResultFixture()
	kind := IARetireSource
	if !source {
		kind = IARetireLibrary
		r.original.kind, r.original.target = "library", r.original.libraryKey
		r.original.sourceIncarnation, r.original.sourceGeneration, r.original.rootIdentity = "", 0, ""
	}
	var result any = iaRetireSourceResult{r}
	if !source {
		result = iaRetireLibraryResult{r}
	}
	digest, err := canonicalResultDigest(id, request, result)
	if err != nil {
		t.Fatal(err)
	}
	n, err := iaRetirementRetainedBytes(kind, r)
	if err != nil {
		t.Fatal(err)
	}
	o := r.original
	var sourceInc, generation, root any
	if source {
		sourceInc, generation, root = o.sourceIncarnation, o.sourceGeneration, o.rootIdentity
	}
	exec(`INSERT INTO libraries(id,name,kind,root) VALUES('library','Library','movie','/media/original')`)
	exec(`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'library','/media/original')`)
	exec(`INSERT INTO catalog_removal_progress(kind,target,original_operation_id,original_request_digest,command_incarnation,
original_library_id,original_library_key,original_resolved_root,original_source_incarnation,original_source_generation,original_root_identity,
last_command_sequence,acknowledged_command_sequence,receipt_retained_count,receipt_retained_bytes,receipt_reclaim_due_ms,job_lease_owner,job_lease_generation,job_lease_expires_ms)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, o.kind, o.target, o.operationID[:], o.requestDigest[:], id.Incarnation[:], o.libraryID, o.libraryKey, o.resolvedRoot,
		sourceInc, generation, root, int64(1), int64(1), int64(1), n, int64(100), int64(17), int64(2), int64(10000))
	name, _ := iaRetirementKindName(kind)
	iaRetirementReclaimInsertHeader(t, w, id, request, r, name, digest, n)
	q := iaReceiptReclaimReference{purpose: IARetirementReceiptReclaim, identity: CommandIdentity{Incarnation: id.Incarnation, Sequence: 2, OperationID: [16]byte{3}}, request: RequestDigest{4}, retirement: o}
	registry := nativeInventoryFixtureRegistry(t, w, iaRetirementReclaimDraftDefinitions(), "ia_retirement_reclaim_native.go", "ia_retirement_reclaim_statements.go", "ia_retirement_reclaim_draft_probe_test.go", "../../../research/p2ia-native/retirement-reclaim.sql")
	return w, path, q, registry
}

func iaRetirementReclaimInsertHeader(t *testing.T, w *executor, id CommandIdentity, request RequestDigest, r iaRetirementResult, name string, digest ResultDigest, n int64) {
	t.Helper()
	o := r.original
	_, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_removal_receipts(original_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,
result_version,accepted_revision,applied_ms,result_kind,result_target,result_phase,result_entity_kind,result_entity_cursor,result_object_cursor,
result_walk_removed,result_walk_kept,result_state,result_count,result_digest,retained_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.operationID[:], id.Incarnation[:], id.Sequence, id.OperationID[:], request[:], name, int64(1), r.acceptedRevision, r.appliedMS, o.kind, o.target, r.phase, r.entityKind, r.entityCursor, r.objectCursor, r.walkRemoved, r.walkKept, r.state, int64(0), digest[:], n)
	if err != nil {
		t.Fatal(err)
	}
}

func iaRetirementReclaimReopen(t *testing.T, w *executor, path string) *executor {
	t.Helper()
	cfg, clock := w.config, w.now
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	next := &executor{config: cfg, now: clock}
	next.openPool(connector, nil)
	t.Cleanup(func() {
		if err := next.close(); err != nil {
			t.Error(err)
		}
	})
	conn, err := next.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = sqliteconn.Assert(t.Context(), conn, cfg)
	err = errors.Join(err, conn.Close())
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestIARetirementReclaimDraftNativeLostReturnAndAck(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(map[bool]string{false: "library", true: "source"}[source], func(t *testing.T) {
			w, path, q, registry := iaRetirementReclaimDraftFixture(t, source)
			turn := scanLifecycleProtocolTurn(t, w, registry, q.purpose)
			h, err := applyIARetirementReceiptReclaim(t.Context(), turn, q, iaReceiptReclaimSlot{})
			if err != nil || h.decision != receiptFresh || h.facts.removedCount != 1 || h.facts.removedSequence != 1 || !h.facts.noNext || h.owner.slot.retainedBytes != 216 {
				t.Fatal("bounded106 failed", h, err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			// Lose the return, close the physical pool, and reopen this known draft
			// file. This is the saved-reference retry model, not a physical unknown-
			// COMMIT fault/issuer qualification claim.
			w = iaRetirementReclaimReopen(t, w, path)
			turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
			replay, err := applyIARetirementReceiptReclaim(t.Context(), turn, q, iaReceiptReclaimSlot{})
			if err != nil || replay.decision != receiptReplay || replay.facts != h.facts || replay.digest != h.digest {
				t.Fatal("lost return became fresh missing-child output", replay, err)
			}
			bad := q
			bad.request[0]++
			if out, err := applyIARetirementReceiptReclaim(t.Context(), turn, bad, iaReceiptReclaimSlot{}); !errors.Is(err, ErrConflict) || out.owner.exists {
				t.Fatal("mismatched original request replayed", out, err)
			}
			floor, err := acknowledgeIARetirementReceiptReclaim(t.Context(), turn, q)
			if err != nil || floor != 2 {
				t.Fatal(floor, err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var rows, bytes, slot, owner, generation, expires, phase int64
			var state string
			if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_retained_count,receipt_retained_bytes,reclaim_slot_bytes,job_lease_owner,job_lease_generation,job_lease_expires_ms,phase,retirement_state FROM catalog_removal_progress`).Scan(&rows, &bytes, &slot, &owner, &generation, &expires, &phase, &state); err != nil {
				t.Fatal(err)
			}
			if rows != 0 || bytes != 0 || slot != 216 || owner != 17 || generation != 2 || expires != 10000 || phase != 0 || state != "retiring" {
				t.Fatal("GC/ACK changed physical retirement or refunded slot", rows, bytes, slot, owner, generation, expires, phase, state)
			}
			if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_removal_progress SET reclaim_removed_bytes=reclaim_removed_bytes+1`); err != nil {
				t.Fatal(err)
			}
			turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
			retired, err := lookupIARetirementReceiptReclaim(t.Context(), turn, q)
			if err != nil || retired.decision != receiptRetired || retired.digest != (ResultDigest{}) || retired.facts != (iaReceiptReclaimResult{}) {
				t.Fatal("ACKed body was decoded", retired, err)
			}
		})
	}
}

func TestIARetirementReclaimDraftNativeRollbackScopeAndMissing(t *testing.T) {
	w, _, q, registry := iaRetirementReclaimDraftFixture(t, true)
	defs := iaRetirementReclaimDraftDefinitions()
	for i := range defs {
		if defs[i].ID == iaRetirementGCStoreID(true, 0) {
			// Change only the OLD predicate, never SET last_command_sequence.
			at := strings.Index(defs[i].SQL, " WHERE ")
			defs[i].SQL = defs[i].SQL[:at] + strings.Replace(defs[i].SQL[at:], "last_command_sequence=?", "last_command_sequence<?", 1)
		}
	}
	fault := nativeInventoryFixtureRegistry(t, w, defs, "ia_retirement_reclaim_native.go", "ia_retirement_reclaim_statements.go", "ia_retirement_reclaim_draft_probe_test.go")
	turn := scanLifecycleProtocolTurn(t, w, fault, q.purpose)
	if out, err := applyIARetirementReceiptReclaim(t.Context(), turn, q, iaReceiptReclaimSlot{}); !errors.Is(err, sql.ErrNoRows) || out.owner.exists || out.decision != 0 {
		t.Fatal("stale CAS returned a prefix", out, err)
	}
	turn.transaction.rollback(errors.New("stale CAS"))
	var rows, headers, slot int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_retained_count,(SELECT count(*) FROM catalog_removal_receipts),reclaim_slot_bytes FROM catalog_removal_progress`).Scan(&rows, &headers, &slot); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || headers != 1 || slot != 0 {
		t.Fatal("failed CAS retained deletion/debt/slot", rows, headers, slot)
	}
	turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
	bad := q
	bad.retirement.rootIdentity = "replacement"
	if out, err := applyIARetirementReceiptReclaim(t.Context(), turn, bad, iaReceiptReclaimSlot{}); !errors.Is(err, ErrConflict) || out.owner.exists {
		t.Fatal("source root scope drift admitted", out, err)
	}
	turn.transaction.rollback(nil)
	// Damage an unreceipted accounting prefix. It must resolve/refuse, never
	// manufacture a zero-removal receipt for an unknowably deleted old header.
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM catalog_removal_receipts; UPDATE catalog_removal_progress SET receipt_retained_count=0,receipt_retained_bytes=0`); err != nil {
		t.Fatal(err)
	}
	turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
	if out, err := applyIARetirementReceiptReclaim(t.Context(), turn, q, iaReceiptReclaimSlot{}); !errors.Is(err, ErrResolveRequired) || out.decision != 0 {
		t.Fatal("missing data became a fresh zero result", out, err)
	}
}

func TestIARetirementReclaimDraftNativeSQLMutants(t *testing.T) {
	for _, mutant := range []bool{false, true} {
		t.Run(map[bool]string{false: "baseline", true: "loaded_mutants"}[mutant], func(t *testing.T) {
			w, _, q, registry := iaRetirementReclaimDraftFixture(t, true)
			if mutant {
				defs := iaRetirementReclaimDraftDefinitions()
				for i := range defs {
					if defs[i].ID == iaRetirementGCFirst {
						defs[i].SQL = strings.Replace(defs[i].SQL, "command_sequence>?", "command_sequence>=?", 1)
					}
					if defs[i].ID == iaRetirementGCStoreID(true, 0) {
						defs[i].SQL = strings.Replace(defs[i].SQL, "original_request_digest=?", "original_request_digest>=?", 1)
					}
				}
				registry = nativeInventoryFixtureRegistry(t, w, defs, "ia_retirement_reclaim_native.go", "ia_retirement_reclaim_statements.go", "ia_retirement_reclaim_draft_probe_test.go")
			}
			turn := scanLifecycleProtocolTurn(t, w, registry, q.purpose)
			out, err := applyIARetirementReceiptReclaim(t.Context(), turn, q, iaReceiptReclaimSlot{})
			if !mutant && (err != nil || out.decision != receiptFresh) || mutant && (!errors.Is(err, ErrConflict) || out.decision != 0) {
				t.Fatal("exclusive cursor mutant not distinguished", out, err)
			}
			turn.transaction.rollback(nil)
			turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
			h, err := lookupIARetirementReceiptReclaim(t.Context(), turn, q)
			if err != nil {
				t.Fatal(err)
			}
			v := iaReceiptReclaimResult{capturedACK: 1, old: iaReceiptCursor{0, -1}, next: iaReceiptCursor{1, -1}, removedKind: "header", removedSequence: 1, removedOrdinal: -1, removedCount: 1, removedBytes: h.owner.bytes, noNext: true, appliedMS: 100}
			digest, err := canonicalResultDigest(q.identity, q.request, iaRetirementReceiptReclaimResult{q.retirement, v})
			if err != nil {
				t.Fatal(err)
			}
			s := iaReceiptReclaimSlot{present: true, identity: q.identity, request: q.request, purpose: q.purpose, version: 1, resultCount: 1, facts: v, digest: digest}
			s.retainedBytes, _ = iaReceiptReclaimSlotBytes(q.purpose, v)
			shape, args := iaRetirementGCStoreArguments(q, h.owner, s)
			id := iaRetirementGCStoreID(true, shape)
			for _, d := range iaRetirementReceiptReclaimDefinitions() {
				if d.ID == id {
					for i, p := range d.Params {
						if p.Name == "original_request" {
							args[i] = make([]byte, 32)
						}
					}
				}
			}
			var charge int64
			err = iaRetirementPoint(t.Context(), turn, id, args, &charge)
			if !mutant && !errors.Is(err, sql.ErrNoRows) || mutant && (err != nil || charge != 216) {
				t.Fatal("original-request CAS mutant not distinguished", charge, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

func TestIARetirementReclaimDraftNativeLaterDataAndOldSlot(t *testing.T) {
	w, _, q, registry := iaRetirementReclaimDraftFixture(t, true)
	turn := scanLifecycleProtocolTurn(t, w, registry, q.purpose)
	first, err := applyIARetirementReceiptReclaim(t.Context(), turn, q, iaReceiptReclaimSlot{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acknowledgeIARetirementReceiptReclaim(t.Context(), turn, q); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	// Seed an already accepted/ACKed physical STEP3. This fixture is not
	// verification of that separate104 body. GC2 stays a cleanup-only gap.
	now := w.now().Add(time.Millisecond)
	w.now = func() time.Time { return now }
	r := iaRetirementResult{original: q.retirement, acceptedRevision: 2, appliedMS: now.UnixMilli(), phase: 1, state: "retiring"}
	id := CommandIdentity{Incarnation: q.identity.Incarnation, Sequence: 3, OperationID: [16]byte{8}}
	request := RequestDigest{8}
	digest, err := canonicalResultDigest(id, request, iaRetireStepResult{r})
	if err != nil {
		t.Fatal(err)
	}
	n, err := iaRetirementRetainedBytes(IARetireStep, r)
	if err != nil {
		t.Fatal(err)
	}
	iaRetirementReclaimInsertHeader(t, w, id, request, r, "retire_step", digest, n)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_removal_progress SET last_command_sequence=3,acknowledged_command_sequence=3,receipt_retained_count=1,receipt_retained_bytes=?,receipt_reclaim_due_ms=?,phase=1,job_revision=2`, n, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
	wrongPurpose := q
	wrongPurpose.identity = id
	wrongPurpose.request = request
	if out, err := lookupIARetirementReceiptReclaim(t.Context(), turn, wrongPurpose); !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("ACK floor hid retained STEP purpose", out, err)
	}
	turn.transaction.rollback(nil)
	q.identity.Sequence = 4
	q.identity.OperationID = [16]byte{6}
	q.request = RequestDigest{6}
	turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
	wrong := first.owner.slot
	wrong.digest[0]++
	if out, err := applyIARetirementReceiptReclaim(t.Context(), turn, q, wrong); !errors.Is(err, ErrResolveRequired) || out.decision != 0 {
		t.Fatal("changed OLD slot adopted", out, err)
	}
	turn.transaction.rollback(nil)
	turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
	last, err := applyIARetirementReceiptReclaim(t.Context(), turn, q, first.owner.slot)
	if err != nil || last.facts.removedSequence != 3 || last.facts.next.sequence != 3 || last.owner.rows != 0 || first.owner.slot.facts.rows != 0 {
		t.Fatal("later physical data/GC-only gap lost", last, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	turn = scanLifecycleProtocolTurn(t, w, registry, q.purpose)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := lookupIARetirementReceiptReclaim(ctx, turn, q); !errors.Is(err, context.Canceled) || out.owner.exists || out.decision != 0 {
		t.Fatal("cancelled saved output escaped", out, err)
	}
}
