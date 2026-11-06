package writer

import (
	"bytes"
	"context"
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

// This journal contains every saved header/child column, including fields not
// exposed by the current Inventory projection. It is captured from raw SQL in
// the parent's original fixture, never from ResolveScan or its decoder/codec.
type inventoryCrashHeader struct {
	RunID                                                             string
	Incarnation                                                       []byte
	Sequence                                                          int64
	OperationID, RequestDigest                                        []byte
	Kind                                                              string
	AcceptedRevision, AppliedMS                                       int64
	SourceID, SourceIncarnation, SourceRevision                       string
	ChangeID                                                          sql.NullInt64
	NextCursor                                                        string
	NextFamily, NextCopyPhase, NextCopyID                             int64
	NextCopyAux                                                       int64
	NextCopyKey, NextCopyKey2, NextCopyKey3                           string
	AfterEntity, ExpectedEpoch, ResultCount                           int64
	ResultDigest                                                      []byte
	RefusedRelativePath                                               []byte
	RefusalCode, CursorKind                                           sql.NullString
	CursorDirectoryPath, CursorDirectoryIdentity                      []byte
	CursorDirectoryRevision, CursorExpectedPayload, CursorNextPayload []byte
}

type inventoryCrashChild struct {
	RunID                                                string
	Incarnation                                          []byte
	Sequence                                             int64
	InputOrdinal, ChildKind, ChildOrdinal                int64
	ObjectID                                             string
	LibraryID, SubjectID, AssetID, AcceptedAssetRevision int64
}

type inventoryCrashJournal struct {
	Header   inventoryCrashHeader
	Children []inventoryCrashChild
}

type inventoryCrashReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readInventoryCrashJournal(ctx context.Context, reader inventoryCrashReader, run string) (out inventoryCrashJournal, err error) {
	h := &out.Header
	err = reader.QueryRowContext(ctx, `SELECT run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,
 accepted_revision,applied_ms,source_id,source_incarnation,source_revision,change_id,next_cursor,
 next_family,next_copy_phase,next_copy_id,next_copy_aux,next_copy_key,next_copy_key2,next_copy_key3,
 after_entity,expected_epoch,result_count,result_digest,refused_relative_path,refusal_code,cursor_kind,
 cursor_directory_path,cursor_directory_identity,cursor_directory_revision,cursor_expected_payload,cursor_next_payload
 FROM scan_command_receipts WHERE run_id=?`, run).Scan(
		&h.RunID, &h.Incarnation, &h.Sequence, &h.OperationID, &h.RequestDigest, &h.Kind,
		&h.AcceptedRevision, &h.AppliedMS, &h.SourceID, &h.SourceIncarnation, &h.SourceRevision, &h.ChangeID, &h.NextCursor,
		&h.NextFamily, &h.NextCopyPhase, &h.NextCopyID, &h.NextCopyAux, &h.NextCopyKey, &h.NextCopyKey2, &h.NextCopyKey3,
		&h.AfterEntity, &h.ExpectedEpoch, &h.ResultCount, &h.ResultDigest, &h.RefusedRelativePath, &h.RefusalCode, &h.CursorKind,
		&h.CursorDirectoryPath, &h.CursorDirectoryIdentity, &h.CursorDirectoryRevision, &h.CursorExpectedPayload, &h.CursorNextPayload)
	if err != nil {
		return out, err
	}
	rows, err := reader.QueryContext(ctx, `SELECT run_id,command_incarnation,command_sequence,input_ordinal,child_kind,child_ordinal,
 object_id,library_id,subject_id,asset_id,accepted_asset_revision FROM scan_command_results WHERE run_id=?
 ORDER BY command_incarnation,command_sequence,input_ordinal,child_kind,child_ordinal`, run)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var child inventoryCrashChild
		if err = rows.Scan(&child.RunID, &child.Incarnation, &child.Sequence, &child.InputOrdinal, &child.ChildKind, &child.ChildOrdinal,
			&child.ObjectID, &child.LibraryID, &child.SubjectID, &child.AssetID, &child.AcceptedAssetRevision); err != nil {
			return out, err
		}
		out.Children = append(out.Children, child)
	}
	return out, rows.Err()
}

func compareInventoryCrashJournal(want, got inventoryCrashJournal) error {
	if !reflect.DeepEqual(want.Header, got.Header) {
		return fmt.Errorf("saved Inventory header changed: got %+v, want %+v", got.Header, want.Header)
	}
	if len(got.Children) != len(want.Children) {
		return fmt.Errorf("saved Inventory child count %d, want %d", len(got.Children), len(want.Children))
	}
	for i, child := range got.Children {
		if !reflect.DeepEqual(child, want.Children[i]) {
			return fmt.Errorf("saved Inventory child %d changed: got %+v, want %+v", i, child, want.Children[i])
		}
	}
	return nil
}

// Independently project only raw saved columns and the journal's ACK decision.
// Do not use inventoryHistoricalResult, a production decoder or result codec.
func inventoryCrashProjection(saved inventoryCrashJournal, retired bool) (InventoryResult, error) {
	h := saved.Header
	if h.Kind != "inventory_accept" || len(h.Incarnation) != 16 || len(h.OperationID) != 16 || len(h.RequestDigest) != 32 || len(h.ResultDigest) != 32 ||
		h.ResultCount <= 0 || h.ResultCount != int64(len(saved.Children)) {
		return InventoryResult{}, fmt.Errorf("unsupported Inventory journal projection")
	}
	var reference ReceiptReference
	copy(reference.Identity.Incarnation[:], h.Incarnation)
	reference.Identity.Sequence = h.Sequence
	copy(reference.Identity.OperationID[:], h.OperationID)
	copy(reference.RequestDigest[:], h.RequestDigest)
	if retired {
		// Every payload field and Replayed must be zero, even while all saved
		// physical bytes remain. Evidence describes this call, checked outside.
		return InventoryResult{Outcome: ReceiptOutcome{State: ResultRetired, Reference: reference}}, nil
	}
	out := InventoryResult{Outcome: ReceiptOutcome{State: Applied, Reference: reference, Replayed: true},
		RunID: h.RunID, AcceptedRunRevision: h.AcceptedRevision, AppliedMS: h.AppliedMS,
		SourceID: h.SourceID, SourceIncarnation: h.SourceIncarnation, SourceRevision: h.SourceRevision, NextCursor: h.NextCursor, ResultCount: h.ResultCount}
	copy(out.Digest[:], h.ResultDigest)
	for _, c := range saved.Children {
		if c.RunID != h.RunID || !bytes.Equal(c.Incarnation, h.Incarnation) || c.Sequence != h.Sequence {
			return InventoryResult{}, fmt.Errorf("Inventory child belongs to another receipt")
		}
		out.Members = append(out.Members, InventoryMember{InputOrdinal: c.InputOrdinal, ChildKind: c.ChildKind, ChildOrdinal: c.ChildOrdinal,
			ObjectID: c.ObjectID, LibraryID: c.LibraryID, SubjectID: c.SubjectID, AssetID: c.AssetID, AcceptedAssetRevision: c.AcceptedAssetRevision})
	}
	return out, nil
}

func compareInventoryCrashProjection(want, got InventoryResult) error {
	got.Outcome.Evidence = nil // actual per-call evidence is not immutable result data
	if !reflect.DeepEqual(want, got) {
		return fmt.Errorf("Inventory projection changed: got %+v, want %+v", got, want)
	}
	return nil
}

func verifyInventoryCrash(t *testing.T, q ScanReceiptRequest, profile effectivePlan, journal inventoryCrashJournal, witness crashWitness) {
	t.Helper()
	if witness.Reference != q.Receipt || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("Inventory original receipt changed", witness.Reference, q.Receipt)
	}
	e := reopenCrashExecutor(t, witness.Path)
	saved, err := readInventoryCrashJournal(t.Context(), e.db, q.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareInventoryCrashJournal(journal, saved); err != nil {
		t.Fatal(err) // checked even when the ACK floor forbids decoding the payload
	}
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
	want, err := inventoryCrashProjection(saved, wantFloor == 1)
	if err != nil || want.Outcome.Reference != q.Receipt {
		t.Fatal("raw saved original reference changed", want, err)
	}
	for range 2 {
		result, err := w.ResolveScan(t.Context(), cap, q)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareInventoryCrashProjection(want, result); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		ack, err := w.AckScan(t.Context(), cap, q)
		if err != nil || ack.AcknowledgedThrough != 1 {
			t.Fatal("Inventory ACK was not idempotent", ack, err)
		}
	}
	after, err := readInventoryCrashJournal(t.Context(), e.db, q.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareInventoryCrashJournal(journal, after); err != nil {
		t.Fatal(err) // ACK may not erase or corrupt any saved header/child column
	}
	retired, err := inventoryCrashProjection(after, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := w.ResolveScan(t.Context(), cap, q)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareInventoryCrashProjection(retired, result); err != nil {
		t.Fatal(err)
	}
	if w.effective.authority.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("Inventory recovery stranded physical custody")
	}
}

func TestCrashInventoryOracleRejectsProjectionDrift(t *testing.T) {
	w, requests, e, _ := inventoryFacadeFixture(t)
	q := requests["hit"]
	cap := inventoryTestDisclosure(t, w, q)
	saved, err := readInventoryCrashJournal(t.Context(), e.db, q.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []bool{false, true} {
		if retired {
			ack, err := w.AckScan(t.Context(), cap, q)
			if err != nil || ack.AcknowledgedThrough != 1 {
				t.Fatal(ack, err)
			}
		}
		want, err := inventoryCrashProjection(saved, retired)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := w.ResolveScan(t.Context(), cap, q)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareInventoryCrashProjection(want, actual); err != nil {
			t.Fatal(err)
		}
		for name, mutate := range map[string]func(*InventoryResult){
			"run":                func(v *InventoryResult) { v.RunID = "WRONG-RUN" },
			"accepted_revision":  func(v *InventoryResult) { v.AcceptedRunRevision++ },
			"applied_ms":         func(v *InventoryResult) { v.AppliedMS++ },
			"source":             func(v *InventoryResult) { v.SourceID = "WRONG-PROJECTION" },
			"source_incarnation": func(v *InventoryResult) { v.SourceIncarnation = "WRONG-INCARNATION" },
			"source_revision":    func(v *InventoryResult) { v.SourceRevision = "WRONG-REVISION" },
			"cursor":             func(v *InventoryResult) { v.NextCursor = "WRONG-CURSOR" },
			"count":              func(v *InventoryResult) { v.ResultCount++ },
			"digest":             func(v *InventoryResult) { v.Digest[0] ^= 1 },
			"outcome":            func(v *InventoryResult) { v.Outcome.State = Pending },
			"reference":          func(v *InventoryResult) { v.Outcome.Reference.RequestDigest[0] ^= 1 },
			"replayed":           func(v *InventoryResult) { v.Outcome.Replayed = !v.Outcome.Replayed },
			"members":            func(v *InventoryResult) { v.Members = []InventoryMember{{ObjectID: "WRONG-CHILD"}} },
		} {
			changed := actual
			mutate(&changed)
			if compareInventoryCrashProjection(want, changed) == nil {
				t.Fatalf("retired=%v oracle accepted %s projection corruption", retired, name)
			}
		}
	}
}

func TestCrashInventoryOracleRejectsRetiredCorruption(t *testing.T) {
	for name, mutation := range map[string]string{
		"child_object": "UPDATE scan_command_results SET object_id='CORRUPTED-ORIGINAL' WHERE run_id=?",
		"source":       "UPDATE scan_command_receipts SET source_id='CORRUPTED-SOURCE' WHERE run_id=?",
		"digest":       "UPDATE scan_command_receipts SET result_digest=zeroblob(32) WHERE run_id=?",
	} {
		t.Run(name, func(t *testing.T) {
			w, requests, e, _ := inventoryFacadeFixture(t)
			q := requests["hit"]
			journal, err := readInventoryCrashJournal(t.Context(), e.db, q.RunID)
			if err != nil {
				t.Fatal(err)
			}
			ack, err := w.AckScan(t.Context(), inventoryTestDisclosure(t, w, q), q)
			if err != nil || ack.AcknowledgedThrough != 1 {
				t.Fatal(ack, err)
			}
			written, err := e.db.ExecContext(t.Context(), mutation, q.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if rows, err := written.RowsAffected(); err != nil || rows != 1 {
				t.Fatal("corruption control did not modify exactly one saved row", rows, err)
			}
			if err := e.close(); err != nil {
				t.Fatal(err)
			}
			// These corruptions satisfy layout/integrity/FKs and are below the
			// ACK floor. Only exact raw-journal comparison detects the loss.
			survivor := reopenCrashExecutor(t, w.effective.path)
			got, err := readInventoryCrashJournal(t.Context(), survivor.db, q.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if compareInventoryCrashJournal(journal, got) == nil {
				t.Fatal("retired saved payload corruption escaped raw journal", name)
			}
		})
	}
}
