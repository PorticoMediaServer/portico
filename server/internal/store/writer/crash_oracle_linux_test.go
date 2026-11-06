package writer

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// V's D-INGEST/PERSONAL contract: expected state comes from the operation
// journal and the witnessed native boundary, never the candidate's receipt
// classifier, reducers, predicates or result encoder. This small hidden-BEGIN
// model is separate from V's Python leaf browse/sort/search evaluator.
type crashExpected struct{ Images, Receipts, Sequence, Ack int64 }

func crashCommitted(c crashCase) bool {
	if c.CommitFault != "" {
		return c.CommitFault == "after"
	}
	return c.Boundary == "after_commit" || c.Boundary == "physical_return" || c.Boundary == "api_return"
}

func expectedCrashState(c crashCase) (crashExpected, error) {
	valid := c.Domain == "effective"
	switch c.Operation {
	case "stage", "resolve", "ack":
		boundaries := map[string]bool{"before_begin": true, "after_begin": true, "before_commit": true, "after_commit": true, "physical_return": true, "api_return": true}
		switch c.Operation {
		case "stage":
			boundaries["stage_image"], boundaries["stage_attach"], boundaries["stage_receipt"] = true, true, true
		case "resolve":
			boundaries["resolve_receipt"] = true
		case "ack":
			boundaries["resolve_receipt"], boundaries["ack_floor"] = true, true
		}
		valid = valid && (c.CommitFault == "" && boundaries[c.Boundary] || c.Boundary == "uncertain_return" && (c.CommitFault == "before" || c.CommitFault == "after"))
	case "checkpoint":
		valid = valid && c.CommitFault == "" && (c.Boundary == "before_checkpoint" || c.Boundary == "after_checkpoint" || c.Boundary == "checkpoint_return")
	default:
		valid = false
	}
	if !valid {
		return crashExpected{}, fmt.Errorf("unsupported crash journal cell: %+v", c)
	}
	want := crashExpected{}
	if c.Operation != "stage" || crashCommitted(c) {
		want.Images, want.Receipts, want.Sequence = 1, 1, 1
	}
	if c.Operation == "ack" && crashCommitted(c) {
		want.Ack = 1
	}
	return want, nil
}

func compareCrashState(want, got crashExpected) error {
	if got != want {
		return fmt.Errorf("atomic receipt/effect oracle: got %+v, want %+v", got, want)
	}
	return nil
}

func readEffectiveCrashState(t *testing.T, e *executor, reference ReceiptReference) (crashExpected, EffectiveStageBeginResult) {
	t.Helper()
	var state crashExpected
	for _, q := range []struct {
		text string
		out  *int64
	}{
		{"SELECT count(*) FROM catalog_credit_images", &state.Images},
		{"SELECT count(*) FROM enrichment_command_receipts", &state.Receipts},
		{"SELECT last_command_sequence FROM metadata_provider_operations WHERE id='operation-one'", &state.Sequence},
		{"SELECT acknowledged_command_sequence FROM metadata_provider_operations WHERE id='operation-one'", &state.Ack},
	} {
		if err := e.db.QueryRowContext(t.Context(), q.text).Scan(q.out); err != nil {
			t.Fatal(err)
		}
	}
	var serving sql.NullInt64
	if err := e.db.QueryRowContext(t.Context(), "SELECT credit_image_id FROM catalog_entities WHERE id=101").Scan(&serving); err != nil || serving.Valid {
		t.Fatal("hidden BEGIN published a serving head", serving, err)
	}
	var revision, source, identity, metadata, owner int64
	if err := e.db.QueryRowContext(t.Context(), "SELECT revision,source_revision,identity_revision,metadata_revision,credit_owner_revision FROM catalog_entities WHERE id=101").Scan(&revision, &source, &identity, &metadata, &owner); err != nil || revision != 203 || source != 13 || identity != 17 || metadata != 19 || owner != 23 {
		t.Fatal("hidden BEGIN changed serving/source witnesses", revision, source, identity, metadata, owner, err)
	}
	var attached sql.NullInt64
	if err := e.db.QueryRowContext(t.Context(), "SELECT credit_image_id FROM metadata_provider_operations WHERE id='operation-one'").Scan(&attached); err != nil || attached.Valid != (state.Images == 1) {
		t.Fatal("partial image/owner attachment", attached, state, err)
	}
	if state.Images == 1 {
		var sealed, members, staged int64
		var reclaim string
		if err := e.db.QueryRowContext(t.Context(), "SELECT sealed,member_count,build_staged_count,reclaim_state FROM catalog_credit_images WHERE id=?", attached.Int64).Scan(&sealed, &members, &staged, &reclaim); err != nil || sealed != 0 || members != 0 || staged != 0 || reclaim != "held" {
			t.Fatal("BEGIN image is not hidden/empty/held", sealed, members, staged, reclaim, err)
		}
	}
	var saved EffectiveStageBeginResult
	if state.Receipts != 0 {
		var incarnation, operation, digest, resultDigest []byte
		var sequence int64
		if err := e.db.QueryRowContext(t.Context(), `SELECT provider_operation_id,entity_id,accepted_revision,applied_ms,after_item,after_ordinal,
 effective_image_id,effective_image_revision,result_digest,command_incarnation,command_sequence,operation_id,request_digest
 FROM enrichment_command_receipts`).Scan(&saved.ProviderOperationID, &saved.EntityID, &saved.ObservedCatalogRevision, &saved.AppliedMS, &saved.AfterItem, &saved.NextOrdinal,
			&saved.ImageID, &saved.ImageRevision, &resultDigest, &incarnation, &sequence, &operation, &digest); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(incarnation, reference.Identity.Incarnation[:]) || sequence != 1 || !bytes.Equal(operation, reference.Identity.OperationID[:]) || !bytes.Equal(digest, reference.RequestDigest[:]) || len(resultDigest) != 32 {
			t.Fatal("lost original receipt identity/digest", reference, sequence)
		}
		copy(saved.Digest[:], resultDigest)
		if saved.ProviderOperationID != "operation-one" || saved.EntityID != 101 || saved.ObservedCatalogRevision != 203 || saved.AppliedMS != crashClock.UnixMilli() || saved.AfterItem != "opaque-page:61" || saved.NextOrdinal != 0 || saved.ImageID != attached.Int64 || saved.ImageRevision != 1 || saved.Digest == (ResultDigest{}) {
			t.Fatal("saved result violates independent source constants", saved)
		}
	}
	return state, saved
}

func verifyEffectiveCrash(t *testing.T, original *effectiveStageBeginCommand, witness crashWitness) {
	t.Helper()
	if witness.Reference != effectiveReference(original).Receipt || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("child used another original request or lost uncertain outcome", witness)
	}
	e := reopenCrashExecutor(t, witness.Path)
	want, err := expectedCrashState(witness.Case)
	if err != nil {
		t.Fatal(err)
	}
	got, saved := readEffectiveCrashState(t, e, witness.Reference)
	if err := compareCrashState(want, got); err != nil {
		t.Fatal(err)
	}
	if witness.Saved != (EffectiveStageBeginResult{}) && saved != witness.Saved {
		t.Fatal("restart lost the original scalar result", saved, witness.Saved)
	}
	w, cap, _, _, authority := bindEffectiveFixtureRuntime(t, e, original, e.config)
	// Reopening restores current disclosure and receipt access only. CR cannot
	// mint or copy W's fresh-source fixture permission onto a restart runtime.
	if cap.scope.fixture != nil {
		t.Fatal("restart unexpectedly acquired fresh-source fixture permission")
	}
	q := effectiveReference(original)
	resolved, err := w.ResolveEffective(t.Context(), cap, q)
	if want.Receipts == 0 {
		if err != nil || resolved.Outcome.State != Pending || resolved.Value != (EffectiveStageBeginResult{}) {
			t.Fatal("uncommitted prefix became success", resolved, err)
		}
		if ack, err := w.AckEffective(t.Context(), cap, q); !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(ack, AckResult{}) {
			t.Fatal("missing receipt was ACKed", ack, err)
		}
		refused, err := w.StageEffective(t.Context(), cap, original)
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(refused, EffectiveStageResult{}) {
			t.Fatal("restart admitted fresh work without W source rebinding", refused, err)
		}
		got, saved = readEffectiveCrashState(t, e, witness.Reference)
		if err := compareCrashState(want, got); err != nil || saved != (EffectiveStageBeginResult{}) {
			t.Fatal("gated restart retry changed effects or receipt", got, saved, err)
		}
		if authority.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
			t.Fatal("gated restart retry stranded physical custody")
		}
		return // fresh retry coverage needs W-owned reviewed source rebinding
	} else if want.Ack == 1 {
		if err != nil || resolved.Outcome.State != ResultRetired || resolved.Value != (EffectiveStageBeginResult{}) {
			t.Fatal("ACK floor resurrected Applied", resolved, err)
		}
	} else if err != nil || resolved.Outcome.State != Applied || !resolved.Outcome.Replayed || resolved.Value != saved {
		t.Fatal("saved result did not replay exactly", resolved, saved, err)
	}
	for _, mutate := range []func(*EnrichmentReceiptRequest){
		func(q *EnrichmentReceiptRequest) { q.Receipt.Identity.OperationID[0] ^= 1 },
		func(q *EnrichmentReceiptRequest) { q.Receipt.RequestDigest[0] ^= 1 },
	} {
		wrong := q
		mutate(&wrong)
		if result, err := w.ResolveEffective(t.Context(), cap, wrong); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(result, EffectiveStageResult{}) {
			t.Fatal("recovery disclosed another original request", result, err)
		}
		if ack, err := w.AckEffective(t.Context(), cap, wrong); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(ack, AckResult{}) {
			t.Fatal("recovery ACKed another original request", ack, err)
		}
	}
	cap.scope.revoked.Store(true)
	if result, err := w.ResolveEffective(t.Context(), cap, q); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(result, EffectiveStageResult{}) {
		t.Fatal("restart history bypassed current disclosure", result, err)
	}
	cap.scope.revoked.Store(false)
	// Source/lease/phase changes must not make original history depend on fresh
	// admission, nor turn an unsealed image into a serving publication.
	if _, err := e.db.ExecContext(t.Context(), "UPDATE metadata_provider_operations SET source_revision=source_revision+1,phase='superseded',lease_until='1900-01-01T00:00:00Z',after_item='changed-current-cursor' WHERE id='operation-one'"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		replayed, err := w.ResolveEffective(t.Context(), cap, q)
		if want.Ack == 1 {
			if err != nil || replayed.Outcome.State != ResultRetired || replayed.Value != (EffectiveStageBeginResult{}) {
				t.Fatal("retired replay fabricated a result", replayed, err)
			}
		} else if err != nil || replayed.Outcome.State != Applied || !replayed.Outcome.Replayed || replayed.Value != saved {
			t.Fatal("original replay changed immutable output", replayed, saved, err)
		}
	}
	got, after := readEffectiveCrashState(t, e, witness.Reference)
	if err := compareCrashState(want, got); err != nil || after != saved {
		t.Fatal("duplicate effects or rewritten receipt", got, after, err)
	}
	for range 2 {
		ack, err := w.AckEffective(t.Context(), cap, q)
		if err != nil || ack.AcknowledgedThrough != 1 {
			t.Fatal("restart ACK was not idempotent", ack, err)
		}
	}
	if authority.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("recovery stranded physical custody")
	}
}

func TestCrashOracleRejectsReceiptEffectMutants(t *testing.T) {
	want := crashExpected{1, 1, 1, 0}
	for _, mutant := range []crashExpected{{0, 1, 1, 0}, {1, 0, 1, 0}, {2, 1, 1, 0}, {1, 2, 1, 0}, {1, 1, 0, 0}, {1, 1, 1, 1}} {
		if compareCrashState(want, mutant) == nil {
			t.Fatal("oracle accepted corrupt state", mutant)
		}
	}
	for _, unsupported := range []crashCase{
		{"effective", "future_accept", "after_commit", ""},
		{"effective", "stage", "inside_fsync", ""},
		{"effective", "stage", "uncertain_return", "guess"},
		{"effective", "stage", "after_commit", "before"},
		{"inventory", "stage", "after_commit", ""},
	} {
		if _, err := expectedCrashState(unsupported); err == nil {
			t.Fatal("unsupported history became an expected empty/successful state", unsupported)
		}
	}
}

func verifyInventoryCrash(t *testing.T, q ScanReceiptRequest, profile effectivePlan, witness crashWitness) {
	t.Helper()
	if witness.Reference != q.Receipt || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("Inventory original receipt changed", witness.Reference, q.Receipt)
	}
	e := reopenCrashExecutor(t, witness.Path)
	w := bindCrashInventory(t, e, witness.Path, profile)
	cap := inventoryTestDisclosure(t, w, q)
	var last, floor, receipts, children int64
	for _, query := range []struct {
		text string
		out  *int64
	}{
		{"SELECT last_command_sequence FROM inventory_runs WHERE job_id=?", &last},
		{"SELECT acknowledged_command_sequence FROM inventory_runs WHERE job_id=?", &floor},
		{"SELECT count(*) FROM scan_command_receipts WHERE run_id=?", &receipts},
		{"SELECT count(*) FROM scan_command_results WHERE run_id=?", &children},
	} {
		if err := e.db.QueryRowContext(t.Context(), query.text, q.RunID).Scan(query.out); err != nil {
			t.Fatal(err)
		}
	}
	wantFloor := int64(0)
	if witness.Case.Operation == "ack" && crashCommitted(witness.Case) {
		wantFloor = 1
	}
	if last != 1 || floor != wantFloor || receipts != 1 || children != 1 {
		t.Fatal("Inventory receipt/effect/floor lost or partially reclaimed", last, floor, wantFloor, receipts, children)
	}
	for range 2 {
		result, err := w.ResolveScan(t.Context(), cap, q)
		result.Outcome.Evidence = nil
		if floor == 1 {
			if err != nil || result.Outcome.State != ResultRetired || result.RunID != "" || len(result.Members) != 0 {
				t.Fatal("Inventory ACK floor resurrected result", result, err)
			}
		} else if err != nil || !reflect.DeepEqual(result, witness.Inventory) || result.SourceRevision != "opaque:007" || result.NextCursor != "native-next" || result.AcceptedRunRevision != 2 || result.AppliedMS != 100 || len(result.Members) != 1 || result.Members[0] != (InventoryMember{0, 0, 0, "object-hit", 7, 31, 41, 1}) {
			t.Fatal("Inventory saved ordered cohort changed", result, witness.Inventory, err)
		}
	}
	for range 2 {
		ack, err := w.AckScan(t.Context(), cap, q)
		if err != nil || ack.AcknowledgedThrough != 1 {
			t.Fatal("Inventory ACK was not idempotent", ack, err)
		}
	}
	if err := e.db.QueryRowContext(t.Context(), "SELECT count(*) FROM scan_command_results WHERE run_id=?", q.RunID).Scan(&children); err != nil || children != 1 {
		t.Fatal("ACK partially reclaimed saved cohort", children, err)
	}
	if w.effective.authority.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("Inventory recovery stranded physical custody")
	}
}
