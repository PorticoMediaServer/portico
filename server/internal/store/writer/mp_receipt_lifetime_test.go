package writer

import "testing"

func mpReclaimDigestFixture() mpReclaimResult {
	return mpReclaimResult{ownerID: "queue", identity: CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 2}, kind: 786, request: RequestDigest{3}, appliedMS: 1000, capturedACK: 1, beforeCursor: 0, afterCursor: 0, beforeOrdinal: -1, afterChild: "segments", afterOrdinal: 0, removedRows: 1, removedBytes: 10, beforeRows: 2, beforeBytes: 50, remainingRows: 1, remainingBytes: 40}
}
func TestMPReclaimOriginalCommitment(t *testing.T) {
	r := mpReclaimDigestFixture()
	original, err := mpReclaimDigest(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*mpReclaimResult){func(r *mpReclaimResult) { r.ownerID = "other" }, func(r *mpReclaimResult) { r.identity.OperationID[0]++ }, func(r *mpReclaimResult) { r.request[0]++ }, func(r *mpReclaimResult) { r.afterOrdinal++ }, func(r *mpReclaimResult) { r.removedBytes++; r.remainingBytes-- }, func(r *mpReclaimResult) { r.capturedACK++ }} {
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
	for _, change := range []func(*mpReclaimResult){func(r *mpReclaimResult) { r.kind = 782 }, func(r *mpReclaimResult) { r.afterChild = "arbitrary" }, func(r *mpReclaimResult) { r.removedRows = 2; r.remainingRows = 0 }, func(r *mpReclaimResult) { r.remainingBytes-- }, func(r *mpReclaimResult) { r.finished = true }, func(r *mpReclaimResult) { r.afterCursor = 2 }} {
		r := mpReclaimDigestFixture()
		change(&r)
		if _, err := mpReclaimDigest(r); err == nil {
			t.Fatalf("accepted wrong owner/cohort/debt: %+v", r)
		}
	}
}
func TestMPBirthRetirementConcreteDomain(t *testing.T) {
	r := mpBirthRetirementEffect{family: "queue", birthID: [16]byte{1}, identity: CommandIdentity{Incarnation: [16]byte{2}, OperationID: [16]byte{3}, Sequence: 2}, request: RequestDigest{4}, appliedMS: 1000, scopeCutoffMS: 900}
	q, err := mpBirthRetirementDigest(r)
	if err != nil {
		t.Fatal(err)
	}
	r.family = "job"
	j, err := mpBirthRetirementDigest(r)
	if err != nil || j == q {
		t.Fatal("cross-owner birth result commitment", err)
	}
	r.family = "generic"
	if _, err = mpBirthRetirementDigest(r); err == nil {
		t.Fatal("accepted unknown birth family")
	}
	r.family = "queue"
	r.identity.Sequence = 1
	if _, err = mpBirthRetirementDigest(r); err == nil {
		t.Fatal("retirement reused creation sequence")
	}
}
