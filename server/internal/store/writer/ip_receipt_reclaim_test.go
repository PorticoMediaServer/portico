package writer

import (
	"fmt"
	"reflect"
	"testing"
)

func ipReclaimPolicySource(t *testing.T, w *executor, r ipPolicyRequest) {
	t.Helper()
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
	q := ipReceiptACKRequest{family: ipReceiptPolicy, libraryID: r.library, provider: r.provider, reference: ReceiptReference{r.identity, request}, sourceKind: IPPolicySet, resultDigest: ResultDigest(digest), appliedMS: r.nowMS}
	ack := ipPolicyTestTurn(t, w, IPReceiptACK, ipReceiptACKDefinitions())
	floor, err := acknowledgeIPReceipt(t.Context(), ack, q)
	if err != nil || floor != r.identity.Sequence {
		t.Fatal(floor, err)
	}
	ipPolicyTestCommit(t, ack)
}
func ipReclaimDo(t *testing.T, w *executor, q ipReceiptReclaimRequest) (ipReceiptReclaimNative, *commandTurn, error) {
	t.Helper()
	c, err := newIPReceiptReclaimCommand(q)
	if err != nil {
		t.Fatal(err)
	}
	turn := ipPolicyTestTurn(t, w, IPReceiptReclaim, ipReceiptReclaimDefinitions())
	out, err := applyIPReceiptReclaim(t.Context(), turn, c, 300000)
	return out, turn, err
}
func ipReclaimACK(t *testing.T, w *executor, q ipReceiptReclaimRequest, out ipReceiptReclaimNative) {
	t.Helper()
	c, err := newIPReceiptReclaimCommand(q)
	if err != nil {
		t.Fatal(err)
	}
	turn := ipPolicyTestTurn(t, w, IPReceiptACK, ipReceiptReclaimDefinitions())
	floor, err := acknowledgeIPReceiptReclaim(t.Context(), turn, q.Scope, ReceiptReference{q.Identity, c.digest}, out.digest)
	if err != nil || floor != q.Identity.Sequence {
		t.Fatal(floor, err)
	}
	ipPolicyTestCommit(t, turn)
}
func TestIPReceiptReclaimFixedSlotNativeReplayAndNoRecursiveDebt(t *testing.T) {
	w := ipPolicyFixture(t)
	source := ipPolicyTestRequest()
	ipReclaimPolicySource(t, w, source)
	q := ipReceiptReclaimRequest{Identity: CommandIdentity{Incarnation: source.identity.Incarnation, Sequence: 2, OperationID: [16]byte{22}}, Scope: ipRetentionScope{Family: ipReceiptPolicy, LibraryID: 7, Provider: "tmdb"}, CapturedACK: 1}
	got, turn, err := ipReclaimDo(t, w, q)
	if err != nil || got.decision != receiptFresh || got.value.RemovedRows != 1 || got.value.RemovedPayloadBytes <= 0 || got.value.AfterSequence != 1 || !got.value.NoNext {
		t.Fatal(got, err)
	}
	ipPolicyTestCommit(t, turn)
	if n := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_policy_receipts`); n != 0 {
		t.Fatal("cleanup created another child", n)
	}
	if n := ipPolicyTestScalar(t, w, `SELECT revision FROM metadata_provider_policy`); n != 6 {
		t.Fatal("cleanup changed policy semantics", n)
	}
	replay, turn, err := ipReclaimDo(t, w, q)
	if err != nil || replay.decision != receiptReplay || replay.value != got.value || replay.digest != got.digest {
		t.Fatal(replay, err)
	}
	ipPolicyTestCommit(t, turn)
	ipReclaimACK(t, w, q, got)
	if n := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_provider_policy WHERE acknowledged_command_sequence=2 AND receipt_reclaim_due_ms IS NULL AND reclaim_command_sequence=2`); n != 1 {
		t.Fatal("cleanup ACK redued itself", n)
	}
	// A new real source command can follow the ACKed fixed slot. Its cleanup
	// overwrites that old slot only after the old result was acknowledged.
	source.identity.Sequence = 3
	source.identity.OperationID = [16]byte{33}
	source.revision = 6
	source.values.region = "GB"
	source.nowMS = 200000
	ipReclaimPolicySource(t, w, source)
	oldCommand, err := newIPReceiptReclaimCommand(q)
	if err != nil {
		t.Fatal(err)
	}
	previous := ipReceiptReclaimSlot{q.Identity, oldCommand.digest, got.digest, got.value}
	next := ipReceiptReclaimRequest{Identity: CommandIdentity{Incarnation: q.Identity.Incarnation, Sequence: 4, OperationID: [16]byte{44}}, Scope: q.Scope, CapturedACK: 3, AfterSequence: 1, Previous: &previous}
	newer, turn, err := ipReclaimDo(t, w, next)
	if err != nil || newer.value.RemovedRows != 1 || newer.value.AfterSequence != 3 || !newer.value.NoNext {
		t.Fatal(newer, err)
	}
	ipPolicyTestCommit(t, turn)
	retired, turn, err := ipReclaimDo(t, w, q)
	if err != nil || retired.decision != receiptRetired || retired.value != (ipReceiptReclaimResult{}) {
		t.Fatal(retired, err)
	}
	ipPolicyTestCommit(t, turn)
	ipReclaimACK(t, w, next, newer)
	if n := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_policy_receipts`); n != 0 {
		t.Fatal(n)
	}
	if n := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_provider_policy WHERE last_command_sequence=4 AND acknowledged_command_sequence=4 AND reclaim_command_sequence=4 AND receipt_reclaim_due_ms IS NULL AND revision=7`); n != 1 {
		t.Fatal(n)
	}
}
func TestIPReceiptReclaimTerminalRollbackRetainsSourceDebt(t *testing.T) {
	w := ipPolicyFixture(t)
	source := ipPolicyTestRequest()
	ipReclaimPolicySource(t, w, source)
	ipPolicyTestExec(t, w, `CREATE TRIGGER ip_reclaim_fail BEFORE UPDATE OF reclaim_command_sequence ON metadata_provider_policy BEGIN SELECT RAISE(ABORT,'slot');END`)
	q := ipReceiptReclaimRequest{Identity: CommandIdentity{Incarnation: source.identity.Incarnation, Sequence: 2, OperationID: [16]byte{22}}, Scope: ipRetentionScope{Family: ipReceiptPolicy, LibraryID: 7, Provider: "tmdb"}, CapturedACK: 1}
	got, turn, err := ipReclaimDo(t, w, q)
	if err == nil || !reflect.DeepEqual(got, ipReceiptReclaimNative{}) {
		t.Fatal(got, err)
	}
	_, _ = turn.transaction.rollback(err)
	if n := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_policy_receipts`); n != 1 {
		t.Fatal("source debt lost", n)
	}
	if n := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_provider_policy WHERE last_command_sequence=1 AND receipt_reclaim_after_sequence=0 AND reclaim_command_sequence IS NULL AND receipt_reclaim_due_ms IS NOT NULL`); n != 1 {
		t.Fatal(n)
	}
}

func ipReclaimTextSourceACK(t *testing.T, w *executor, q ipTextBundleRequest, out ipTextBundleNative) {
	t.Helper()
	c, err := newIPTextBundleCommand(q)
	if err != nil {
		t.Fatal(err)
	}
	ack := ipPolicyTestTurn(t, w, IPReceiptACK, ipReceiptACKDefinitions())
	floor, err := acknowledgeIPReceipt(t.Context(), ack, ipReceiptACKRequest{family: ipReceiptText, ownerID: q.Source.OwnerID, reference: ReceiptReference{q.Identity, c.digest}, sourceKind: q.Kind, resultDigest: out.digest, appliedMS: 1234})
	if err != nil || floor != q.Identity.Sequence {
		t.Fatal(floor, err)
	}
	ipPolicyTestCommit(t, ack)
}
func TestIPReceiptReclaimTextResultChildrenAreDeletedBeforeHeader(t *testing.T) {
	w, q := ipBundleFixture(t)
	out, turn, err := ipBundleDo(t, w, q)
	if err != nil {
		t.Fatal(err)
	}
	ipPolicyTestCommit(t, turn)
	ipReclaimTextSourceACK(t, w, q, out)
	cleanup := ipReceiptReclaimRequest{Identity: CommandIdentity{Incarnation: q.Identity.Incarnation, Sequence: 2, OperationID: [16]byte{22}}, Scope: ipRetentionScope{Family: ipReceiptText, OwnerID: 11}, CapturedACK: 1}
	got, turn, err := ipReclaimDo(t, w, cleanup)
	if err != nil || got.value.RemovedRows != 3 || !got.value.NoNext || got.value.RemovedPayloadBytes <= 0 {
		t.Fatal(got, err)
	}
	ipPolicyTestCommit(t, turn)
	if n := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_edit_receipts`); n != 0 {
		t.Fatal(n)
	}
	if n := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_edit_receipt_fields`); n != 0 {
		t.Fatal(n)
	}
	if n := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_owner_history_fields`); n != 2 {
		t.Fatal("cleanup refunded unrelated history", n)
	}
	if n := ipPolicyTestScalar(t, w, `SELECT revision FROM catalog_entities WHERE id=101`); n != 24 {
		t.Fatal(n)
	}
}
func TestIPReceiptReclaimLiveBulkPinPreservesResultChildren(t *testing.T) {
	w, q := ipBulkReady(t)
	_, turn, err := ipBulkApplyDo(t, w, q)
	if err != nil {
		t.Fatal(err)
	}
	ipPolicyTestCommit(t, turn)
	child := q.Edits[0]
	saved, turn, err := ipBundleDo(t, w, child)
	if err != nil || saved.decision != receiptReplay {
		t.Fatal(saved, err)
	}
	ipPolicyTestCommit(t, turn)
	ipReclaimTextSourceACK(t, w, child, saved)
	cleanup := ipReceiptReclaimRequest{Identity: CommandIdentity{Incarnation: child.Identity.Incarnation, Sequence: 2, OperationID: [16]byte{22}}, Scope: ipRetentionScope{Family: ipReceiptText, OwnerID: 11}, CapturedACK: 1}
	got, turn, err := ipReclaimDo(t, w, cleanup)
	if err != nil || got.value.RemovedRows != 0 || got.value.RemovedPayloadBytes != 0 || got.value.AfterSequence != 0 || got.value.NoNext {
		t.Fatal(got, err)
	}
	ipPolicyTestCommit(t, turn)
	if n := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_edit_receipts`); n != 1 {
		t.Fatal(n)
	}
	if n := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_edit_receipt_fields`); n != 1 {
		t.Fatal(n)
	}
	if n := ipPolicyTestScalar(t, w, `SELECT receipt_reclaim_due_ms FROM metadata_edit_owners WHERE id=11`); n != 301000 {
		t.Fatal(n)
	}
}
func TestIPReceiptReclaimIndependentResultWire(t *testing.T) {
	var i CommandIdentity
	var q RequestDigest
	for n := range i.Incarnation {
		i.Incarnation[n] = byte(n)
		i.OperationID[n] = byte(n + 16)
	}
	i.Sequence = 31
	for n := range q {
		q[n] = byte(n + 32)
	}
	v := ipReceiptReclaimResult{Family: ipReceiptText, CapturedACK: 29, OldCursor: 17, RemovedRows: 3, RemovedPayloadBytes: 1024, AfterSequence: 23, AppliedMS: 41}
	got, err := canonicalResultDigest(i, q, v)
	if err != nil || fmt.Sprintf("%x", got) != "c767da6ebc4e07442c2ce6d2ae4d72fc395ee89b11397bb30af9278ded931b99" {
		t.Fatal(got, err)
	}
}
