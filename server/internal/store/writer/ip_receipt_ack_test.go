package writer

import (
	"errors"
	"testing"
)

func TestIPReceiptACKPolicyFloorOnlyAndRetiredRetry(t *testing.T) {
	w := ipPolicyFixture(t)
	r := ipPolicyTestRequest()
	turn := ipPolicyTestTurn(t, w, IPPolicySet)
	result, err := ipPolicySetBody(t.Context(), turn, r, ipPolicyTestDecode())
	if err != nil {
		t.Fatal(err)
	}
	ipPolicyTestCommit(t, turn)
	request, err := r.digest()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := result.digest(r.identity, request)
	if err != nil {
		t.Fatal(err)
	}
	q := ipReceiptACKRequest{family: ipReceiptPolicy, libraryID: r.library, provider: r.provider, reference: ReceiptReference{r.identity, request}, sourceKind: IPPolicySet, resultDigest: ResultDigest(digest), appliedMS: 200000}
	defs := append(ipPolicyDefinitions(), ipReceiptACKDefinitions()...)
	// Bad saved-result proof cannot advance the floor or make cleanup due.
	bad := q
	bad.resultDigest[0] ^= 1
	ack := ipPolicyTestTurn(t, w, IPReceiptACK, defs)
	floor, err := acknowledgeIPReceipt(t.Context(), ack, bad)
	if !errors.Is(err, ErrConflict) || floor != 0 {
		t.Fatal(floor, err)
	}
	_, _ = ack.transaction.rollback(err)
	if got := ipPolicyTestScalar(t, w, `SELECT acknowledged_command_sequence FROM metadata_provider_policy`); got != 0 {
		t.Fatal(got)
	}
	ack = ipPolicyTestTurn(t, w, IPReceiptACK, defs)
	floor, err = acknowledgeIPReceipt(t.Context(), ack, q)
	if err != nil || floor != 1 {
		t.Fatal(floor, err)
	}
	ipPolicyTestCommit(t, ack)
	var nativeFloor, due, receipts, revision int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT acknowledged_command_sequence,receipt_reclaim_due_ms,(SELECT count(*) FROM metadata_policy_receipts),revision FROM metadata_provider_policy`).Scan(&nativeFloor, &due, &receipts, &revision); err != nil || nativeFloor != 1 || due != 200000 || receipts != 1 || revision != 6 {
		t.Fatal(nativeFloor, due, receipts, revision, err)
	}
	ipPolicyTestExec(t, w, `DELETE FROM metadata_policy_receipts`)
	ipPolicyTestExec(t, w, `UPDATE metadata_provider_policy SET receipt_reclaim_after_sequence=1,receipt_reclaim_due_ms=NULL`)
	retry := ipPolicyTestTurn(t, w, IPReceiptACK, defs)
	floor, err = acknowledgeIPReceipt(t.Context(), retry, q)
	if err != nil || floor != 1 {
		t.Fatal(floor, err)
	}
	ipPolicyTestCommit(t, retry)
	if got := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_provider_policy WHERE receipt_reclaim_due_ms IS NULL`); got != 1 {
		t.Fatal("ACK retry redue", got)
	}
}
