package writer

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// ipBulkApplyDefinitions is one test registry for the birth, the reviewed typed
// owner edit and both new commands. Duplicate shared prefixes are collapsed by
// statement ID so the fixture is one W plan.
func ipBulkApplyDefinitions() []stmt.Definition {
	seen := make(map[stmt.ID]bool)
	var out []stmt.Definition
	for _, group := range [][]stmt.Definition{
		ipEditBulkDefinitions(), ipTextEditDefinitions(),
		ipEditBulkPageDefinitions(), ipEditBulkFinishDefinitions(),
	} {
		for _, d := range group {
			if seen[d.ID] {
				continue
			}
			seen[d.ID] = true
			out = append(out, d)
		}
	}
	return out
}

func ipBulkApplyFixture(t *testing.T) (*executor, *ipEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, p := ipEntrySecurityFixture(t)
	for _, sql := range []string{
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media'),(778,'other-library','/other')`,
		`INSERT OR IGNORE INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(8,'book',1,0,1,1,0)`,
	} {
		ipBulkExec(t, w, sql)
	}
	publicIDs := [4][16]byte{{8}, {9}, {10}, {11}}
	ipBulkExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(778,?,777,1),(779,?,777,1),(780,?,777,8),(781,?,778,1)`,
		publicIDs[0][:], publicIDs[1][:], publicIDs[2][:], publicIDs[3][:])
	for _, row := range []struct {
		id       int64
		publicID []byte
		library  int64
		kind     int64
		title    string
		revision int64
		inc      [16]byte
	}{
		{778, publicIDs[0][:], 777, 1, "Item", 3, [16]byte{4}},
		{779, publicIDs[1][:], 777, 1, "Second", 5, [16]byte{5}},
		{780, publicIDs[2][:], 777, 8, "Book", 1, [16]byte{6}},
		{781, publicIDs[3][:], 778, 1, "Foreign", 1, [16]byte{7}},
	} {
		ipBulkExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision,revision) VALUES(?,?,?,?,?,?,?,?,?,0,?,?,1,1,1,?)`,
			row.id, row.publicID, row.library, row.kind, row.title, strings.ToLower(row.title), strings.ToLower(row.title)[:1],
			strings.ToLower(row.title), strings.ToLower(row.title)[:1], row.inc[:], row.inc[:], row.revision)
	}
	r, err := stmttest.New(t, ipBulkApplyDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipEntryRegistry{r, keeper}, p
}

func ipBulkApplyTurn(t *testing.T, w *executor, r *ipEntryRegistry, kind CommandID) (*commandTurn, *transaction, ipEntryBudget) {
	t.Helper()
	turn, b := ipEntryTurn(t, w, r, ipEntryRequest{purpose: kind})
	return turn, turn.transaction, b
}

func ipBulkAck(t *testing.T, w *executor) {
	t.Helper()
	ipTextExec(t, w, `UPDATE metadata_repair_cascades SET acknowledged_command_sequence=last_command_sequence`)
}

func ipBulkBirth(t *testing.T, w *executor, r *ipEntryRegistry, p *usOwnerAuthorityProof) *ipEditBulkCommand {
	t.Helper()
	cmd := ipBulkCommand(t, ipBulkIdentity(1, 2), nil)
	turn, tx, b := ipBulkApplyTurn(t, w, r, IPEditBulkBegin)
	out, err := applyIPEditBulkBegin(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	return cmd
}

func ipBulkPageRequestFor(birth *ipEditBulkCommand, revision, after int64, targets []ipEditBulkTarget) ipEditBulkPageRequest {
	return ipEditBulkPageRequest{Kind: IPEditBulkPage, Root: birth.request.Root, Revision: revision,
		AfterOrdinal: after, Targets: append([]ipEditBulkTarget(nil), targets...)}
}

func ipBulkPageCommandFor(t *testing.T, identity CommandIdentity, birth *ipEditBulkCommand, revision, after int64, targets []ipEditBulkTarget, mutate func(*ipEditBulkPageRequest)) *ipEditBulkPageCommand {
	t.Helper()
	q := ipBulkPageRequestFor(birth, revision, after, targets)
	if mutate != nil {
		mutate(&q)
	}
	c, err := newIPEditBulkPageCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipBulkPageApply(t *testing.T, w *executor, r *ipEntryRegistry, p *usOwnerAuthorityProof, c *ipEditBulkPageCommand) (ipEditBulkPageCommandResult, *commandTurn, *transaction, error) {
	t.Helper()
	turn, tx, b := ipBulkApplyTurn(t, w, r, IPEditBulkPage)
	out, err := applyIPEditBulkPage(t.Context(), turn, p, t.Context(), b, c)
	return out, turn, tx, err
}

func ipBulkFinishCommandFor(t *testing.T, identity CommandIdentity, birth *ipEditBulkCommand, revision, after int64, mutate func(*ipEditBulkFinishRequest)) *ipEditBulkFinishCommand {
	t.Helper()
	q := ipEditBulkFinishRequest{Kind: IPEditBulkFinish, Root: birth.request.Root, Revision: revision, AfterOrdinal: after}
	if mutate != nil {
		mutate(&q)
	}
	c, err := newIPEditBulkFinishCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipBulkFinishApply(t *testing.T, w *executor, r *ipEntryRegistry, p *usOwnerAuthorityProof, c *ipEditBulkFinishCommand) (ipEditBulkFinishCommandResult, *commandTurn, *transaction, error) {
	t.Helper()
	turn, tx, b := ipBulkApplyTurn(t, w, r, IPEditBulkFinish)
	out, err := applyIPEditBulkFinish(t.Context(), turn, p, t.Context(), b, c)
	return out, turn, tx, err
}

func ipBulkPageOutputZero(v ipEditBulkPageCommandResult) bool {
	return v.Outcome.State == 0 && !v.Outcome.Replayed && v.Outcome.Evidence == nil &&
		v.Outcome.Reference == (ReceiptReference{}) && v.Value == (ipEditBulkPageResult{}) && v.Digest == (ResultDigest{})
}

func ipBulkFinishOutputZero(v ipEditBulkFinishCommandResult) bool {
	return v.Outcome.State == 0 && !v.Outcome.Replayed && v.Outcome.Evidence == nil &&
		v.Outcome.Reference == (ReceiptReference{}) && v.Value == (ipEditBulkFinishResult{}) && v.Digest == (ResultDigest{})
}

func TestIPEditBulkPageFreshReplayRetired(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipBulkAck(t, w)
	identity := ipBulkIdentity(2, 3)
	cmd := ipBulkPageCommandFor(t, identity, birth, 1, 0, ipBulkTargets(), nil)
	out, turn, tx, err := ipBulkPageApply(t, w, r, p, cmd)
	if err != nil || out.Outcome.State != Applied || out.Outcome.Replayed {
		t.Fatal(out, err)
	}
	id := out.Value.OwnerID
	if out.Value.Revision != 2 || out.Value.AfterOrdinal != 2 || out.Value.Captured != 2 ||
		out.Value.Processed != 2 || out.Value.Failed != 0 || !out.Value.NoNext ||
		out.Value.AppliedMS != w.now().UnixMilli() || out.Digest == (ResultDigest{}) {
		t.Fatal(out.Value)
	}
	ipTextCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_repair_cascades WHERE id='` + id + `' AND job_revision=2 AND last_command_sequence=2 AND acknowledged_command_sequence=1 AND after_item='2' AND processed=2 AND failed=0 AND status='running'`: 1,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND state='applied' AND edit_owner_id IS NOT NULL AND result_revision IS NOT NULL`:                                                          2,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND edit_command_incarnation=X'01000000000000000000000000000000' AND edit_command_sequence=1`:                                               2,
		`SELECT count(*) FROM metadata_owner_fields WHERE field='country' AND value='US' AND value_present=1`:                                                                                                                         2,
		`SELECT count(*) FROM metadata_owner_history`:                                 2,
		`SELECT count(*) FROM metadata_edit_receipts WHERE command_kind='field_edit'`: 2,
		`SELECT count(*) FROM metadata_edit_batch_receipts WHERE owner_id='` + id + `' AND command_kind='page' AND accepted_revision=2 AND after_ordinal=2 AND captured_count=2 AND processed_count=2 AND failed_count=0`: 1,
		`SELECT count(*) FROM catalog_entities WHERE id=778 AND revision=4 AND metadata_revision=2`:                                                                                                                       1,
		`SELECT count(*) FROM catalog_entities WHERE id=779 AND revision=6 AND metadata_revision=2`:                                                                                                                       1,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
	original := out

	// Catalogue movement after the page must not rebuild the original result.
	drift := [16]byte{12}
	ipTextExec(t, w, `UPDATE catalog_entities SET revision=99,metadata_incarnation=? WHERE id IN(778,779)`, drift[:])
	turn, tx, b := ipBulkApplyTurn(t, w, r, IPEditBulkPage)
	out, err = applyIPEditBulkPage(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !out.Outcome.Replayed || out.Digest != original.Digest || out.Value != original.Value {
		t.Fatal("catalogue drift altered the original page", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_batch_receipts WHERE command_kind='page'`); got != 1 {
		t.Fatal("replay wrote a page receipt", got)
	}
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_receipts`); got != 2 {
		t.Fatal("replay re-applied a target edit", got)
	}

	// At or below ACK is Retired even while the physical rows remain.
	ipBulkAck(t, w)
	turn, tx, b = ipBulkApplyTurn(t, w, r, IPEditBulkPage)
	out, err = applyIPEditBulkPage(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != ResultRetired || out.Outcome.Replayed || out.Value != (ipEditBulkPageResult{}) {
		t.Fatal("retired page served a value", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_batch_receipts WHERE command_kind='page'`); got != 1 {
		t.Fatal("retired removed the physical page receipt", got)
	}
}

func TestIPEditBulkPageLostResponseAfterItemEdit(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipBulkAck(t, w)
	cmd := ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil)
	first, turn, tx, err := ipBulkPageApply(t, w, r, p, cmd)
	if err != nil || first.Outcome.State != Applied {
		t.Fatal(first, err)
	}
	ipTextCommit(t, turn, tx)
	// The caller never saw the response; the same original request now resolves
	// its exact saved page instead of re-running the item edits.
	out, turn, tx, err := ipBulkPageApply(t, w, r, p, cmd)
	if err != nil || !out.Outcome.Replayed || out.Value != first.Value || out.Digest != first.Digest {
		t.Fatal(out, err)
	}
	ipTextRollback(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_edit_batch_receipts WHERE command_kind='page'`: 1,
		`SELECT count(*) FROM metadata_edit_receipts`:                                 2,
		`SELECT count(*) FROM metadata_owner_history`:                                 2,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
}

func TestIPEditBulkPageAllRejectedRawCursor(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipTextExec(t, w, `UPDATE catalog_entities SET retired=1 WHERE id IN(778,779)`)
	ipBulkAck(t, w)
	out, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil))
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	id := out.Value.OwnerID
	if out.Value.Processed != 0 || out.Value.Failed != 2 || out.Value.AfterOrdinal != 2 || !out.Value.NoNext {
		t.Fatal(out.Value)
	}
	ipTextCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND state='stale' AND edit_owner_id IS NULL AND result_revision IS NULL`: 2,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND state='captured'`:                                                    0,
		`SELECT count(*) FROM metadata_repair_cascades WHERE id='` + id + `' AND processed=0 AND failed=2 AND after_item='2'`:                                      1,
		`SELECT count(*) FROM metadata_edit_receipts`: 0,
		`SELECT count(*) FROM metadata_owner_fields`:  0,
		`SELECT count(*) FROM metadata_owner_history`: 0,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}

	// The truthful terminal still accepts an all-stale cohort.
	ipBulkAck(t, w)
	finish := ipBulkFinishCommandFor(t, ipBulkIdentity(3, 4), birth, 2, 2, nil)
	done, turn, tx, err := ipBulkFinishApply(t, w, r, p, finish)
	if err != nil || done.Outcome.State != Applied || done.Value.Processed != 0 || done.Value.Failed != 2 || !done.Value.Complete {
		t.Fatal(done, err)
	}
	ipTextCommit(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_repair_cascades WHERE id='`+id+`' AND status='complete' AND job_revision=3`); got != 1 {
		t.Fatal("all-stale terminal not complete", got)
	}
}

func TestIPEditBulkPageEmptyNoNextVersusRemainingInput(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipBulkAck(t, w)
	// An empty page while captured input remains is refused, not a false end.
	out, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, nil, nil))
	if !ipBulkPageOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("empty page before the end was admitted", out, err)
	}
	ipTextRollback(t, turn, tx)
	// One real page leaves a remaining target, so the next page is not no-next.
	first, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets()[:1], nil))
	if err != nil || first.Outcome.State != Applied || first.Value.AfterOrdinal != 1 || first.Value.NoNext {
		t.Fatal(first, err)
	}
	ipTextCommit(t, turn, tx)
	ipBulkAck(t, w)
	out, turn, tx, err = ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(3, 4), birth, 2, 1, nil, nil))
	if !ipBulkPageOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("empty page with remaining input was admitted", out, err)
	}
	ipTextRollback(t, turn, tx)
	// The truthful no-next page is at the full cursor with nothing left.
	second, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(3, 4), birth, 2, 1, ipBulkTargets()[1:], nil))
	if err != nil || second.Outcome.State != Applied || !second.Value.NoNext {
		t.Fatal(second, err)
	}
	ipTextCommit(t, turn, tx)
	ipBulkAck(t, w)
	empty, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(4, 5), birth, 3, 2, nil, nil))
	if err != nil || empty.Outcome.State != Applied || !empty.Value.NoNext ||
		empty.Value.Processed != 2 || empty.Value.Failed != 0 || empty.Value.Revision != 4 {
		t.Fatal(empty, err)
	}
	ipTextCommit(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_batch_receipts WHERE command_kind='page'`); got != 3 {
		t.Fatal(got)
	}
}

// A page target pins the EXACT atomic edit receipt it produced, and that pin
// survives an owner ACK because ACK never removes the physical proof.
func TestIPEditBulkPageExactTargetPinAcrossACK(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipBulkAck(t, w)
	identity := ipBulkIdentity(2, 3)
	out, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, identity, birth, 1, 0, ipBulkTargets(), nil))
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	id := out.Value.OwnerID
	for ordinal, entity := range map[int64]int64{0: 778, 1: 779} {
		var ownerID, sequence, accepted int64
		var incarnation []byte
		if err = w.db.QueryRowContext(t.Context(), `SELECT edit_owner_id,edit_command_incarnation,edit_command_sequence,result_revision FROM metadata_edit_batch_targets WHERE operation_id=? AND ordinal=? AND entity_id=? AND state='applied'`, id, ordinal, entity).
			Scan(&ownerID, &incarnation, &sequence, &accepted); err != nil {
			t.Fatal(err)
		}
		wantOperation := ipEditBulkTargetOperation(identity, ordinal)
		var operation []byte
		var receiptSubject, receiptOwner, receiptSequence int64
		if err = w.db.QueryRowContext(t.Context(), `SELECT operation_id,subject_revision,owner_id,command_sequence FROM metadata_edit_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, ownerID, incarnation, sequence).
			Scan(&operation, &receiptSubject, &receiptOwner, &receiptSequence); err != nil {
			t.Fatal(err)
		}
		if len(incarnation) != 16 || ownerID != receiptOwner || sequence != receiptSequence ||
			!bytes.Equal(operation, wantOperation[:]) || accepted != receiptSubject {
			t.Fatal(ordinal, ownerID, incarnation, sequence, accepted, receiptSubject)
		}
	}
	// Owner ACK moves the sequence floor but retains the live pins.
	ipTextExec(t, w, `UPDATE metadata_edit_owners SET acknowledged_command_sequence=last_command_sequence`)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_batch_targets t JOIN metadata_edit_receipts r ON r.owner_id=t.edit_owner_id AND r.command_incarnation=t.edit_command_incarnation AND r.command_sequence=t.edit_command_sequence WHERE t.operation_id='`+id+`' AND r.subject_revision=t.result_revision`); got != 2 {
		t.Fatal("pins did not survive owner ACK", got)
	}
	ipBulkAck(t, w)
	done, turn, tx, err := ipBulkFinishApply(t, w, r, p, ipBulkFinishCommandFor(t, ipBulkIdentity(3, 4), birth, 2, 2, nil))
	if err != nil || done.Outcome.State != Applied || !done.Value.Complete {
		t.Fatal(done, err)
	}
	ipTextCommit(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_repair_cascades WHERE id='`+id+`' AND status='complete' AND last_command_sequence=3 AND job_revision=3`); got != 1 {
		t.Fatal(got)
	}
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_batch_receipts WHERE owner_id='`+id+`' AND command_kind='finish' AND accepted_revision=3 AND after_ordinal=2 AND processed_count=2 AND failed_count=0`); got != 1 {
		t.Fatal(got)
	}
}

func TestIPEditBulkFinishRefusesRemainingInput(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipBulkAck(t, w)
	if out, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets()[:1], nil)); err != nil {
		t.Fatal(out, err)
	} else {
		ipTextCommit(t, turn, tx)
	}
	// The declared cursor equals the expected count, but one target is still raw.
	out, turn, tx, err := ipBulkFinishApply(t, w, r, p, ipBulkFinishCommandFor(t, ipBulkIdentity(3, 4), birth, 2, 2, nil))
	if !ipBulkFinishOutputZero(out) || err == nil {
		t.Fatal("terminal accepted remaining input", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_repair_cascades WHERE status='complete'`); got != 0 {
		t.Fatal(got)
	}
	// A terminal revision that has already moved is a conflict, not a replay.
	ipBulkAck(t, w)
	if out, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(3, 4), birth, 2, 1, ipBulkTargets()[1:], nil)); err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	} else {
		ipTextCommit(t, turn, tx)
	}
	ipBulkAck(t, w)
	out, turn, tx, err = ipBulkFinishApply(t, w, r, p, ipBulkFinishCommandFor(t, ipBulkIdentity(4, 5), birth, 2, 2, nil))
	if !ipBulkFinishOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("stale terminal revision admitted", out, err)
	}
	ipTextRollback(t, turn, tx)
	done, turn, tx, err := ipBulkFinishApply(t, w, r, p, ipBulkFinishCommandFor(t, ipBulkIdentity(4, 5), birth, 3, 2, nil))
	if err != nil || done.Outcome.State != Applied || !done.Value.Complete || done.Value.Processed != 2 {
		t.Fatal(done, err)
	}
	ipTextCommit(t, turn, tx)
	// Finish replay then ACK/Retired.
	again, turn, tx, err := ipBulkFinishApply(t, w, r, p, ipBulkFinishCommandFor(t, ipBulkIdentity(4, 5), birth, 3, 2, nil))
	if err != nil || !again.Outcome.Replayed || again.Value != done.Value {
		t.Fatal(again, err)
	}
	ipTextRollback(t, turn, tx)
	ipBulkAck(t, w)
	retired, turn, tx, err := ipBulkFinishApply(t, w, r, p, ipBulkFinishCommandFor(t, ipBulkIdentity(4, 5), birth, 3, 2, nil))
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipEditBulkFinishResult{}) {
		t.Fatal(retired, err)
	}
	ipTextRollback(t, turn, tx)
}

func TestIPEditBulkPageCurrentAuthority(t *testing.T) {
	for _, mode := range []string{"forged_proof", "revoked_family", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := ipBulkApplyFixture(t)
			birth := ipBulkBirth(t, w, r, p)
			ipBulkAck(t, w)
			cmd := ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil)
			if out, turn, tx, err := ipBulkPageApply(t, w, r, p, cmd); err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			} else {
				ipTextCommit(t, turn, tx)
			}
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b := ipBulkApplyTurn(t, w, r, IPEditBulkPage)
			out, err := applyIPEditBulkPage(t.Context(), turn, proof, t.Context(), b, cmd)
			if !ipBulkPageOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale authority served the saved page", out, err)
			}
			ipTextRollback(t, turn, tx)
		})
	}
	// A cancelled caller lifetime redacts an otherwise fresh page.
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipBulkAck(t, w)
	turn, tx, b := ipBulkApplyTurn(t, w, r, IPEditBulkPage)
	lifetime, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := applyIPEditBulkPage(t.Context(), turn, p, lifetime, b, ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil))
	if !ipBulkPageOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("cancelled lifetime released page output", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_batch_targets WHERE state='applied'`); got != 0 {
		t.Fatal("cancelled lifetime applied a target", got)
	}

	// The terminal checks current authority before disclosing its saved result.
	for _, mode := range []string{"forged_proof", "revoked_family", "revoked_membership"} {
		t.Run("finish_"+mode, func(t *testing.T) {
			w2, r2, p2 := ipBulkApplyFixture(t)
			birth2 := ipBulkBirth(t, w2, r2, p2)
			ipBulkAck(t, w2)
			if out, turn, tx, err := ipBulkPageApply(t, w2, r2, p2, ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth2, 1, 0, ipBulkTargets(), nil)); err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			} else {
				ipTextCommit(t, turn, tx)
			}
			ipBulkAck(t, w2)
			finish := ipBulkFinishCommandFor(t, ipBulkIdentity(3, 4), birth2, 2, 2, nil)
			if out, turn, tx, err := ipBulkFinishApply(t, w2, r2, p2, finish); err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			} else {
				ipTextCommit(t, turn, tx)
			}
			proof2 := ipPolicyRevoke(t, w2, p2, mode)
			turn, tx, b := ipBulkApplyTurn(t, w2, r2, IPEditBulkFinish)
			got, e := applyIPEditBulkFinish(t.Context(), turn, proof2, t.Context(), b, finish)
			if !ipBulkFinishOutputZero(got) || !errors.Is(e, ErrDisclosureDenied) {
				t.Fatal("stale authority served the saved terminal", got, e)
			}
			ipTextRollback(t, turn, tx)
		})
	}
}

func TestIPEditBulkPageRollbackAndConflict(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipBulkAck(t, w)
	if _, err := w.db.ExecContext(t.Context(), `CREATE TRIGGER ip_bulk_page_fail BEFORE INSERT ON metadata_edit_batch_receipts WHEN NEW.command_kind='page' BEGIN SELECT RAISE(ABORT,'page');END`); err != nil {
		t.Fatal(err)
	}
	out, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil))
	if err == nil || !ipBulkPageOutputZero(out) {
		t.Fatal("failed page returned public output", out, err)
	}
	ipTextRollback(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE state='applied'`:                                            0,
		`SELECT count(*) FROM metadata_edit_receipts`:                                                                       0,
		`SELECT count(*) FROM metadata_owner_fields`:                                                                        0,
		`SELECT count(*) FROM metadata_repair_cascades WHERE job_revision=1 AND after_item='' AND processed=0 AND failed=0`: 1,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
	if _, err = w.db.ExecContext(t.Context(), `DROP TRIGGER ip_bulk_page_fail`); err != nil {
		t.Fatal(err)
	}

	// Same sequence with a different original request is a conflict.
	first := ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil)
	if out, turn, tx, err := ipBulkPageApply(t, w, r, p, first); err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	} else {
		ipTextCommit(t, turn, tx)
	}
	conflicting := ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), func(q *ipEditBulkPageRequest) { q.Root.Actor = "Intruder" })
	out, turn, tx, err = ipBulkPageApply(t, w, r, p, conflicting)
	if !ipBulkPageOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting page admitted", out, err)
	}
	ipTextRollback(t, turn, tx)

	// A different cursor under the same retained sequence cannot rewrite it.
	conflicting = ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, nil, func(q *ipEditBulkPageRequest) { q.AfterOrdinal = 0 })
	out, turn, tx, err = ipBulkPageApply(t, w, r, p, conflicting)
	if !ipBulkPageOutputZero(out) || err == nil {
		t.Fatal("retained page accepted a different request shape", out, err)
	}
	ipTextRollback(t, turn, tx)
}

func TestIPEditBulkPageRefusesTamperedInputCohort(t *testing.T) {
	for name, mutate := range map[string]string{
		"rewritten_field": `UPDATE metadata_edit_batch_fields SET value='CA' WHERE operation_id=?`,
		"dropped_field":   `DELETE FROM metadata_edit_batch_fields WHERE operation_id=?`,
		"extra_target":    `INSERT INTO metadata_edit_batch_targets(operation_id,ordinal,entity_id,expected_entity_incarnation,expected_revision,state) VALUES(?,2,780,X'0A000000000000000000000000000000',1,'captured')`,
		"moved_revision":  `UPDATE metadata_edit_batch_targets SET expected_revision=4 WHERE operation_id=? AND ordinal=1`,
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := ipBulkApplyFixture(t)
			birth := ipBulkBirth(t, w, r, p)
			ipBulkAck(t, w)
			id := ipEditBulkOperationID(birth.identity.OperationID)
			ipTextExec(t, w, mutate, id)
			out, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil))
			if !ipBulkPageOutputZero(out) || err == nil {
				t.Fatal("tampered captured cohort was applied", out, err)
			}
			ipTextRollback(t, turn, tx)
			if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_receipts`); got != 0 {
				t.Fatal(got)
			}
		})
	}
}

func TestIPEditBulkPageUnknownCommit(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipBulkAck(t, w)
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	out, _, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil))
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	id := out.Value.OwnerID
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	// The durable outcome is read through a FRESH read-only connection, never
	// the quarantined writer pool.
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_repair_cascades WHERE id='` + id + `' AND after_item='2' AND processed=2 AND failed=0`:                                                                                                1,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND state='applied'`:                                                                                                               2,
		`SELECT count(*) FROM metadata_edit_batch_receipts WHERE owner_id='` + id + `' AND command_kind='page'`:                                                                                                              1,
		`SELECT count(*) FROM metadata_edit_receipts WHERE command_kind='field_edit'`:                                                                                                                                        2,
		`SELECT count(*) FROM metadata_edit_batch_targets t JOIN metadata_edit_receipts r ON r.owner_id=t.edit_owner_id AND r.command_incarnation=t.edit_command_incarnation AND r.command_sequence=t.edit_command_sequence`: 2,
	} {
		if got := ipPolicyFreshInt(t, path, query); got != want {
			t.Fatal(query, got, want)
		}
	}
}

func TestIPEditBulkPageBounds(t *testing.T) {
	identity := ipBulkIdentity(2, 3)
	base := ipBulkRawRequest(ipBulkIdentity(1, 2))
	birth := &ipEditBulkCommand{identity: ipBulkIdentity(1, 2), request: base, valid: true}
	if c, err := newIPEditBulkPageCommand(identity, ipBulkPageRequestFor(birth, 0, 0, nil)); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("page without a declared root revision admitted", c, err)
	}
	// Exactly one bounded page is admitted; one more is not.
	big := base.Root
	big.ExpectedCount = ipEditBulkPageSize
	big.ExpectedBytes = ipBulkFieldBytes(big.Fields) + ipEditBulkTargetBytes*ipEditBulkPageSize
	big.Manifest = [32]byte{1}
	wide := &ipEditBulkCommand{identity: ipBulkIdentity(1, 2), request: ipEditBulkRequest{Kind: IPEditBulkBegin, Root: big}}
	targets := make([]ipEditBulkTarget, ipEditBulkPageSize)
	for j := range targets {
		targets[j] = ipEditBulkTarget{Ordinal: int64(j), EntityID: int64(2000 + j), Incarnation: [16]byte{13}, Revision: 1}
	}
	if _, err := newIPEditBulkPageCommand(identity, ipBulkPageRequestFor(wide, 1, 0, targets)); err != nil {
		t.Fatal("maximum page refused", err)
	}
	over := append(append([]ipEditBulkTarget(nil), targets...), ipEditBulkTarget{Ordinal: ipEditBulkPageSize, EntityID: 3000, Incarnation: [16]byte{13}, Revision: 1})
	if c, err := newIPEditBulkPageCommand(identity, ipBulkPageRequestFor(wide, 1, 0, over)); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize page admitted", c, err)
	}
	dup := append([]ipEditBulkTarget(nil), targets...)
	dup[1].EntityID = dup[0].EntityID
	if c, err := newIPEditBulkPageCommand(identity, ipBulkPageRequestFor(wide, 1, 0, dup)); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate page entity admitted", c, err)
	}
	gap := append([]ipEditBulkTarget(nil), targets...)
	gap[1].Ordinal = 7
	if c, err := newIPEditBulkPageCommand(identity, ipBulkPageRequestFor(wide, 1, 0, gap)); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("gapped page ordinal admitted", c, err)
	}
	outside := append([]ipEditBulkTarget(nil), targets...)
	outside[0] = ipEditBulkTarget{Ordinal: 0, EntityID: 2000, Incarnation: [16]byte{13}, Revision: 1}
	if c, err := newIPEditBulkPageCommand(identity, ipBulkPageRequestFor(birth, 1, 0, outside)); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("page ordinal beyond the declared count admitted", c, err)
	}
	if c, err := newIPEditBulkPageCommand(identity, ipBulkPageRequestFor(birth, 1, 3, nil)); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("page cursor beyond the declared count admitted", c, err)
	}
	if c, err := newIPEditBulkPageCommand(identity, ipBulkPageRequestFor(birth, 1, base.Root.ExpectedCount, nil)); c == nil || !c.valid || err != nil {
		t.Fatal("truthful no-next page refused", c, err)
	}
	// The terminal must declare the exact captured end.
	if c, err := newIPEditBulkFinishCommand(ipBulkIdentity(3, 4), ipEditBulkFinishRequest{Kind: IPEditBulkFinish, Root: base.Root, Revision: 1, AfterOrdinal: 1}); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("terminal before the captured end admitted", c, err)
	}
	if c, err := newIPEditBulkFinishCommand(ipBulkIdentity(3, 4), ipEditBulkFinishRequest{Kind: IPEditBulkFinish, Root: base.Root, Revision: 0, AfterOrdinal: base.Root.ExpectedCount}); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("terminal without a revision admitted", c, err)
	}
	if _, err := newIPEditBulkFinishCommand(ipBulkIdentity(3, 4), ipEditBulkFinishRequest{Kind: IPEditBulkFinish, Root: base.Root, Revision: 1, AfterOrdinal: base.Root.ExpectedCount}); err != nil {
		t.Fatal("truthful terminal refused", err)
	}
}

func TestIPEditBulkApplyDescriptors(t *testing.T) {
	for _, want := range []Descriptor{
		{ID: IPEditBulkPage, Name: "ip_edit_bulk_page", Durability: Normal},
		{ID: IPEditBulkFinish, Name: "ip_edit_bulk_finish", Durability: Normal},
	} {
		actual, err := Describe(want.ID)
		if err != nil || actual != want {
			t.Fatal(actual, err)
		}
	}
	if ipEditBulkPageSize != 16 {
		t.Fatal(ipEditBulkPageSize)
	}
}

func TestIPEditBulkPageResultPreflight(t *testing.T) {
	identity := ipBulkIdentity(2, 3)
	page := ipEditBulkPageResult{OwnerID: ipEditBulkOperationID([16]byte{0xde, 0xad}), Revision: 2, AfterOrdinal: 2,
		Captured: 2, Processed: 2, Failed: 0, NoNext: true, AppliedMS: 7, Manifest: [32]byte{1}}
	digest, err := ipEditBulkPageResultDigest(identity, RequestDigest{3}, page)
	if err != nil || digest == (ResultDigest{}) {
		t.Fatal(digest, err)
	}
	for name, mutate := range map[string]func(*ipEditBulkPageResult){
		"zero_owner":      func(v *ipEditBulkPageResult) { v.OwnerID = "" },
		"uppercase_owner": func(v *ipEditBulkPageResult) { v.OwnerID = strings.ToUpper(v.OwnerID) },
		"zero_revision":   func(v *ipEditBulkPageResult) { v.Revision = 0 },
		"cursor_over":     func(v *ipEditBulkPageResult) { v.AfterOrdinal = 3 },
		"counts_mismatch": func(v *ipEditBulkPageResult) { v.Failed = 1; v.Processed = 2 },
		"false_no_next":   func(v *ipEditBulkPageResult) { v.NoNext = false },
		"zero_manifest":   func(v *ipEditBulkPageResult) { v.Manifest = [32]byte{} },
		"negative_time":   func(v *ipEditBulkPageResult) { v.AppliedMS = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := page
			mutate(&bad)
			var zero [32]byte
			if got, err := ipEditBulkPageResultDigest(identity, RequestDigest{3}, bad); err == nil || got != ResultDigest(zero) {
				t.Fatal(got, err)
			}
		})
	}
	finish := ipEditBulkFinishResult{OwnerID: page.OwnerID, Revision: 3, AfterOrdinal: 2, Captured: 2,
		Processed: 1, Failed: 1, Complete: true, AppliedMS: 7, Manifest: [32]byte{1}}
	digest, err = ipEditBulkFinishResultDigest(identity, RequestDigest{3}, finish)
	if err != nil || digest == (ResultDigest{}) {
		t.Fatal(digest, err)
	}
	for name, mutate := range map[string]func(*ipEditBulkFinishResult){
		"not_complete":    func(v *ipEditBulkFinishResult) { v.Complete = false },
		"short_cursor":    func(v *ipEditBulkFinishResult) { v.AfterOrdinal = 1 },
		"counts_mismatch": func(v *ipEditBulkFinishResult) { v.Processed = 2 },
		"zero_manifest":   func(v *ipEditBulkFinishResult) { v.Manifest = [32]byte{} },
	} {
		t.Run(name, func(t *testing.T) {
			bad := finish
			mutate(&bad)
			var zero [32]byte
			if got, err := ipEditBulkFinishResultDigest(identity, RequestDigest{3}, bad); err == nil || got != ResultDigest(zero) {
				t.Fatal(got, err)
			}
		})
	}
}

// The terminal is an accepting command too: a cancelled lifetime releases no
// output, and an uncertain COMMIT is resolved only through a fresh read-only
// connection, never the quarantined writer pool.
func TestIPEditBulkFinishCancelledAndUnknownCommit(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipBulkAck(t, w)
	if out, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil)); err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	} else {
		ipTextCommit(t, turn, tx)
	}
	ipBulkAck(t, w)
	finish := ipBulkFinishCommandFor(t, ipBulkIdentity(3, 4), birth, 2, 2, nil)
	turn, tx, b := ipBulkApplyTurn(t, w, r, IPEditBulkFinish)
	lifetime, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := applyIPEditBulkFinish(t.Context(), turn, p, lifetime, b, finish)
	if !ipBulkFinishOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("cancelled lifetime released terminal output", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_repair_cascades WHERE status='complete'`); got != 0 {
		t.Fatal("cancelled lifetime completed the bulk operation", got)
	}

	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	out, _, tx, err = ipBulkFinishApply(t, w, r, p, finish)
	if err != nil || out.Outcome.State != Applied || !out.Value.Complete {
		t.Fatal(out, err)
	}
	id := out.Value.OwnerID
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_repair_cascades WHERE id='` + id + `' AND status='complete' AND job_revision=3`: 1,
		`SELECT count(*) FROM metadata_edit_batch_receipts WHERE owner_id='` + id + `' AND command_kind='finish'`:      1,
	} {
		if got := ipPolicyFreshInt(t, path, query); got != want {
			t.Fatal(query, got, want)
		}
	}
}

// A fresh page whose declared owner revision has already moved is a conflict;
// only the exact post-page revision may consume the next allowance.
func TestIPEditBulkPageStaleRevision(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipBulkAck(t, w)
	if out, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil)); err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	} else {
		ipTextCommit(t, turn, tx)
	}
	ipBulkAck(t, w)
	out, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(3, 4), birth, 1, 2, nil, nil))
	if !ipBulkPageOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("stale page revision admitted", out, err)
	}
	ipTextRollback(t, turn, tx)
}

// A target edit owns its OWN command sequence, never the page's. An existing
// owner advances from its own last (even far past the page sequence), and a
// brand-new owner is born at sequence 1 with last=1/ack=0.
func TestIPEditBulkPageTargetOwnerSequence(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipTextExec(t, w, `INSERT INTO metadata_edit_owners(entity_id,kind,command_incarnation,revision,last_command_sequence,acknowledged_command_sequence) VALUES(778,'item',X'21000000000000000000000000000000',3,5,5)`)
	ipBulkAck(t, w)
	cmd := ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil)
	out, turn, tx, err := ipBulkPageApply(t, w, r, p, cmd)
	if err != nil || out.Outcome.State != Applied || out.Value.Processed != 2 || out.Value.Failed != 0 || !out.Value.NoNext {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	id := out.Value.OwnerID
	first := out.Value
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_edit_owners WHERE entity_id=778 AND revision=4 AND last_command_sequence=6 AND acknowledged_command_sequence=5`:                                                                    1,
		`SELECT count(*) FROM metadata_edit_owners WHERE entity_id=779 AND command_incarnation=X'01000000000000000000000000000000' AND revision=2 AND last_command_sequence=1 AND acknowledged_command_sequence=0`:        1,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND ordinal=0 AND edit_command_sequence=6 AND edit_command_incarnation=X'21000000000000000000000000000000' AND state='applied'`: 1,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND ordinal=1 AND edit_command_sequence=1 AND edit_command_incarnation=X'01000000000000000000000000000000' AND state='applied'`: 1,
		`SELECT count(*) FROM metadata_edit_receipts WHERE command_incarnation=X'21000000000000000000000000000000' AND command_sequence=6`:                                                                                1,
		`SELECT count(*) FROM metadata_edit_receipts WHERE command_incarnation=X'01000000000000000000000000000000' AND command_sequence=1`:                                                                                1,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
	receipts := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_receipts`)
	// The page replay is served by its own receipt and writes nothing new.
	out, turn, tx, err = ipBulkPageApply(t, w, r, p, cmd)
	if err != nil || !out.Outcome.Replayed || out.Value != first {
		t.Fatal("page replay served a different result", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_receipts`); got != receipts {
		t.Fatal("replay wrote a target receipt", got)
	}
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_owners`); got != 2 {
		t.Fatal("replay wrote an edit owner", got)
	}
}

// An unacknowledged predecessor cannot admit a fresh target command. The target
// is recorded stale and the page still applies its remaining eligible targets.
func TestIPEditBulkPageUnackedTargetOwnerStale(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	ipTextExec(t, w, `INSERT INTO metadata_edit_owners(entity_id,kind,command_incarnation,revision,last_command_sequence,acknowledged_command_sequence) VALUES(778,'item',X'21000000000000000000000000000000',2,5,4)`)
	ipBulkAck(t, w)
	cmd := ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil)
	out, turn, tx, err := ipBulkPageApply(t, w, r, p, cmd)
	if err != nil || out.Outcome.State != Applied || out.Value.Processed != 1 || out.Value.Failed != 1 || !out.Value.NoNext {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	id := out.Value.OwnerID
	first := out.Value
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_edit_owners WHERE entity_id=778 AND revision=2 AND last_command_sequence=5 AND acknowledged_command_sequence=4`:                           1,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND ordinal=0 AND state='stale' AND edit_owner_id IS NULL AND result_revision IS NULL`: 1,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND ordinal=1 AND state='applied'`:                                                     1,
		`SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778`:                                                                                                         0,
		`SELECT count(*) FROM metadata_edit_receipts`: 1,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
	receipts := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_receipts`)
	out, turn, tx, err = ipBulkPageApply(t, w, r, p, cmd)
	if err != nil || !out.Outcome.Replayed || out.Value != first {
		t.Fatal("stale-page replay served a different result", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_receipts`); got != receipts {
		t.Fatal("replay wrote a target receipt", got)
	}
}

// A target whose captured bundle cannot be validated against its current state
// is stale for this page, never a page-aborting error: locking studio where one
// target has no studio value must still apply the valid target.
func TestIPEditBulkPageIneligibleTargetStale(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkCommand(t, ipBulkIdentity(1, 2), func(q *ipEditBulkRequest) {
		q.Root.Fields = []ipEditBulkField{{Field: fieldmeta.Studio, Edit: fieldmeta.TextEdit{Action: fieldmeta.LockText}}}
	})
	turn, tx, b := ipBulkApplyTurn(t, w, r, IPEditBulkBegin)
	if begun, err := applyIPEditBulkBegin(t.Context(), turn, p, t.Context(), b, birth); err != nil || begun.Outcome.State != Applied {
		t.Fatal(begun, err)
	}
	ipTextCommit(t, turn, tx)
	ipTextExec(t, w, `UPDATE catalog_entities SET studio='Studio A' WHERE id=778`)
	ipBulkAck(t, w)
	cmd := ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil)
	out, turn, tx, err := ipBulkPageApply(t, w, r, p, cmd)
	if err != nil || out.Outcome.State != Applied || out.Value.Processed != 1 || out.Value.Failed != 1 ||
		out.Value.AfterOrdinal != 2 || !out.Value.NoNext {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	id := out.Value.OwnerID
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND ordinal=0 AND state='applied'`:                         1,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND ordinal=1 AND state='stale' AND edit_owner_id IS NULL`: 1,
		`SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778 AND field='studio' AND value='Studio A' AND value_present=1 AND locked=1`:    1,
		`SELECT count(*) FROM metadata_owner_fields WHERE entity_id=779`:                                                                             0,
		`SELECT count(*) FROM metadata_edit_receipts`:                                                                                                1,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
	receipts := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_receipts`)
	out, turn, tx, err = ipBulkPageApply(t, w, r, p, cmd)
	if err != nil || !out.Outcome.Replayed || out.Value.Processed != 1 || out.Value.Failed != 1 {
		t.Fatal("ineligible-target replay differed", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_receipts`); got != receipts {
		t.Fatal("replay wrote a target receipt", got)
	}
}

// A reborn entity under the same id AND revision was never captured. The page
// proves the captured metadata incarnation, records it stale and edits nothing.
func TestIPEditBulkPageRebornTargetStale(t *testing.T) {
	w, r, p := ipBulkApplyFixture(t)
	birth := ipBulkBirth(t, w, r, p)
	reborn := [16]byte{0x44}
	ipTextExec(t, w, `UPDATE catalog_entities SET metadata_incarnation=? WHERE id=778`, reborn[:])
	ipBulkAck(t, w)
	out, turn, tx, err := ipBulkPageApply(t, w, r, p, ipBulkPageCommandFor(t, ipBulkIdentity(2, 3), birth, 1, 0, ipBulkTargets(), nil))
	if err != nil || out.Outcome.State != Applied || out.Value.Processed != 1 || out.Value.Failed != 1 || !out.Value.NoNext {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	id := out.Value.OwnerID
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND ordinal=0 AND state='stale' AND edit_owner_id IS NULL AND result_revision IS NULL`: 1,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id='` + id + `' AND ordinal=1 AND state='applied'`:                                                     1,
		`SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778`:                                                                                                         0,
		`SELECT count(*) FROM metadata_edit_receipts r JOIN metadata_edit_owners o ON o.id=r.owner_id WHERE o.entity_id=778`:                                                     0,
		`SELECT count(*) FROM metadata_owner_fields WHERE entity_id=779`:                                                                                                         1,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
}
