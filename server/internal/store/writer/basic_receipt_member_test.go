package writer

import (
	"errors"
	"testing"
)

func TestBasicReceiptMemberWholeEnvelope(t *testing.T) {
	q := BasicReceiptRequest{Purpose: BasicPurposeAccept, Scan: ScanReceiptRequest{RunID: "run", LibraryID: 7, LibraryKey: "lib"}}
	h := [7]int64{6, 3, 9, 0, 0, 0, 0}
	base, err := basicReceiptRequiredBytes(q, h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := basicReceiptMemberRequiredBytes(q, h, 11)
	if err != nil || got != base+8*(11+64) {
		t.Fatal(got, err)
	}
	for _, n := range []int64{-1, 0, maxCanonicalResultBytes} {
		if got, err = basicReceiptMemberRequiredBytes(q, h, n); got != 0 || err == nil {
			t.Fatal(got, err)
		}
	}
	q.Purpose = BasicPurposeStage
	if got, err = basicReceiptMemberRequiredBytes(q, h, 1); got != 0 || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(got, err)
	}
}
func TestBasicReceiptMemberNativeCompleteCohort(t *testing.T) {
	for _, scenario := range []string{"exact", "wrong_key", "extra_member", "missing", "foreign_library", "byte_capacity"} {
		t.Run(scenario, func(t *testing.T) {
			w, _, original, identity := basicControlFixture(t)
			seedBasicFirstOrdinaryOwner(t, w)
			// Synthetic immutable SQL facts, not an Applied receipt or codec proof.
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO scan_command_receipts(run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,source_id,source_incarnation,source_revision,next_cursor,result_count,result_digest,change_id,expected_epoch,next_family,next_copy_phase,next_copy_id,next_copy_aux,next_copy_key,next_copy_key2,next_copy_key3,after_entity) VALUES(?,?,?,?,?,'basic_accept',42,2000,'basic-source','basic-incarnation','object:019','',1,?,51,17,0,0,0,0,'','','',31)`, original.scope.runID, identity.Incarnation[:], identity.Sequence, identity.OperationID[:], make([]byte, 32), make([]byte, 32)); err != nil {
				t.Fatal(err)
			}
			q := BasicReceiptRequest{Purpose: BasicPurposeAccept, Scan: ScanReceiptRequest{RunID: original.scope.runID, LibraryID: 7, LibraryKey: "basic-library", Receipt: ReceiptReference{Identity: identity}}}
			if scenario != "missing" {
				input := int64(0)
				if scenario == "wrong_key" {
					input = 1
				}
				if scenario == "foreign_library" {
					q.Scan.LibraryID = 8
				}
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO scan_command_results VALUES(?,?,?,?,0,0,'observed'||char(0)||'原',7,31,41,18)`, q.Scan.RunID, identity.Incarnation[:], identity.Sequence, input); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "extra_member" {
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO scan_command_results VALUES(?,?,?,1,0,0,'extra',7,31,41,18)`, q.Scan.RunID, identity.Incarnation[:], identity.Sequence); err != nil {
					t.Fatal(err)
				}
			}
			defs := basicReceiptMemberDefinitions()
			registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_receipt_member.go", "basic_receipt_member_test.go")
			turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
			header := [7]int64{12, 17, 10, 0, 0, 0, 0}
			if scenario == "byte_capacity" {
				header[0] = maxCanonicalResultBytes
			}
			got, err := readBasicReceiptMember(t.Context(), turn, q, header)
			if scenario == "exact" {
				if err != nil || got.ObjectID != "observed\x00原" || got.AcceptedAssetRevision != 18 {
					t.Fatal(got, err)
				}
			} else if err == nil || got != (InventoryMember{}) {
				t.Fatal("partial cohort escaped", got, err)
			}
		})
	}
}
