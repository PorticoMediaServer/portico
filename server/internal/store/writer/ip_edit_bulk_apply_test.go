package writer

import (
	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"reflect"
	"testing"
)

func ipBulkReady(t *testing.T) (*executor, ipEditBulkRequest) {
	w, q := ipBulkFixture(t)
	_, turn, err := ipBulkDo(t, w, q)
	if err != nil {
		t.Fatal(err)
	}
	ipPolicyTestCommit(t, turn)
	q = ipBulkNext(t, w, q, ipEditBulkCapture)
	q.Targets = ipBulkTargets()
	_, turn, err = ipBulkDo(t, w, q)
	if err != nil {
		t.Fatal(err)
	}
	ipPolicyTestCommit(t, turn)
	q = ipBulkNext(t, w, q, ipEditBulkSeal)
	q.AfterOrdinal = 2
	q.Targets = nil
	_, turn, err = ipBulkDo(t, w, q)
	if err != nil {
		t.Fatal(err)
	}
	ipPolicyTestCommit(t, turn)
	q = ipBulkNext(t, w, q, ipEditBulkApply)
	q.AfterOrdinal = 0
	q.LeaseOwner = 1
	q.LeaseGeneration = 1
	q.LeaseUntilMS = 10000
	ipPolicyTestExec(t, w, `UPDATE metadata_repair_cascades SET job_lease_owner=1,job_lease_generation=1,job_lease_expires_ms=10000 WHERE id='bulk1'`)
	q.Targets = ipBulkTargets()
	ipPolicyTestExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at,value_present,automatic_present) VALUES('item',101,'country','','CA',0,'prior','prior',1,1)`)
	for _, target := range q.Targets {
		source := ipTextBundleSource{OwnerID: 11, EntityID: target.EntityID, LibraryID: 7, Kind: q.Root.Kind, Actor: q.Root.Actor, OwnerRevision: 17, SubjectRevision: target.Revision, MetadataRevision: 29, MetadataIncarnation: target.Incarnation}
		// Target102 is retained but has no current entity; native eligibility must
		// advance the raw cursor as stale rather than edit or loop on that row.
		edit := ipTextBundleRequest{Identity: CommandIdentity{Incarnation: [16]byte{2}, Sequence: 1, OperationID: ipEditBulkChildOperationID(q.Identity, target.Ordinal)}, Kind: IPFieldEdit, Source: source, Fields: []ipTextBundleField{{fieldmeta.Country, fieldmeta.TextState{Effective: fieldmeta.Text{Present: true}, Automatic: fieldmeta.Text{"CA", true}}, q.Root.Fields[0].Edit}}}
		q.Edits = append(q.Edits, edit)
	}
	return w, q
}
func ipBulkApplyDo(t *testing.T, w *executor, q ipEditBulkRequest) (ipEditBulkNative, *commandTurn, error) {
	t.Helper()
	c, err := newIPEditBulkCommand(q)
	if err != nil {
		t.Fatal(err)
	}
	defs := append(ipEditBulkDefinitions(), ipTextBundleDefinitions()...)
	turn := ipPolicyTestTurn(t, w, q.Action.command(), defs)
	out, err := applyIPEditBulkPage(t.Context(), turn, c, 1234)
	return out, turn, err
}
func TestIPEditBulkNativeApplyRawStaleReplayAndFinish(t *testing.T) {
	w, q := ipBulkReady(t)
	got, turn, err := ipBulkApplyDo(t, w, q)
	if err != nil || got.value.Processed != 1 || got.value.Failed != 1 || got.value.AfterOrdinal != 2 {
		t.Fatal(got, err)
	}
	ipPolicyTestCommit(t, turn)
	var applied, stale, pins, revision int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM metadata_edit_batch_targets WHERE state='applied'),(SELECT count(*) FROM metadata_edit_batch_targets WHERE state='stale'),(SELECT count(*) FROM metadata_edit_batch_targets WHERE edit_owner_id=11 AND edit_command_sequence=1),(SELECT revision FROM catalog_entities WHERE id=101)`).Scan(&applied, &stale, &pins, &revision); err != nil || applied != 1 || stale != 1 || pins != 1 || revision != 24 {
		t.Fatal(applied, stale, pins, revision, err)
	}
	// The target's FK keeps the exact inner result even after its ACK floor.
	ipPolicyTestExec(t, w, `UPDATE metadata_edit_owners SET acknowledged_command_sequence=1 WHERE id=11`)
	if _, err = w.db.ExecContext(t.Context(), `DELETE FROM metadata_edit_receipts WHERE owner_id=11`); err == nil {
		t.Fatal("bulk lost inner result pin")
	}
	replay, turn, err := ipBulkApplyDo(t, w, q)
	if err != nil || replay.decision != receiptReplay || !reflect.DeepEqual(got, replayWithDecision(replay, got.decision)) {
		t.Fatal(replay, err)
	}
	ipPolicyTestCommit(t, turn)
	q = ipBulkNext(t, w, q, ipEditBulkFinish)
	q.AfterOrdinal = 2
	q.Targets = nil
	q.Edits = nil
	finished, turn, err := ipBulkApplyDo(t, w, q)
	if err != nil || finished.value.Processed != 1 || finished.value.Failed != 1 {
		t.Fatal(finished, err)
	}
	ipPolicyTestCommit(t, turn)
	if got := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_repair_cascades WHERE status='complete' AND processed=1 AND failed=1`); got != 1 {
		t.Fatal(got)
	}
}
func replayWithDecision(v ipEditBulkNative, d receiptDecision) ipEditBulkNative {
	v.decision = d
	return v
}
func TestIPEditBulkNativeApplyWholePageRollback(t *testing.T) {
	w, q := ipBulkReady(t)
	ipPolicyTestExec(t, w, `CREATE TRIGGER ip_bulk_apply_fail BEFORE INSERT ON metadata_edit_batch_receipts WHEN NEW.command_kind='page' BEGIN SELECT RAISE(ABORT,'bulk receipt');END`)
	got, turn, err := ipBulkApplyDo(t, w, q)
	if err == nil || !reflect.DeepEqual(got, ipEditBulkNative{}) {
		t.Fatal(got, err)
	}
	_, _ = turn.transaction.rollback(err)
	if got := ipPolicyTestScalar(t, w, `SELECT revision FROM catalog_entities WHERE id=101`); got != 23 {
		t.Fatal(got)
	}
	if got := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_edit_receipts`); got != 0 {
		t.Fatal(got)
	}
	if got := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_edit_batch_targets WHERE state='captured'`); got != 2 {
		t.Fatal(got)
	}
	if got := ipPolicyTestScalar(t, w, `SELECT processed+failed FROM metadata_repair_cascades`); got != 0 {
		t.Fatal(got)
	}
}
