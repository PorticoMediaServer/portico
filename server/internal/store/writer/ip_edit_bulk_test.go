package writer

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// ipBulkPublic is a 16-byte public id distinguished by its first byte.
func ipBulkPublic(first byte) []byte {
	id := make([]byte, 16)
	id[0] = first
	return id
}

func ipBulkFixture(t *testing.T) (*executor, *ipEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, p := ipEntrySecurityFixture(t)
	// The E subset builds on the reviewed owner-edit owner/receipt pair: batch
	// targets pin the exact atomic edit receipt through that same FK pair.
	for _, sql := range []string{
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media'),(778,'other-library','/other')`,
		`INSERT OR IGNORE INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(8,'book',1,0,1,1,0)`,
	} {
		ipBulkExec(t, w, sql)
	}
	ipBulkExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(778,?,777,1),(779,?,777,1),(780,?,777,8),(781,?,778,1)`,
		ipBulkPublic(8), ipBulkPublic(9), ipBulkPublic(10), ipBulkPublic(11))
	for _, row := range []struct {
		id       int64
		publicID []byte
		library  int64
		kind     int64
		title    string
		revision int64
		inc      [16]byte
	}{
		{778, ipBulkPublic(8), 777, 1, "Item", 3, [16]byte{4}},
		{779, ipBulkPublic(9), 777, 1, "Second", 5, [16]byte{5}},
		{780, ipBulkPublic(10), 777, 8, "Book", 1, [16]byte{6}},
		{781, ipBulkPublic(11), 778, 1, "Foreign", 1, [16]byte{7}},
	} {
		ipBulkExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision,revision) VALUES(?,?,?,?,?,?,?,?,?,0,?,?,1,1,1,?)`,
			row.id, row.publicID, row.library, row.kind, row.title, strings.ToLower(row.title), strings.ToLower(row.title)[:1],
			strings.ToLower(row.title), strings.ToLower(row.title)[:1], row.inc[:], row.inc[:], row.revision)
	}
	r, err := stmttest.New(t, ipEditBulkDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipEntryRegistry{r, keeper}, p
}

func ipBulkExec(t *testing.T, w *executor, query string, args ...any) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(query, err)
	}
}

func ipBulkIdentity(sequence int64, operation byte) CommandIdentity {
	return CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
}

func ipBulkTargets() []ipEditBulkTarget {
	return []ipEditBulkTarget{{0, 778, [16]byte{4}, 3}, {1, 779, [16]byte{5}, 5}}
}

func ipBulkFieldBytes(fields []ipEditBulkField) int64 {
	var n int64
	for _, f := range fields {
		value := ""
		if f.Edit.Action == fieldmeta.SetText {
			value = f.Edit.Value
		}
		n += ipEditBulkFieldPad + int64(len(f.Field)) + int64(len(value))
	}
	return n
}

func ipBulkRawRequest(identity CommandIdentity) ipEditBulkRequest {
	fields := []ipEditBulkField{{Field: fieldmeta.Country, Edit: fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "US"}}}
	targets := ipBulkTargets()
	r := ipEditBulkRoot{
		Operation: ipEditBulkOperationID(identity.OperationID), Kind: "item", Library: "ip-library",
		LibraryID: 777, Actor: "Owner", BaseRevision: "3", RootEntity: 778,
		ExpectedCount: int64(len(targets)), ExpectedBytes: ipBulkFieldBytes(fields) + ipEditBulkTargetBytes*int64(len(targets)),
		Fields: fields,
	}
	r.Manifest = ipBulkManifestFor(r, targets)
	return ipEditBulkRequest{Kind: IPEditBulkBegin, Root: r, Targets: targets}
}

func ipBulkManifestFor(r ipEditBulkRoot, targets []ipEditBulkTarget) (out [32]byte) {
	out = ipEditBulkSeed(r)
	for _, t := range targets {
		out = ipEditBulkChain(out, t)
	}
	return
}

func ipBulkCommand(t *testing.T, identity CommandIdentity, mutate func(*ipEditBulkRequest)) *ipEditBulkCommand {
	t.Helper()
	q := ipBulkRawRequest(identity)
	if mutate != nil {
		mutate(&q)
		q.Root.ExpectedCount = int64(len(q.Targets))
		if q.Root.ExpectedBytes != 0 {
			q.Root.ExpectedBytes = ipBulkFieldBytes(q.Root.Fields) + ipEditBulkTargetBytes*int64(len(q.Targets))
		}
		q.Root.Manifest = ipBulkManifestFor(q.Root, q.Targets)
	}
	c, err := newIPEditBulkCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipBulkTurn(t *testing.T, w *executor, r *ipEntryRegistry) (*commandTurn, *transaction, ipEntryBudget) {
	t.Helper()
	turn, b := ipEntryTurn(t, w, r, ipEntryRequest{purpose: IPEditBulkBegin})
	return turn, turn.transaction, b
}

func ipBulkApply(t *testing.T, w *executor, r *ipEntryRegistry, p *usOwnerAuthorityProof, c *ipEditBulkCommand) (ipEditBulkCommandResult, *commandTurn, *transaction, error) {
	t.Helper()
	turn, tx, b := ipBulkTurn(t, w, r)
	out, err := applyIPEditBulkBegin(t.Context(), turn, p, t.Context(), b, c)
	return out, turn, tx, err
}

func ipEditBulkOutputZero(v ipEditBulkCommandResult) bool {
	return v.Outcome.State == 0 && !v.Outcome.Replayed && v.Outcome.Evidence == nil &&
		v.Outcome.Reference == (ReceiptReference{}) && v.Value == (ipEditBulkResult{}) && v.Digest == (ResultDigest{})
}

// The manifest/chained digest is byte-identical to the draft's published
// vectors, so the retained header is not a new invented collision domain.
func TestIPEditBulkManifestVectors(t *testing.T) {
	r := ipEditBulkRoot{
		Operation: "bulk1", Kind: "item", Library: "ip-library", Actor: "local:administrator",
		BaseRevision: "23", RootEntity: 101, ExpectedCount: 2, ExpectedBytes: 121,
		Fields: []ipEditBulkField{{Field: fieldmeta.Country, Edit: fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "US"}}},
	}
	seed := ipEditBulkSeed(r)
	if got := hex.EncodeToString(seed[:]); got != "ffd6a9179106818b501dc6b5e929ff4906f37de92614816cf92a38dcb1b652c9" {
		t.Fatal("seed", got)
	}
	targets := []ipEditBulkTarget{{0, 101, [16]byte{1}, 23}, {1, 102, [16]byte{1}, 7}}
	for n, want := range []string{
		"3a5e1c0d8cb3d4e61018f2c4b611f24549880c5d479d7fcb9d0e87223650a866",
		"2d5f7dbae8111d94c68ff52d425a4e60a55f1127d42c2608a10c2fbf8c3b35cb",
	} {
		seed = ipEditBulkChain(seed, targets[n])
		if got := hex.EncodeToString(seed[:]); got != want {
			t.Fatal(n, got)
		}
	}
}

func TestIPEditBulkBeginFreshReplayRetired(t *testing.T) {
	w, r, p := ipBulkFixture(t)
	identity := ipBulkIdentity(1, 2)
	cmd := ipBulkCommand(t, identity, nil)
	out, turn, tx, err := ipBulkApply(t, w, r, p, cmd)
	if err != nil || out.Outcome.State != Applied || out.Outcome.Replayed {
		t.Fatal(out, err)
	}
	if out.Value.OwnerID != ipEditBulkOperationID(identity.OperationID) || out.Value.Revision != 1 ||
		out.Value.Captured != 2 || out.Value.AfterOrdinal != 2 || !out.Value.Complete ||
		out.Value.Processed != 0 || out.Value.Failed != 0 ||
		out.Value.AppliedMS != w.now().UnixMilli() || out.Digest == (ResultDigest{}) {
		t.Fatal(out.Value)
	}
	ipTextCommit(t, turn, tx)
	original := out
	id := out.Value.OwnerID
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_repair_cascades WHERE id='` + id + `' AND kind='item' AND entity_id=778 AND library_id='ip-library' AND actor='Owner' AND intent='descriptive-bulk-v1' AND base_revision='3' AND status='ready'`: 1,
		`SELECT descriptive_expected_count FROM metadata_repair_cascades WHERE id='` + id + `'`:                                                                                                                                                 2,
		`SELECT descriptive_captured_count FROM metadata_repair_cascades WHERE id='` + id + `'`:                                                                                                                                                 2,
		`SELECT descriptive_input_complete FROM metadata_repair_cascades WHERE id='` + id + `'`:                                                                                                                                                 1,
		`SELECT descriptive_expected_bytes FROM metadata_repair_cascades WHERE id='` + id + `'`:                                                                                                                                                 121,
		`SELECT descriptive_captured_bytes FROM metadata_repair_cascades WHERE id='` + id + `'`:                                                                                                                                                 121,
		`SELECT descriptive_reclaim_after_ordinal FROM metadata_repair_cascades WHERE id='` + id + `'`:                                                                                                                                          -1,
		`SELECT descriptive_receipt_reclaim_after_sequence FROM metadata_repair_cascades WHERE id='` + id + `'`:                                                                                                                                 0,
		`SELECT count(*) FROM metadata_repair_cascades WHERE id='` + id + `' AND descriptive_manifest_digest=descriptive_captured_digest`:                                                                                                       1,
		`SELECT count(*) FROM metadata_edit_batch_fields WHERE operation_id='` + id + `' AND field='country' AND action='set' AND value='US' AND lock_override=0`:                                                                               1,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND state='captured' AND result_revision IS NULL`:                                                                                                     2,
		`SELECT count(*) FROM metadata_edit_batch_receipts WHERE owner_id='` + id + `' AND command_kind='begin' AND accepted_revision=1 AND after_ordinal=2 AND captured_count=2 AND processed_count=0 AND failed_count=0 AND input_complete=1`: 1,
		`SELECT last_command_sequence FROM metadata_repair_cascades WHERE id='` + id + `'`:                                                                                                                                                      1,
		`SELECT acknowledged_command_sequence FROM metadata_repair_cascades WHERE id='` + id + `'`:                                                                                                                                              0,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
	// Bulk capture never edits a target.
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_owner_fields`); got != 0 {
		t.Fatal("capture mutated an owner field", got)
	}
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_owner_history`); got != 0 {
		t.Fatal("capture wrote owner history", got)
	}

	// Catalogue movement after capture must not rebuild the original result.
	ipTextExec(t, w, `UPDATE catalog_entities SET revision=99,metadata_incarnation=? WHERE id IN(778,779)`, ipBulkPublic(12))
	turn, tx, b := ipBulkTurn(t, w, r)
	out, err = applyIPEditBulkBegin(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !out.Outcome.Replayed || out.Digest != original.Digest || !ipEditBulkResultEqual(out.Value, original.Value) {
		t.Fatal("catalogue change altered original replay", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_batch_receipts`); got != 1 {
		t.Fatal("replay wrote receipts", got)
	}
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_batch_targets WHERE expected_revision IN(3,5)`); got != 2 {
		t.Fatal("replay re-fetched the target revisions", got)
	}

	// At or below ACK is Retired even while the physical owner/children remain.
	ipTextExec(t, w, `UPDATE metadata_repair_cascades SET acknowledged_command_sequence=last_command_sequence WHERE id='`+id+`'`)
	turn, tx, b = ipBulkTurn(t, w, r)
	out, err = applyIPEditBulkBegin(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != ResultRetired || out.Outcome.Replayed || out.Outcome.Evidence != nil {
		t.Fatal("retired result served", out, err)
	}
	if out.Value != (ipEditBulkResult{}) || out.Digest != (ResultDigest{}) {
		t.Fatal("retired result leaked a value", out)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_batch_receipts`); got != 1 {
		t.Fatal("retired removed the physical receipt", got)
	}
}

func TestIPEditBulkBeginRefusesPartialMixedIntent(t *testing.T) {
	identity := ipBulkIdentity(1, 2)
	cases := map[string]func(*ipEditBulkRequest){
		"zero_manifest":  func(q *ipEditBulkRequest) { q.Root.Manifest = [32]byte{} },
		"wrong_manifest": func(q *ipEditBulkRequest) { q.Root.Manifest[0] ^= 1 },
		"count_mismatch": func(q *ipEditBulkRequest) { q.Root.ExpectedCount = 3 },
		"bytes_mismatch": func(q *ipEditBulkRequest) { q.Root.ExpectedBytes = 122 },
		"no_fields":      func(q *ipEditBulkRequest) { q.Root.Fields = nil },
		"unknown_field":  func(q *ipEditBulkRequest) { q.Root.Fields[0].Field = fieldmeta.Title },
		"unsorted_fields": func(q *ipEditBulkRequest) {
			q.Root.Fields = []ipEditBulkField{{fieldmeta.Studio, fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "S"}}, {fieldmeta.Country, fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "US"}}}
		},
		"non_set_value": func(q *ipEditBulkRequest) {
			q.Root.Fields[0].Edit = fieldmeta.TextEdit{Action: fieldmeta.LockText, Value: "x"}
		},
		"non_set_lock": func(q *ipEditBulkRequest) {
			q.Root.Fields[0].Edit = fieldmeta.TextEdit{Action: fieldmeta.UnlockText, Lock: fieldmeta.ForceUnlocked}
		},
		"empty_actor":           func(q *ipEditBulkRequest) { q.Root.Actor = "" },
		"person_root":           func(q *ipEditBulkRequest) { q.Root.Kind = "person" },
		"unknown_root_kind":     func(q *ipEditBulkRequest) { q.Root.Kind = "trailer" },
		"zero_entity":           func(q *ipEditBulkRequest) { q.Root.RootEntity = 0 },
		"base_not_canonical":    func(q *ipEditBulkRequest) { q.Root.BaseRevision = "03" },
		"base_zero":             func(q *ipEditBulkRequest) { q.Root.BaseRevision = "0" },
		"root_without_target":   func(q *ipEditBulkRequest) { q.Root.RootEntity = 780 },
		"root_revision_moved":   func(q *ipEditBulkRequest) { q.Root.BaseRevision = "4" },
		"duplicate_target":      func(q *ipEditBulkRequest) { q.Targets[1].EntityID = q.Targets[0].EntityID },
		"duplicate_ordinal":     func(q *ipEditBulkRequest) { q.Targets[1].Ordinal = 0 },
		"zero_target_inc":       func(q *ipEditBulkRequest) { q.Targets[0].Incarnation = [16]byte{} },
		"zero_target_rev":       func(q *ipEditBulkRequest) { q.Targets[0].Revision = 0 },
		"operation_not_derived": func(q *ipEditBulkRequest) { q.Root.Operation = "bulk1" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := ipBulkRawRequest(identity)
			mutate(&q)
			if c, err := newIPEditBulkCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}

	// A parent that does not carry the ONE descriptive intent is never adopted,
	// and its children cannot be written even by direct SQL.
	w, r, p := ipBulkFixture(t)
	id := ipEditBulkOperationID(identity.OperationID)
	ipBulkExec(t, w, `INSERT INTO metadata_repair_cascades(id,kind,entity_id,library_id,actor,intent,base_revision,created_at,command_incarnation) VALUES(?,'item',778,'ip-library','Owner','other','3','now',X'01000000000000000000000000000000')`, id)
	for _, sql := range []string{
		`INSERT INTO metadata_edit_batch_fields(operation_id,field,action,value,lock_override) VALUES(?, 'country','set','US',0)`,
		`INSERT INTO metadata_edit_batch_targets(operation_id,ordinal,entity_id,expected_entity_incarnation,expected_revision,state) VALUES(?,0,778,zeroblob(16),3,'captured')`,
		`INSERT INTO metadata_edit_batch_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,after_ordinal,captured_count,processed_count,failed_count,input_complete,manifest_algorithm,manifest_digest) VALUES(?,X'01000000000000000000000000000000',1,X'02000000000000000000000000000000',zeroblob(32),0,zeroblob(32),'begin',1,0,0,0,0,1,'v1',zeroblob(32))`,
	} {
		if _, err := w.db.ExecContext(t.Context(), sql, id); err == nil {
			t.Fatal("descriptive child crossed a mixed-intent parent", sql)
		}
	}
	cmd := ipBulkCommand(t, identity, nil)
	out, turn, tx, err := ipBulkApply(t, w, r, p, cmd)
	if err == nil || out.Value != (ipEditBulkResult{}) {
		t.Fatal("native adopted another parent intent", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_batch_fields`); got != 0 {
		t.Fatal(got)
	}

	// A partially populated descriptive header (or one with an empty manifest
	// domain) is refused by the E header checks themselves.
	w2, _, _ := ipBulkFixture(t)
	for _, sql := range []string{
		`INSERT INTO metadata_repair_cascades(id,kind,entity_id,library_id,actor,intent,base_revision,created_at,descriptive_expected_count) VALUES('partial','item',778,'ip-library','Owner','descriptive-bulk-v1','3','now',1)`,
		`INSERT INTO metadata_repair_cascades(id,kind,entity_id,library_id,actor,intent,base_revision,created_at,descriptive_expected_count,descriptive_captured_count) VALUES('partial','item',778,'ip-library','Owner','descriptive-bulk-v1','3','now',1,0)`,
		`INSERT INTO metadata_repair_cascades(id,kind,entity_id,library_id,actor,intent,base_revision,created_at,descriptive_expected_count,descriptive_captured_count,descriptive_input_complete,descriptive_manifest_algorithm,descriptive_manifest_digest,descriptive_expected_bytes,descriptive_captured_bytes,descriptive_captured_digest,descriptive_reclaim_after_ordinal,descriptive_receipt_reclaim_after_sequence) VALUES('partial','item',778,'ip-library','Owner','descriptive-bulk-v1','3','now',1,1,1,'',zeroblob(32),41,41,zeroblob(32),-1,0)`,
		`INSERT INTO metadata_repair_cascades(id,kind,entity_id,library_id,actor,intent,base_revision,created_at,descriptive_expected_count,descriptive_captured_count,descriptive_input_complete,descriptive_manifest_algorithm,descriptive_manifest_digest,descriptive_expected_bytes,descriptive_captured_bytes,descriptive_captured_digest,descriptive_reclaim_after_ordinal,descriptive_receipt_reclaim_after_sequence) VALUES('partial','item',778,'ip-library','Owner','descriptive-bulk-v1','3','now',2,1,1,'portico-ip-descriptive-input-sha256-v1',zeroblob(32),121,41,zeroblob(32),-1,0)`,
	} {
		if _, err := w2.db.ExecContext(t.Context(), sql); err == nil {
			t.Fatal("accepted a partial/mixed descriptive header", sql)
		}
	}
}

func TestIPEditBulkBeginRetainsOriginalTargetSet(t *testing.T) {
	w, r, p := ipBulkFixture(t)
	cmd := ipBulkCommand(t, ipBulkIdentity(1, 2), nil)
	out, turn, tx, err := ipBulkApply(t, w, r, p, cmd)
	if err != nil {
		t.Fatal(err)
	}
	ipTextCommit(t, turn, tx)
	id := out.Value.OwnerID
	rows, err := w.db.QueryContext(t.Context(), `SELECT ordinal,entity_id,expected_revision FROM metadata_edit_batch_targets WHERE operation_id=? ORDER BY ordinal`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][3]int64
	for rows.Next() {
		var ordinal, entity, revision int64
		if err = rows.Scan(&ordinal, &entity, &revision); err != nil {
			t.Fatal(err)
		}
		got = append(got, [3]int64{ordinal, entity, revision})
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := [][3]int64{{0, 778, 3}, {1, 779, 5}}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for j := range want {
		if got[j] != want[j] {
			t.Fatal(j, got[j], want[j])
		}
	}

	// The original target set and its revisions survive later catalogue drift;
	// the saved result never becomes a re-fetched current cohort.
	ipTextExec(t, w, `UPDATE catalog_entities SET revision=revision+10 WHERE id IN(778,779)`)
	turn, tx, b := ipBulkTurn(t, w, r)
	replay, e := applyIPEditBulkBegin(t.Context(), turn, p, t.Context(), b, cmd)
	if e != nil || !replay.Outcome.Replayed || !ipEditBulkResultEqual(replay.Value, out.Value) {
		t.Fatal(replay, e)
	}
	ipTextRollback(t, turn, tx)
	if n := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='`+id+`' AND expected_revision IN(3,5)`); n != 2 {
		t.Fatal("capture re-read the remainder", n)
	}
}

func TestIPEditBulkBeginDuplicateAndForeignTargets(t *testing.T) {
	w, r, p := ipBulkFixture(t)
	identity := ipBulkIdentity(1, 2)
	for name, mutate := range map[string]func(*ipEditBulkRequest){
		"foreign_library": func(q *ipEditBulkRequest) {
			q.Targets[1] = ipEditBulkTarget{Ordinal: 1, EntityID: 781, Incarnation: [16]byte{7}, Revision: 1}
		},
		"missing_entity": func(q *ipEditBulkRequest) {
			q.Targets[1] = ipEditBulkTarget{Ordinal: 1, EntityID: 999, Incarnation: [16]byte{7}, Revision: 1}
		},
		"stale_revision": func(q *ipEditBulkRequest) { q.Targets[1].Revision = 4 },
		"stale_incarn":   func(q *ipEditBulkRequest) { q.Targets[1].Incarnation = [16]byte{15} },
	} {
		t.Run(name, func(t *testing.T) {
			q := ipBulkRawRequest(identity)
			mutate(&q)
			q.Root.ExpectedBytes = ipBulkFieldBytes(q.Root.Fields) + ipEditBulkTargetBytes*int64(len(q.Targets))
			q.Root.Manifest = ipBulkManifestFor(q.Root, q.Targets)
			c, err := newIPEditBulkCommand(identity, q)
			if err != nil {
				t.Fatal(err)
			}
			out, turn, tx, err := ipBulkApply(t, w, r, p, c)
			if !ipEditBulkOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("foreign/stale target admitted", out, err)
			}
			ipTextRollback(t, turn, tx)
			for query, want := range map[string]int64{
				`SELECT count(*) FROM metadata_repair_cascades`:     0,
				`SELECT count(*) FROM metadata_edit_batch_fields`:   0,
				`SELECT count(*) FROM metadata_edit_batch_targets`:  0,
				`SELECT count(*) FROM metadata_edit_batch_receipts`: 0,
			} {
				if got := ipTextQueryInt(t, w, query); got != want {
					t.Fatal(query, got)
				}
			}
		})
	}

	// A retired target is not a visible cohort member.
	w2, r2, p2 := ipBulkFixture(t)
	ipTextExec(t, w2, `UPDATE catalog_entities SET retired=1 WHERE id=779`)
	cmd := ipBulkCommand(t, identity, nil)
	out, turn, tx, err := ipBulkApply(t, w2, r2, p2, cmd)
	if !ipEditBulkOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("retired target admitted", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w2, `SELECT count(*) FROM metadata_repair_cascades`); got != 0 {
		t.Fatal(got)
	}
}

func TestIPEditBulkBeginWrongKindCohort(t *testing.T) {
	w, r, p := ipBulkFixture(t)
	identity := ipBulkIdentity(1, 2)
	q := ipBulkRawRequest(identity)
	q.Root.Kind = "book"
	q.Root.RootEntity = 780
	q.Targets = []ipEditBulkTarget{{0, 780, [16]byte{6}, 1}}
	q.Root.BaseRevision = "1"
	q.Root.ExpectedCount = 1
	q.Root.ExpectedBytes = ipBulkFieldBytes(q.Root.Fields) + ipEditBulkTargetBytes
	q.Root.Manifest = ipBulkManifestFor(q.Root, q.Targets)
	c, err := newIPEditBulkCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	if out, turn, tx, err := ipBulkApply(t, w, r, p, c); err != nil || out.Outcome.State != Applied {
		t.Fatal("legitimate book cohort refused", out, err)
	} else {
		ipTextCommit(t, turn, tx)
	}

	// The same declared kind with an item target is a foreign cohort member.
	w2, r2, p2 := ipBulkFixture(t)
	q = ipBulkRawRequest(identity)
	q.Root.Kind = "book"
	q.Root.RootEntity = 780
	q.Targets = []ipEditBulkTarget{{0, 780, [16]byte{6}, 1}, {1, 778, [16]byte{4}, 3}}
	q.Root.BaseRevision = "1"
	q.Root.ExpectedCount = 2
	q.Root.ExpectedBytes = ipBulkFieldBytes(q.Root.Fields) + 2*ipEditBulkTargetBytes
	q.Root.Manifest = ipBulkManifestFor(q.Root, q.Targets)
	c, err = newIPEditBulkCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	if out, turn, tx, err := ipBulkApply(t, w2, r2, p2, c); !ipEditBulkOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("mixed-kind cohort admitted", out, err)
	} else {
		ipTextRollback(t, turn, tx)
	}
}

func TestIPEditBulkBeginCurrentAuthority(t *testing.T) {
	for _, mode := range []string{"forged_proof", "revoked_family", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := ipBulkFixture(t)
			cmd := ipBulkCommand(t, ipBulkIdentity(1, 2), nil)
			if out, turn, tx, err := ipBulkApply(t, w, r, p, cmd); err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			} else {
				ipTextCommit(t, turn, tx)
			}
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b := ipBulkTurn(t, w, r)
			out, err := applyIPEditBulkBegin(t.Context(), turn, proof, t.Context(), b, cmd)
			if !ipEditBulkOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale authority served the saved capture", out, err)
			}
			ipTextRollback(t, turn, tx)
		})
	}
	// A cancelled caller lifetime redacts an otherwise fresh command.
	w, r, p := ipBulkFixture(t)
	cmd := ipBulkCommand(t, ipBulkIdentity(1, 2), nil)
	turn, tx, b := ipBulkTurn(t, w, r)
	lifetime, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := applyIPEditBulkBegin(t.Context(), turn, p, lifetime, b, cmd)
	if !ipEditBulkOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("cancelled lifetime released output", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_repair_cascades`); got != 0 {
		t.Fatal("cancelled lifetime wrote the owner", got)
	}
}

func TestIPEditBulkBeginRollbackAndConflict(t *testing.T) {
	w, r, p := ipBulkFixture(t)
	cmd := ipBulkCommand(t, ipBulkIdentity(1, 2), nil)
	if _, err := w.db.ExecContext(t.Context(), `CREATE TRIGGER ip_bulk_fail BEFORE INSERT ON metadata_edit_batch_receipts BEGIN SELECT RAISE(ABORT,'receipt');END`); err != nil {
		t.Fatal(err)
	}
	out, turn, tx, err := ipBulkApply(t, w, r, p, cmd)
	if err == nil || !ipEditBulkOutputZero(out) {
		t.Fatal("failed capture returned public output", out, err)
	}
	ipTextRollback(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_repair_cascades`:     0,
		`SELECT count(*) FROM metadata_edit_batch_fields`:   0,
		`SELECT count(*) FROM metadata_edit_batch_targets`:  0,
		`SELECT count(*) FROM metadata_edit_batch_receipts`: 0,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}

	// A retained sequence with a different original request is a conflict.
	w2, r2, p2 := ipBulkFixture(t)
	first := ipBulkCommand(t, ipBulkIdentity(1, 2), nil)
	if out, turn, tx, err := ipBulkApply(t, w2, r2, p2, first); err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	} else {
		ipTextCommit(t, turn, tx)
	}
	conflicting := ipBulkCommand(t, ipBulkIdentity(1, 2), func(q *ipEditBulkRequest) { q.Root.Fields[0].Edit.Value = "CA" })
	if out, turn, tx, err := ipBulkApply(t, w2, r2, p2, conflicting); !ipEditBulkOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting request admitted", out, err)
	} else {
		ipTextRollback(t, turn, tx)
	}
	// A gap cannot be admitted while the predecessor is unacknowledged.
	w3, r3, p3 := ipBulkFixture(t)
	first = ipBulkCommand(t, ipBulkIdentity(1, 2), nil)
	if out, turn, tx, err := ipBulkApply(t, w3, r3, p3, first); err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	} else {
		ipTextCommit(t, turn, tx)
	}
	next := ipBulkCommand(t, ipBulkIdentity(2, 2), nil)
	if out, turn, tx, err := ipBulkApply(t, w3, r3, p3, next); !ipEditBulkOutputZero(out) || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("unacknowledged predecessor admitted the next sequence", out, err)
	} else {
		ipTextRollback(t, turn, tx)
	}
}

// A tampered owner header, receipt or captured cohort can never be relabelled
// as the original saved result.
func TestIPEditBulkSavedShapeRefused(t *testing.T) {
	for name, mutate := range map[string]string{
		"parent_count_drift": `UPDATE metadata_repair_cascades SET descriptive_expected_count=3,descriptive_captured_count=3 WHERE id=?`,
		"parent_manifest":    `UPDATE metadata_repair_cascades SET descriptive_manifest_digest=zeroblob(32),descriptive_captured_digest=zeroblob(32) WHERE id=?`,
		"parent_incomplete":  `UPDATE metadata_repair_cascades SET descriptive_input_complete=0 WHERE id=?`,
		"receipt_result":     `UPDATE metadata_edit_batch_receipts SET result_digest=zeroblob(32) WHERE owner_id=?`,
		"receipt_variant":    `UPDATE metadata_edit_batch_receipts SET command_kind='capture' WHERE owner_id=?`,
		"extra_target":       `INSERT INTO metadata_edit_batch_targets(operation_id,ordinal,entity_id,expected_entity_incarnation,expected_revision,state) VALUES(?,2,780,zeroblob(16),1,'captured')`,
		"rewritten_field":    `UPDATE metadata_edit_batch_fields SET value='CA' WHERE operation_id=?`,
		"dropped_field":      `DELETE FROM metadata_edit_batch_fields WHERE operation_id=?`,
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := ipBulkFixture(t)
			cmd := ipBulkCommand(t, ipBulkIdentity(1, 2), nil)
			out, turn, tx, err := ipBulkApply(t, w, r, p, cmd)
			if err != nil {
				t.Fatal(err)
			}
			ipTextCommit(t, turn, tx)
			ipTextExec(t, w, mutate, out.Value.OwnerID)
			turn, tx, b := ipBulkTurn(t, w, r)
			got, e := applyIPEditBulkBegin(t.Context(), turn, p, t.Context(), b, cmd)
			if !ipEditBulkOutputZero(got) || e == nil {
				t.Fatal("tampered saved shape served as Applied", got, e)
			}
			ipTextRollback(t, turn, tx)
		})
	}
}

func TestIPEditBulkBeginUnknownCommit(t *testing.T) {
	w, r, p := ipBulkFixture(t)
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	cmd := ipBulkCommand(t, ipBulkIdentity(1, 2), nil)
	turn, tx, b := ipBulkTurn(t, w, r)
	out, err := applyIPEditBulkBegin(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	id := out.Value.OwnerID
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if out, e := applyIPEditBulkBegin(t.Context(), turn, p, t.Context(), b, cmd); !ipEditBulkOutputZero(out) || e == nil {
		t.Fatal("uncertain frame reserved the saved capture", out, e)
	}
	// Durable outcome is read through a FRESH read-only connection, never the
	// quarantined writer pool.
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_repair_cascades WHERE id='` + id + `'`:                                                             1,
		`SELECT descriptive_input_complete FROM metadata_repair_cascades WHERE id='` + id + `'`:                                           1,
		`SELECT count(*) FROM metadata_edit_batch_fields WHERE operation_id='` + id + `'`:                                                 1,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `'`:                                                2,
		`SELECT count(*) FROM metadata_edit_batch_receipts WHERE owner_id='` + id + `' AND command_kind='begin'`:                          1,
		`SELECT count(*) FROM metadata_repair_cascades WHERE id='` + id + `' AND descriptive_captured_digest=descriptive_manifest_digest`: 1,
	} {
		if got := ipPolicyFreshInt(t, path, query); got != want {
			t.Fatal(query, got, want)
		}
	}
}

func TestIPEditBulkBeginBounds(t *testing.T) {
	identity := ipBulkIdentity(1, 2)
	base := ipBulkRawRequest(identity)
	// All four closed bulk fields are admitted together.
	fields := []ipEditBulkField{
		{fieldmeta.Country, fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "US"}},
		{fieldmeta.MetadataLanguage, fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "en"}},
		{fieldmeta.Network, fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "N"}},
		{fieldmeta.Studio, fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "S"}},
	}
	q := base
	q.Root.Fields = fields
	q.Root.ExpectedBytes = ipBulkFieldBytes(fields) + ipEditBulkTargetBytes*int64(len(q.Targets))
	q.Root.Manifest = ipBulkManifestFor(q.Root, q.Targets)
	if _, err := newIPEditBulkCommand(identity, q); err != nil {
		t.Fatal("complete closed field set refused", err)
	}
	// The field value keeps its own per-field 300-rune bound.
	q = base
	q.Root.Fields = []ipEditBulkField{{fieldmeta.Country, fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: strings.Repeat("é", 300)}}}
	q.Root.ExpectedBytes = ipBulkFieldBytes(q.Root.Fields) + ipEditBulkTargetBytes*int64(len(q.Targets))
	q.Root.Manifest = ipBulkManifestFor(q.Root, q.Targets)
	if _, err := newIPEditBulkCommand(identity, q); err != nil {
		t.Fatal("bounded maximum field refused", err)
	}
	q.Root.Fields[0].Edit.Value = strings.Repeat("é", 301)
	q.Root.ExpectedBytes = ipBulkFieldBytes(q.Root.Fields) + ipEditBulkTargetBytes*int64(len(q.Targets))
	q.Root.Manifest = ipBulkManifestFor(q.Root, q.Targets)
	if c, err := newIPEditBulkCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize field admitted", c, err)
	}
	// Actor is bounded; the target cohort has an exact native cap.
	q = base
	q.Root.Actor = strings.Repeat("a", ipEditBulkActorMax)
	q.Root.Manifest = ipBulkManifestFor(q.Root, q.Targets)
	if _, err := newIPEditBulkCommand(identity, q); err != nil {
		t.Fatal("bounded actor refused", err)
	}
	q.Root.Actor = strings.Repeat("a", ipEditBulkActorMax+1)
	q.Root.Manifest = ipBulkManifestFor(q.Root, q.Targets)
	if c, err := newIPEditBulkCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize actor admitted", c, err)
	}
	q = base
	q.Root.ExpectedCount = ipEditBulkMaxTargets + 1
	if c, err := newIPEditBulkCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize cohort admitted", c, err)
	}
	// Empty input is never a bulk capture.
	q = base
	q.Root.ExpectedCount, q.Targets, q.Root.ExpectedBytes = 0, nil, 0
	q.Root.Manifest = ipBulkManifestFor(q.Root, q.Targets)
	if c, err := newIPEditBulkCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("empty cohort admitted", c, err)
	}
	// Exactly the native cap is admitted.
	q = base
	q.Targets = make([]ipEditBulkTarget, ipEditBulkMaxTargets)
	for j := range q.Targets {
		q.Targets[j] = ipEditBulkTarget{Ordinal: int64(j), EntityID: int64(1000 + j), Incarnation: [16]byte{13}, Revision: int64(j + 1)}
	}
	// The root must remain in its own cohort.
	q.Targets[0] = ipEditBulkTarget{Ordinal: 0, EntityID: q.Root.RootEntity, Incarnation: [16]byte{4}, Revision: 3}
	q.Targets[0].EntityID = q.Root.RootEntity
	q.Root.ExpectedCount = ipEditBulkMaxTargets
	q.Root.ExpectedBytes = ipBulkFieldBytes(q.Root.Fields) + ipEditBulkTargetBytes*ipEditBulkMaxTargets
	q.Root.Manifest = ipBulkManifestFor(q.Root, q.Targets)
	if _, err := newIPEditBulkCommand(identity, q); err != nil {
		t.Fatal("maximum cohort refused", err)
	}
}

func TestIPEditBulkBeginDescriptors(t *testing.T) {
	expected := Descriptor{ID: IPEditBulkBegin, Name: "ip_edit_bulk_begin", Durability: Normal}
	actual, err := Describe(expected.ID)
	if err != nil || actual != expected || actual.ID != 243 {
		t.Fatal(actual, err)
	}
}

// The manifest header is completeness data only: its exact typed shape refuses
// a payload rewrite rather than relabelling a different result.
func TestIPEditBulkResultPreflight(t *testing.T) {
	value := ipEditBulkResult{OwnerID: ipEditBulkOperationID([16]byte{0xde, 0xad}), Revision: 1, AfterOrdinal: 2, Captured: 2, Complete: true, Manifest: [32]byte{1}, AppliedMS: 7}
	identity := ipBulkIdentity(1, 2)
	digest, err := ipEditBulkResultDigest(identity, RequestDigest{3}, value)
	if err != nil || digest == (ResultDigest{}) {
		t.Fatal(digest, err)
	}
	for name, mutate := range map[string]func(*ipEditBulkResult){
		"zero_owner":      func(v *ipEditBulkResult) { v.OwnerID = "" },
		"uppercase_owner": func(v *ipEditBulkResult) { v.OwnerID = strings.ToUpper(v.OwnerID) },
		"not_complete":    func(v *ipEditBulkResult) { v.Complete = false },
		"after_mismatch":  func(v *ipEditBulkResult) { v.AfterOrdinal = 1 },
		"failed_over":     func(v *ipEditBulkResult) { v.Failed = 3 },
		"zero_manifest":   func(v *ipEditBulkResult) { v.Manifest = [32]byte{} },
		"zero_revision":   func(v *ipEditBulkResult) { v.Revision = 0 },
		"negative_time":   func(v *ipEditBulkResult) { v.AppliedMS = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := value
			mutate(&bad)
			var zero [32]byte
			if got, err := ipEditBulkResultDigest(identity, RequestDigest{3}, bad); err == nil || got != ResultDigest(zero) {
				t.Fatal(got, err)
			}
		})
	}
	if got, err := ipEditBulkResultDigest(identity, RequestDigest{4}, value); err != nil || got == digest {
		t.Fatal("request digest did not bind the result", got)
	}
}
