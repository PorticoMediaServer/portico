package writer

import (
	"encoding/hex"
	"fmt"
	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"testing"
)

func ipBulkFixture(t *testing.T) (*executor, ipEditBulkRequest) {
	w, _ := ipTextFixture(t)
	for _, query := range []string{
		`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(102,X'01000000000000000000000000000000',7,1)`,
		`INSERT INTO metadata_repair_cascades(id,kind,entity_id,library_id,actor,intent,base_revision,created_at,command_incarnation) VALUES('bulk1','item',101,'ip-library','local:administrator','descriptive-bulk-v1','23','now',X'02000000000000000000000000000000')`,
	} {
		ipPolicyTestExec(t, w, query)
	}
	r := ipEditBulkRoot{Operation: "bulk1", Kind: "item", Library: "ip-library", Actor: "local:administrator", BaseRevision: "23", RootEntity: 101, ExpectedCount: 2, ExpectedBytes: 121, Fields: []ipEditBulkField{{fieldmeta.Country, fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "US"}}}}
	b, _ := hex.DecodeString("2d5f7dbae8111d94c68ff52d425a4e60a55f1127d42c2608a10c2fbf8c3b35cb")
	copy(r.Manifest[:], b)
	return w, ipEditBulkRequest{Identity: CommandIdentity{Incarnation: [16]byte{2}, Sequence: 1, OperationID: [16]byte{10}}, Action: ipEditBulkBegin, Root: r, Revision: 1}
}
func ipBulkTargets() []ipEditBulkTarget {
	return []ipEditBulkTarget{{0, 101, [16]byte{1}, 23}, {1, 102, [16]byte{1}, 7}}
}
func ipBulkDo(t *testing.T, w *executor, q ipEditBulkRequest) (ipEditBulkNative, *commandTurn, error) {
	t.Helper()
	c, err := newIPEditBulkCommand(q)
	if err != nil {
		t.Fatal(err)
	}
	turn := ipPolicyTestTurn(t, w, q.Action.command(), ipEditBulkDefinitions())
	out, err := applyIPEditBulkCapture(t.Context(), turn, c, 1234)
	return out, turn, err
}
func ipBulkNext(t *testing.T, w *executor, q ipEditBulkRequest, action ipEditBulkAction) ipEditBulkRequest {
	t.Helper()
	ipPolicyTestExec(t, w, `UPDATE metadata_repair_cascades SET acknowledged_command_sequence=last_command_sequence WHERE id='bulk1'`)
	q.Identity.Sequence++
	q.Identity.OperationID[0]++
	q.Revision++
	q.Action = action
	return q
}
func TestIPEditBulkIndependentOriginalPrefixManifest(t *testing.T) {
	_, q := ipBulkFixture(t)
	seed := ipEditBulkSeed(q.Root)
	if fmt.Sprintf("%x", seed) != "ffd6a9179106818b501dc6b5e929ff4906f37de92614816cf92a38dcb1b652c9" {
		t.Fatal(seed)
	}
	for n, target := range ipBulkTargets() {
		seed = ipEditBulkChain(seed, target)
		expected := []string{"3a5e1c0d8cb3d4e61018f2c4b611f24549880c5d479d7fcb9d0e87223650a866", "2d5f7dbae8111d94c68ff52d425a4e60a55f1127d42c2608a10c2fbf8c3b35cb"}[n]
		if fmt.Sprintf("%x", seed) != expected {
			t.Fatal(n, seed)
		}
	}
}
func TestIPEditBulkNativeCaptureReplayAndSeal(t *testing.T) {
	w, q := ipBulkFixture(t)
	first, turn, err := ipBulkDo(t, w, q)
	if err != nil || first.value.Captured != 0 || first.value.Complete {
		t.Fatal(first, err)
	}
	ipPolicyTestCommit(t, turn)
	q = ipBulkNext(t, w, q, ipEditBulkCapture)
	q.Targets = ipBulkTargets()
	got, turn, err := ipBulkDo(t, w, q)
	if err != nil || got.value.Captured != 2 || got.value.Complete {
		t.Fatal(got, err)
	}
	ipPolicyTestCommit(t, turn)
	replay, turn, err := ipBulkDo(t, w, q)
	if err != nil || replay.decision != receiptReplay || replay.value != got.value || replay.digest != got.digest {
		t.Fatal(replay, err)
	}
	ipPolicyTestCommit(t, turn)
	q = ipBulkNext(t, w, q, ipEditBulkSeal)
	q.Targets = nil
	q.AfterOrdinal = 2
	sealed, turn, err := ipBulkDo(t, w, q)
	if err != nil || !sealed.value.Complete || sealed.value.Captured != 2 {
		t.Fatal(sealed, err)
	}
	ipPolicyTestCommit(t, turn)
	if got := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_repair_cascades WHERE descriptive_input_complete=1 AND status='ready' AND processed=0 AND failed=0`); got != 1 {
		t.Fatal(got)
	}
	if got := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_owner_history`); got != 0 {
		t.Fatal("capture edited targets", got)
	}
}
func TestIPEditBulkNativeRollbackAndIncompleteSeal(t *testing.T) {
	for _, fault := range []string{"terminal", "wrong_manifest", "extra_target", "missing_suffix"} {
		t.Run(fault, func(t *testing.T) {
			w, q := ipBulkFixture(t)
			if fault == "wrong_manifest" {
				q.Root.Manifest[0] ^= 1
			}
			_, turn, err := ipBulkDo(t, w, q)
			if err != nil {
				t.Fatal(err)
			}
			ipPolicyTestCommit(t, turn)
			q = ipBulkNext(t, w, q, ipEditBulkCapture)
			q.Targets = ipBulkTargets()
			if fault == "missing_suffix" {
				q.Targets = q.Targets[:1]
			}
			if fault == "terminal" {
				ipPolicyTestExec(t, w, `CREATE TRIGGER ip_bulk_fail BEFORE INSERT ON metadata_edit_batch_receipts WHEN NEW.command_kind='capture' BEGIN SELECT RAISE(ABORT,'receipt');END`)
			}
			got, turn, err := ipBulkDo(t, w, q)
			if fault == "terminal" {
				if err == nil || got != (ipEditBulkNative{}) {
					t.Fatal(got, err)
				}
				_, _ = turn.transaction.rollback(err)
				if n := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_edit_batch_targets`); n != 0 {
					t.Fatal(n)
				}
				if n := ipPolicyTestScalar(t, w, `SELECT descriptive_captured_count FROM metadata_repair_cascades`); n != 0 {
					t.Fatal(n)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ipPolicyTestCommit(t, turn)
			if fault == "extra_target" {
				ipPolicyTestExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(103,X'03000000000000000000000000000000',7,1)`)
				ipPolicyTestExec(t, w, `INSERT INTO metadata_edit_batch_targets(operation_id,ordinal,entity_id,expected_entity_incarnation,expected_revision,state,result_revision) VALUES('bulk1',2,103,zeroblob(16),1,'captured',NULL)`)
			}
			q = ipBulkNext(t, w, q, ipEditBulkSeal)
			q.AfterOrdinal = int64(len(q.Targets))
			q.Targets = nil
			got, turn, err = ipBulkDo(t, w, q)
			if err == nil || got != (ipEditBulkNative{}) {
				t.Fatal(got, err)
			}
			_, _ = turn.transaction.rollback(err)
			if n := ipPolicyTestScalar(t, w, `SELECT descriptive_input_complete FROM metadata_repair_cascades`); n != 0 {
				t.Fatal(n)
			}
		})
	}
}
