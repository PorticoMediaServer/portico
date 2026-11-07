package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
	"testing"
)

func mpReclaimDigestFixture() mpReclaimResult {
	return mpReclaimResult{ownerID: "queue", identity: CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 2}, kind: 786, request: RequestDigest{3}, appliedMS: 1000, capturedACK: 1, beforeCursor: 0, afterCursor: 1, removedSequence: 1, removedRows: 1, removedBytes: 10, beforeRows: 2, beforeBytes: 50, remainingRows: 1, remainingBytes: 40}
}
func TestMPReclaimOriginalCommitment(t *testing.T) {
	r := mpReclaimDigestFixture()
	original, err := mpReclaimDigest(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*mpReclaimResult){func(r *mpReclaimResult) { r.ownerID = "other" }, func(r *mpReclaimResult) { r.identity.OperationID[0]++ }, func(r *mpReclaimResult) { r.request[0]++ }, func(r *mpReclaimResult) { r.appliedMS++ }, func(r *mpReclaimResult) { r.removedBytes++; r.remainingBytes-- }, func(r *mpReclaimResult) { r.capturedACK++; r.identity.Sequence++ }} {
		altered := r
		change(&altered)
		got, err := mpReclaimDigest(altered)
		if err != nil || got == original {
			t.Fatalf("lost original result commitment: %+v %v", altered, err)
		}
	}
	// Native control-byte observations are admission/accounting data, not fields
	// reconstructed from today's root to validate an old result.
	r.oldControlBytes = 300
	r.newControlBytes = 400
	again, err := mpReclaimDigest(r)
	if err != nil || again != original {
		t.Fatal("mutable control observation changed saved result", err)
	}
}
func TestMPReclaimRejectsWrongOwnerCohortAndRefund(t *testing.T) {
	for _, change := range []func(*mpReclaimResult){func(r *mpReclaimResult) { r.kind = 780 }, func(r *mpReclaimResult) { r.removedSequence = 0 }, func(r *mpReclaimResult) { r.removedRows = 2; r.remainingRows = 0 }, func(r *mpReclaimResult) { r.remainingBytes-- }, func(r *mpReclaimResult) { r.removedBytes = 0 }, func(r *mpReclaimResult) { r.afterCursor = 2 }} {
		r := mpReclaimDigestFixture()
		change(&r)
		if _, err := mpReclaimDigest(r); err == nil {
			t.Fatalf("accepted wrong owner/cohort/debt: %+v", r)
		}
	}
}

func TestMPReclaimFinalFClosedCatalog(t *testing.T) {
	for _, defs := range [][]stmt.Definition{mpReceiptAckDefinitions(), mpReceiptReclaimDefinitions()} {
		if _, err := stmt.Describe(defs); err != nil {
			t.Fatal(err)
		}
		for _, d := range defs {
			if len(d.Params) != strings.Count(d.SQL, "?") {
				t.Fatal(d.ID, "arity")
			}
			for _, obsolete := range []string{"child_kind", "child_ordinal", "receipt_segments", "receipt_retire_media", "receipt_intervals", "mp_reclaim_finished", "mp_reclaim_result_rows"} {
				if strings.Contains(d.SQL, obsolete) {
					t.Fatal(d.ID, obsolete)
				}
			}
			if strings.Contains(string(d.ID), ".ack") && (strings.Contains(d.SQL, "mp_reclaim_result_payload=") || strings.Contains(d.SQL, "DELETE")) {
				t.Fatal("ACK changed fixed slot")
			}
			if strings.HasSuffix(string(d.ID), ".store") {
				for _, f := range []string{"mp_owner_revision IS ?", "mp_command_last_sequence IS ?", "mp_command_acknowledged_sequence IS ?", "mp_reclaim_sequence IS ?", "mp_reclaim_result_digest IS ?", "mp_control_variable_bytes=?"} {
					if !strings.Contains(d.SQL, f) {
						t.Fatal(d.ID, f)
					}
				}
			}
		}
	}
}
