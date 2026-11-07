package writer

import (
	"errors"
	"fmt"
	"testing"
	"time"

	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func ipTextFixture(t *testing.T) (*executor, ipTextLockCommand) {
	t.Helper()
	w := ipDraftExecutor(t)
	for _, s := range []string{
		`INSERT INTO libraries(id,name,kind,root) VALUES('ip-library','IP','movie','/media')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'ip-library','/namespace')`,
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(101,zeroblob(16),7,1)`,
		`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,revision,metadata_revision) VALUES(101,zeroblob(16),7,1,'Title','title','t','title','t',0,X'01000000000000000000000000000000',zeroblob(16),23,29)`,
		`INSERT INTO metadata_edit_owners(id,entity_id,kind,revision,command_incarnation) VALUES(11,101,'item',17,X'02000000000000000000000000000000')`,
		`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at,value_present,automatic_present) VALUES('item',101,'description','','Automatic é',0,'prior','prior',1,1)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	q := ipTextLockRequest{OwnerID: 11, EntityID: 101, LibraryID: 7, Kind: "item", Actor: "local:administrator", Field: fieldmeta.Description,
		ExpectedOwnerRevision: 17, ExpectedSubjectRevision: 23, ExpectedMetadataRevision: 29, MetadataIncarnation: [16]byte{1},
		Before: fieldmeta.TextState{Effective: fieldmeta.Text{Present: true}, Automatic: fieldmeta.Text{Value: "Automatic é", Present: true}}, Locked: true}
	c, err := newIPTextLockCommand(CommandIdentity{Incarnation: [16]byte{2}, Sequence: 1, OperationID: [16]byte{3}}, q)
	if err != nil {
		t.Fatal(err)
	}
	return w, c
}

func ipTextTurn(t *testing.T, w *executor, kinds ...CommandID) *commandTurn {
	t.Helper()
	defs := ipTextLockDefinitions()
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{limit: Work{4096, 10000000, 4096, 64 << 20, 25 * time.Millisecond}, setup: Work{16, 1000, 16, 65536, time.Millisecond}, terminal: Work{16, 1000, 16, 65536, time.Millisecond}}
	kind := IPFieldLock
	if len(kinds) > 0 {
		kind = kinds[0]
	}
	tx, err := w.begin(t.Context(), kind, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{64, 100000, 128, 1 << 20, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func TestIPTextLockNativeHistoryReplayAndRetired(t *testing.T) {
	w, c := ipTextFixture(t)
	turn := ipTextTurn(t, w)
	got, err := applyIPTextLock(t.Context(), turn, c, 1234)
	if err != nil || got.decision != receiptFresh || !got.value.Locked || !got.value.Changed || got.value.Value != "" || !got.value.ValuePresent || got.value.AcceptedRevision != 18 || got.value.SubjectRevision != 24 || !got.value.HistoryID.Present {
		t.Fatalf("fresh: %+v %v", got, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var locked, revision, metadata, history, receipts int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT locked,(SELECT revision FROM catalog_entities WHERE id=101),(SELECT metadata_revision FROM catalog_entities WHERE id=101),(SELECT count(*) FROM metadata_owner_history_fields),(SELECT count(*) FROM metadata_edit_receipts) FROM metadata_owner_fields WHERE kind='item' AND entity_id=101 AND field='description'`).Scan(&locked, &revision, &metadata, &history, &receipts); err != nil || locked != 1 || revision != 24 || metadata != 30 || history != 1 || receipts != 1 {
		t.Fatal("atomic state", locked, revision, metadata, history, receipts, err)
	}
	// Live source/cursor movement cannot hide original unacknowledged history.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_entities SET revision=99,metadata_revision=100 WHERE id=101`); err != nil {
		t.Fatal(err)
	}
	replayTurn := ipTextTurn(t, w)
	replay, err := applyIPTextLock(t.Context(), replayTurn, c, 9999)
	if err != nil || replay.decision != receiptReplay || replay.value != got.value || replay.digest != got.digest {
		t.Fatalf("historical: %+v %v", replay, err)
	}
	if _, err := replayTurn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE metadata_edit_owners SET acknowledged_command_sequence=1 WHERE id=11`); err != nil {
		t.Fatal(err)
	}
	retiredTurn := ipTextTurn(t, w)
	retired, err := applyIPTextLock(t.Context(), retiredTurn, c, 9999)
	if err != nil || retired.decision != receiptRetired || retired.value != (ipTextLockResult{}) {
		t.Fatalf("ACK floor: %+v %v", retired, err)
	}
}

func TestIPTextLockWholeRollbackAndStale(t *testing.T) {
	for _, mode := range []string{"field-stale", "source-stale", "receipt-failure"} {
		t.Run(mode, func(t *testing.T) {
			w, c := ipTextFixture(t)
			switch mode {
			case "field-stale":
				_, _ = w.db.ExecContext(t.Context(), `UPDATE metadata_owner_fields SET automatic_value='later' WHERE entity_id=101`)
			case "source-stale":
				_, _ = w.db.ExecContext(t.Context(), `UPDATE catalog_entities SET metadata_revision=100 WHERE id=101`)
			case "receipt-failure":
				if _, err := w.db.ExecContext(t.Context(), `CREATE TRIGGER ip_test_fail_receipt BEFORE INSERT ON metadata_edit_receipts BEGIN SELECT RAISE(ABORT,'injected terminal failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			turn := ipTextTurn(t, w)
			got, err := applyIPTextLock(t.Context(), turn, c, 1234)
			if err == nil || got != (ipTextLockNative{}) {
				t.Fatalf("partial result: %+v %v", got, err)
			}
			_, _ = turn.transaction.rollback(err)
			var locked, last, history int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT locked,(SELECT last_command_sequence FROM metadata_edit_owners WHERE id=11),(SELECT count(*) FROM metadata_owner_history) FROM metadata_owner_fields WHERE kind='item' AND entity_id=101 AND field='description'`).Scan(&locked, &last, &history); err != nil || locked != 0 || last != 0 || history != 0 {
				t.Fatal("rollback", locked, last, history, err)
			}
		})
	}
}

func TestIPTextLockNoopAndConflictingOriginalRequest(t *testing.T) {
	w, c := ipTextFixture(t)
	q := c.request
	q.Locked = false
	noop, err := newIPTextLockCommand(c.identity, q)
	if err != nil {
		t.Fatal(err)
	}
	turn := ipTextTurn(t, w)
	got, err := applyIPTextLock(t.Context(), turn, noop, 1234)
	if err != nil || got.value.Changed || got.value.HistoryID.Present || got.value.AcceptedRevision != 17 || got.value.SubjectRevision != 23 {
		t.Fatalf("noop %+v %v", got, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	conflict := ipTextTurn(t, w)
	got, err = applyIPTextLock(t.Context(), conflict, c, 1234)
	if !errors.Is(err, ErrConflict) || got != (ipTextLockNative{}) {
		t.Fatalf("changed original: %+v %v", got, err)
	}
}

func TestIPTextLockResultIndependentWire(t *testing.T) {
	var i CommandIdentity
	var request RequestDigest
	for n := range i.Incarnation {
		i.Incarnation[n] = byte(n)
		i.OperationID[n] = byte(n + 16)
	}
	i.Sequence = 31
	for n := range request {
		request[n] = byte(n + 32)
	}
	v := ipTextLockResult{11, 13, 17, 19, 23, "item", "description", "", "Adaé", true, true, true, true, EvidenceInteger{29, true}}
	got, err := ipTextLockResultDigest(i, request, v)
	// Independently encoded from the written v1 field order with Python
	// struct.pack/hashlib; 232 original wire bytes, no production encoder used.
	if err != nil || fmt.Sprintf("%x", got) != "5e7bae0a7a15772392967d55991fb8a4e09896e7ea7e021da9fd2c044502c5b9" {
		t.Fatal(got, err)
	}
	v.ValuePresent = false
	v.Value = "invalid hidden value"
	if _, err := ipTextLockResultDigest(i, request, v); err == nil {
		t.Fatal("accepted missing-tag payload")
	}
}
