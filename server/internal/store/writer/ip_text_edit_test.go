package writer

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func ipTextEditFixture(t *testing.T) (*executor, *ipEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, p := ipEntrySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ip-text-edit-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT OR IGNORE INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(8,'book',1,0,1,1,0)`,
	} {
		wc008NativeExec(t, w, sql)
	}
	itemToken, bookToken, inc := [16]byte{8}, [16]byte{9}, [16]byte{4}
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(778,?,777,1),(779,?,777,8)`, itemToken[:], bookToken[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision) VALUES(778,?,777,1,'Title','title','t','title','t',0,?,?,1,1,1)`, itemToken[:], inc[:], inc[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision) VALUES(779,?,777,8,'Book','book','b','book','b',0,?,?,1,1,1)`, bookToken[:], inc[:], inc[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(778,'person-token','person:key','Person','person',?)`, inc[:])
	r, err := stmttest.New(t, ipTextEditDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipEntryRegistry{r, keeper}, p
}

func ipTextEditTurn(t *testing.T, w *executor, r *ipEntryRegistry) (*commandTurn, *transaction, ipEntryBudget) {
	t.Helper()
	turn, b := ipEntryTurn(t, w, r, ipEntryRequest{purpose: IPFieldEdit})
	return turn, turn.transaction, b
}

func ipTextEditIdentity(sequence int64, operation byte) CommandIdentity {
	return CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
}

func ipTextEditProjection(title string) ipTextProjection {
	projected, err := ProjectFilenameMetadata(title, "", "")
	if err != nil {
		panic(err)
	}
	return ipTextProjection{projected.Title, projected.SortTitle, projected.MetadataLanguage,
		projected.SortKey, projected.SortHead, projected.SortKeyKeepArticles, projected.SortHeadKeepArticles}
}

// ipTextEditItemRequest is the first owner edit of the item fixture: one title
// set from the accepted projection with no owner row yet.
func ipTextEditItemRequest(sequence int64) ipTextEditRequest {
	title := ipTextEditProjection("Title")
	return ipTextEditRequest{Kind: IPFieldEdit, Source: ipTextEditSource{
		EntityID: 778, Kind: "item", TargetKind: 1, LibraryID: 777, LibraryKey: "ip-library",
		Actor: "Owner", OwnerRevision: 1, SubjectRevision: 1, MetadataRevision: 1,
		SourceRevision: 1, IdentityRevision: 1, MetadataIncarnation: [16]byte{4}, Projection: title,
	}, Fields: []ipTextEditField{{Field: fieldmeta.Title,
		Before: fieldmeta.TextState{Effective: fieldmeta.Text{Value: "Title", Present: true}},
		Edit:   fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "The Voyage"}}}}
}

func ipTextEditNewCommand(t *testing.T, sequence int64, operation byte, request ipTextEditRequest) *ipTextEditCommand {
	t.Helper()
	c, err := newIPTextEditCommand(ipTextEditIdentity(sequence, operation), request)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipTextCommit(t *testing.T, turn *commandTurn, tx *transaction) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.commit(); err != nil {
		t.Fatal(err)
	}
}

func ipTextRollback(t *testing.T, turn *commandTurn, tx *transaction) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func ipTextEditResultZero(v ipTextEditResult) bool {
	return v.OwnerID == 0 && v.EntityID == 0 && v.PersonID == 0 && v.Revision == 0 && v.SubjectRevision == 0 &&
		v.AppliedMS == 0 && !v.History.Present && len(v.Fields) == 0
}

func ipTextEditOutputZero(v ipTextEditCommandResult) bool {
	return v.Outcome.State == 0 && !v.Outcome.Replayed && v.Outcome.Evidence == nil &&
		v.Outcome.Reference == (ReceiptReference{}) && ipTextEditResultZero(v.Value) && v.Digest == (ResultDigest{})
}

func ipTextQueryInt(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

func ipTextExec(t *testing.T, w *executor, query string, args ...any) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(query, err)
	}
}

func TestIPTextEditFreshExactReplayRetired(t *testing.T) {
	w, r, p := ipTextEditFixture(t)
	cmd := ipTextEditNewCommand(t, 1, 2, ipTextEditItemRequest(1))
	turn, tx, b := ipTextEditTurn(t, w, r)
	out, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.Revision != 2 ||
		out.Value.SubjectRevision != 2 || out.Value.AppliedMS != w.now().UnixMilli() || out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	if got := out.Value.Fields[0].Change.State.Effective; !got.Present || got.Value != "The Voyage" || !out.Value.Fields[0].Change.Changed {
		t.Fatal("actual field", out.Value.Fields)
	}
	ipTextCommit(t, turn, tx)
	original := out
	for query, want := range map[string]int64{
		`SELECT revision FROM catalog_entities WHERE id=778`:                                              2,
		`SELECT count(*) FROM catalog_entities WHERE id=778 AND title='The Voyage' AND sort_key='voyage'`: 1,
		`SELECT metadata_revision FROM catalog_entities WHERE id=778`:                                     2,
		`SELECT count(*) FROM metadata_edit_owners WHERE entity_id=778`:                                   1,
		`SELECT revision FROM metadata_edit_owners WHERE entity_id=778`:                                   2,
		`SELECT last_command_sequence FROM metadata_edit_owners WHERE entity_id=778`:                      1,
		`SELECT count(*) FROM metadata_edit_receipts`:                                                     1,
		`SELECT count(*) FROM metadata_edit_receipt_fields WHERE field='title'`:                           1,
		`SELECT count(*) FROM metadata_owner_history_fields WHERE field='title'`:                          1,
		`SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778 AND field='title'`:                1,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
	if got := ipTextQueryInt(t, w, `SELECT locked FROM metadata_owner_fields WHERE entity_id=778 AND field='title'`); got != 1 {
		t.Fatal("owner edit did not lock", got)
	}

	// Source, projection and automatic observation move after acceptance; the
	// original request still returns its exact saved result with no new effects.
	ipTextExec(t, w, `UPDATE catalog_entities SET title='Later',sort_key='later',overview='later',revision=99 WHERE id=778`)
	ipTextExec(t, w, `UPDATE metadata_owner_fields SET automatic_value='Provider',automatic_present=1 WHERE entity_id=778 AND field='title'`)
	turn, tx, b = ipTextEditTurn(t, w, r)
	out, err = applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !out.Outcome.Replayed || out.Digest != original.Digest || !ipTextEditResultEqual(out.Value, original.Value) {
		t.Fatal("source change altered original replay", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_receipts`); got != 1 {
		t.Fatal("replay wrote receipts", got)
	}

	// At or below ACK is Retired even while the physical receipt remains.
	ipTextExec(t, w, `UPDATE metadata_edit_owners SET acknowledged_command_sequence=1 WHERE entity_id=778`)
	turn, tx, b = ipTextEditTurn(t, w, r)
	out, err = applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != ResultRetired || !ipTextEditResultZero(out.Value) || out.Digest != (ResultDigest{}) {
		t.Fatal("retired result served", out, err)
	}
	ipTextRollback(t, turn, tx)
}

func TestIPTextEditAbsentEmptyLockedEmpty(t *testing.T) {
	base := ipTextEditItemRequest(1)
	description := func(before fieldmeta.TextState) ipTextEditRequest {
		q := base
		q.Source.Projection = ipTextProjection{}
		q.Fields = []ipTextEditField{{Field: fieldmeta.Description, Before: before,
			Edit: fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "New"}}}
		return q
	}
	cases := []struct {
		name    string
		seed    []string
		before  fieldmeta.TextState
		changed bool
	}{
		{"absent", nil, fieldmeta.TextState{}, true},
		{"accepted_empty", []string{`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES('item',778,'description','','',1,0,0,'prior','prior')`},
			fieldmeta.TextState{Effective: fieldmeta.Text{Value: "", Present: true}}, true},
		{"locked_empty", []string{`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES('item',778,'description','','',1,0,1,'prior','prior')`},
			fieldmeta.TextState{Effective: fieldmeta.Text{Value: "", Present: true}, Locked: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipTextEditFixture(t)
			for _, sql := range tc.seed {
				ipTextExec(t, w, sql)
			}
			q := description(tc.before)
			cmd := ipTextEditNewCommand(t, 1, 2, q)
			turn, tx, b := ipTextEditTurn(t, w, r)
			out, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd)
			if err != nil || out.Outcome.State != Applied || out.Value.Fields[0].Change.Changed != tc.changed {
				t.Fatal(out, err)
			}
			ipTextCommit(t, turn, tx)
			if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778 AND field='description' AND value='New' AND value_present=1 AND locked=1`); got != 1 {
				t.Fatal("edited state missing", got)
			}
			if got := ipTextQueryInt(t, w, `SELECT count(*) FROM catalog_entities WHERE id=778 AND overview='New'`); got != 1 {
				t.Fatal("catalog projection missing", got)
			}
		})
	}

	// A mismatched before cohort is a conflict, never an overwrite.
	w, r, p := ipTextEditFixture(t)
	ipTextExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES('item',778,'description','Other','Other',1,1,0,'prior','prior')`)
	cmd := ipTextEditNewCommand(t, 1, 2, description(fieldmeta.TextState{}))
	turn, tx, b := ipTextEditTurn(t, w, r)
	out, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd)
	if !ipTextEditOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("mismatched before admitted", out, err)
	}
	ipTextRollback(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_edit_receipts`:                           0,
		`SELECT count(*) FROM metadata_owner_history_fields`:                    0,
		`SELECT count(*) FROM catalog_entities WHERE id=778 AND overview='New'`: 0,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
}

func TestIPTextEditActualChangedFieldsAndHistory(t *testing.T) {
	w, r, p := ipTextEditFixture(t)
	q := ipTextEditItemRequest(1)
	// SortTitle is an explicit locked-empty no-op; Tagline is the one real change.
	ipTextExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES('item',778,'sortTitle','','',1,0,1,'prior','prior')`)
	q.Fields = []ipTextEditField{
		{Field: fieldmeta.SortTitle, Before: fieldmeta.TextState{Effective: fieldmeta.Text{Value: "", Present: true}, Locked: true},
			Edit: fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: ""}},
		{Field: fieldmeta.Tagline, Before: fieldmeta.TextState{},
			Edit: fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "A line"}},
	}
	cmd := ipTextEditNewCommand(t, 1, 2, q)
	turn, tx, b := ipTextEditTurn(t, w, r)
	out, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	if out.Value.Fields[0].Change.Changed || out.Value.Fields[1].Change.Changed == false {
		t.Fatal("changed flags", out.Value.Fields)
	}
	ipTextCommit(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_owner_history_fields`); got != 1 {
		t.Fatal("history must name only actual changes", got)
	}
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_owner_history_fields WHERE field='tagline' AND after_value='A line' AND before_value_present=0`); got != 1 {
		t.Fatal("history before/after", got)
	}
	if got := ipTextQueryInt(t, w, `SELECT revision FROM metadata_edit_owners WHERE entity_id=778`); got != 2 {
		t.Fatal("owner revision", got)
	}

	// A complete no-op still saves an exact receipt but changes no revision,
	// subject byte or history. The request must carry the current before state.
	ipTextExec(t, w, `UPDATE metadata_edit_owners SET acknowledged_command_sequence=last_command_sequence WHERE entity_id=778`)
	q = ipTextEditItemRequest(2)
	q.Source.OwnerRevision, q.Source.SubjectRevision, q.Source.MetadataRevision = 2, 2, 2
	q.Source.Projection = ipTextProjection{}
	q.Fields = []ipTextEditField{{Field: fieldmeta.Tagline, Before: fieldmeta.TextState{Effective: fieldmeta.Text{Value: "A line", Present: true}, Locked: true},
		Edit: fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "A line"}}}
	noop := ipTextEditNewCommand(t, 2, 3, q)
	turn, tx, b = ipTextEditTurn(t, w, r)
	out, err = applyIPTextEdit(t.Context(), turn, p, t.Context(), b, noop)
	if err != nil || out.Value.Fields[0].Change.Changed {
		t.Fatal("re-setting a locked equal value must not be a change", out, err)
	}
	ipTextCommit(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT revision FROM metadata_edit_owners WHERE entity_id=778`); got != 2 {
		t.Fatal("no-op advanced revision", got)
	}
}

func TestIPTextEditOwnerActorCurrentCAS(t *testing.T) {
	w, r, p := ipTextEditFixture(t)
	cmd := ipTextEditNewCommand(t, 1, 2, ipTextEditItemRequest(1))
	turn, tx, b := ipTextEditTurn(t, w, r)
	if _, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd); err != nil {
		t.Fatal(err)
	}
	ipTextCommit(t, turn, tx)
	ipTextExec(t, w, `UPDATE metadata_edit_owners SET acknowledged_command_sequence=last_command_sequence WHERE entity_id=778`)

	// The same sequence with the SAME incarnation but a stale owner revision is
	// a conflict, not an implicit allocation.
	stale := ipTextEditItemRequest(2)
	stale.Source.OwnerRevision = 1
	stale.Source.SubjectRevision, stale.Source.MetadataRevision = 2, 2
	stale.Source.Projection = ipTextEditProjection("The Voyage")
	stale.Fields[0].Before = fieldmeta.TextState{Effective: fieldmeta.Text{Value: "The Voyage", Present: true}, Locked: true}
	stale.Fields[0].Edit.Value = "Stale"
	cmd = ipTextEditNewCommand(t, 2, 3, stale)
	turn, tx, b = ipTextEditTurn(t, w, r)
	if out, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd); !ipTextEditOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("stale owner revision admitted", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778 AND field='title' AND actor='Owner'`); got != 1 {
		t.Fatal("owner actor", got)
	}
	// A sequence gap is a conflict.
	gap := ipTextEditItemRequest(3)
	gap.Source.OwnerRevision = 2
	gap.Source.SubjectRevision, gap.Source.MetadataRevision = 2, 2
	gap.Source.Projection = ipTextEditProjection("The Voyage")
	gap.Fields[0].Before = fieldmeta.TextState{Effective: fieldmeta.Text{Value: "The Voyage", Present: true}, Locked: true}
	gap.Fields[0].Edit.Value = "Gap"
	cmd = ipTextEditNewCommand(t, 3, 4, gap)
	turn, tx, b = ipTextEditTurn(t, w, r)
	if out, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd); !ipTextEditOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("sequence gap admitted", out, err)
	}
	ipTextRollback(t, turn, tx)
}

func TestIPTextEditHistoryOutlivesAutomaticObservation(t *testing.T) {
	w, r, p := ipTextEditFixture(t)
	cmd := ipTextEditNewCommand(t, 1, 2, ipTextEditItemRequest(1))
	turn, tx, b := ipTextEditTurn(t, w, r)
	if _, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd); err != nil {
		t.Fatal(err)
	}
	ipTextCommit(t, turn, tx)
	var before, after string
	if err := w.db.QueryRowContext(t.Context(), `SELECT before_value,after_value FROM metadata_owner_history_fields WHERE field='title'`).Scan(&before, &after); err != nil {
		t.Fatal(err)
	}
	// Automatic observation advances its own source and lock-free value without
	// rewriting the retained owner history.
	ipTextExec(t, w, `UPDATE metadata_owner_fields SET automatic_value='Provider',automatic_present=1,actor='provider',observed_at='later' WHERE entity_id=778 AND field='title'`)
	var before2, after2 string
	if err := w.db.QueryRowContext(t.Context(), `SELECT before_value,after_value FROM metadata_owner_history_fields WHERE field='title'`).Scan(&before2, &after2); err != nil {
		t.Fatal(err)
	}
	if before != before2 || after != after2 {
		t.Fatal("automatic observation rewrote owner history", before, before2, after, after2)
	}
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778 AND field='title' AND value='The Voyage' AND locked=1`); got != 1 {
		t.Fatal("automatic observation displaced the locked owner value", got)
	}
}

func TestIPTextEditBookAuthorKeyAtomic(t *testing.T) {
	w, r, p := ipTextEditFixture(t)
	book := ipTextEditRequest{Kind: IPFieldEdit, Source: ipTextEditSource{
		EntityID: 779, Kind: "book", TargetKind: 8, LibraryID: 777, LibraryKey: "ip-library",
		Actor: "Owner", OwnerRevision: 1, SubjectRevision: 1, MetadataRevision: 1,
		SourceRevision: 1, IdentityRevision: 1, MetadataIncarnation: [16]byte{4},
	}, Fields: []ipTextEditField{{Field: ipTextAuthorField, Before: fieldmeta.TextState{},
		Edit: fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "Émile Zola"}}}}
	cmd := ipTextEditNewCommand(t, 1, 2, book)
	turn, tx, b := ipTextEditTurn(t, w, r)
	out, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM catalog_entities WHERE id=779 AND author='Émile Zola' AND reading_author_value='Émile Zola' AND reading_author_key='author:émile zola' AND reading_author_source='owner'`); got != 1 {
		t.Fatal("author value and key did not move together", got)
	}
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_owner_fields WHERE kind='book' AND entity_id=779 AND field='author' AND value='Émile Zola' AND locked=1`); got != 1 {
		t.Fatal("author owner field", got)
	}
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_owner_history_fields WHERE field='author' AND after_value='Émile Zola' AND after_value_present=1`); got != 1 {
		t.Fatal("author history", got)
	}
	// A second edit must recompute the key, never retain the first one.
	ipTextExec(t, w, `UPDATE metadata_edit_owners SET acknowledged_command_sequence=last_command_sequence WHERE entity_id=779`)
	book.Source.OwnerRevision, book.Source.SubjectRevision, book.Source.MetadataRevision = 2, 2, 2
	book.Fields[0].Before = fieldmeta.TextState{Effective: fieldmeta.Text{Value: "Émile Zola", Present: true}, Locked: true}
	book.Fields[0].Edit.Value = "Victor Hugo"
	cmd = ipTextEditNewCommand(t, 2, 3, book)
	turn, tx, b = ipTextEditTurn(t, w, r)
	if out, err = applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd); err != nil {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM catalog_entities WHERE id=779 AND author='Victor Hugo' AND reading_author_key='author:victor hugo'`); got != 1 {
		t.Fatal("stale author key retained", got)
	}
}

func TestIPTextEditPersonBiography(t *testing.T) {
	w, r, p := ipTextEditFixture(t)
	q := ipTextEditRequest{Kind: IPFieldEdit, Source: ipTextEditSource{
		PersonID: 778, Kind: "person", PersonToken: "person-token", Actor: "Owner",
		OwnerRevision: 1, SubjectRevision: 1,
	}, Fields: []ipTextEditField{{Field: fieldmeta.Description, Before: fieldmeta.TextState{},
		Edit: fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "A life"}}}}
	cmd := ipTextEditNewCommand(t, 1, 2, q)
	turn, tx, b := ipTextEditTurn(t, w, r)
	out, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied || out.Value.PersonID != 778 {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM catalog_people WHERE id=778 AND biography='A life' AND revision=2`:                   1,
		`SELECT count(*) FROM metadata_owner_fields WHERE kind='person' AND entity_id=778 AND field='description'`: 1,
		`SELECT count(*) FROM metadata_edit_owners WHERE person_id=778 AND entity_id IS NULL`:                      1,
		`SELECT count(*) FROM metadata_owner_history_fields WHERE field='description' AND after_value='A life'`:    1,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
}

func TestIPTextEditStaleFreshSubject(t *testing.T) {
	w, r, p := ipTextEditFixture(t)
	cmd := ipTextEditNewCommand(t, 1, 2, ipTextEditItemRequest(1))
	ipTextExec(t, w, `UPDATE catalog_entities SET metadata_revision=9 WHERE id=778`)
	turn, tx, b := ipTextEditTurn(t, w, r)
	out, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd)
	if !ipTextEditOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("stale fresh subject admitted", out, err)
	}
	ipTextRollback(t, turn, tx)
	if got := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_receipts`); got != 0 {
		t.Fatal("stale subject wrote a receipt", got)
	}
}

func TestIPTextEditCurrentAuthority(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := ipTextEditFixture(t)
			cmd := ipTextEditNewCommand(t, 1, 2, ipTextEditItemRequest(1))
			turn, tx, b := ipTextEditTurn(t, w, r)
			if out, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd); err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			}
			ipTextCommit(t, turn, tx)
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b = ipTextEditTurn(t, w, r)
			out, err := applyIPTextEdit(t.Context(), turn, proof, t.Context(), b, cmd)
			if !ipTextEditOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale authority served the saved edit", out, err)
			}
			ipTextRollback(t, turn, tx)
		})
	}
}

func TestIPTextEditConflictRollbackAndBounds(t *testing.T) {
	w, r, p := ipTextEditFixture(t)
	cmd := ipTextEditNewCommand(t, 1, 2, ipTextEditItemRequest(1))
	if _, err := w.db.ExecContext(t.Context(), `CREATE TRIGGER ip_text_fail BEFORE INSERT ON metadata_edit_receipts BEGIN SELECT RAISE(ABORT,'receipt');END`); err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipTextEditTurn(t, w, r)
	out, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd)
	if err == nil || !ipTextEditOutputZero(out) {
		t.Fatal("failed bundle returned public output", out, err)
	}
	ipTextRollback(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM catalog_entities WHERE id=778 AND title='The Voyage'`: 0,
		`SELECT count(*) FROM metadata_edit_owners`:                                 0,
		`SELECT count(*) FROM metadata_owner_history_fields`:                        0,
		`SELECT count(*) FROM metadata_edit_receipts`:                               0,
	} {
		if got := ipTextQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}

	// The same retained sequence with a different original request digest is a
	// conflict, never a second effect under the first result.
	w2, r2, p2 := ipTextEditFixture(t)
	first := ipTextEditNewCommand(t, 1, 2, ipTextEditItemRequest(1))
	turn2, tx2, b2 := ipTextEditTurn(t, w2, r2)
	if _, err = applyIPTextEdit(t.Context(), turn2, p2, t.Context(), b2, first); err != nil {
		t.Fatal(err)
	}
	ipTextCommit(t, turn2, tx2)
	conflicting := ipTextEditItemRequest(1)
	conflicting.Fields[0].Edit.Value = "Other"
	cmd2 := ipTextEditNewCommand(t, 1, 2, conflicting)
	turn2, tx2, b2 = ipTextEditTurn(t, w2, r2)
	if out, err = applyIPTextEdit(t.Context(), turn2, p2, t.Context(), b2, cmd2); !ipTextEditOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting request admitted", out, err)
	}
	ipTextRollback(t, turn2, tx2)
	if got := ipTextQueryInt(t, w2, `SELECT count(*) FROM catalog_entities WHERE id=778 AND title='The Voyage'`); got != 1 {
		t.Fatal("conflict changed the accepted title", got)
	}

	for name, mutate := range map[string]func(*ipTextEditRequest){
		"empty_actor":        func(q *ipTextEditRequest) { q.Source.Actor = "" },
		"no_fields":          func(q *ipTextEditRequest) { q.Fields = nil },
		"unknown_field":      func(q *ipTextEditRequest) { q.Fields[0].Field = "rating" },
		"edition_rejected":   func(q *ipTextEditRequest) { q.Fields[0].Field = fieldmeta.Edition },
		"empty_entity":       func(q *ipTextEditRequest) { q.Source.EntityID = 0 },
		"kind_mismatch":      func(q *ipTextEditRequest) { q.Source.Kind = "book" },
		"missing_source_rev": func(q *ipTextEditRequest) { q.Source.SourceRevision = 0 },
		"zero_owner_rev":     func(q *ipTextEditRequest) { q.Source.OwnerRevision = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			q := ipTextEditItemRequest(1)
			mutate(&q)
			if c, err := newIPTextEditCommand(ipTextEditIdentity(1, 2), q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
	// Bounded maximum title (300 runes) is accepted; 301 is refused.
	long := strings.Repeat("é", 300)
	q := ipTextEditItemRequest(1)
	q.Fields[0].Edit.Value = long
	if _, err := newIPTextEditCommand(ipTextEditIdentity(1, 2), q); err != nil {
		t.Fatal("bounded maximum refused", err)
	}
	q.Fields[0].Edit.Value = long + "é"
	if c, err := newIPTextEditCommand(ipTextEditIdentity(1, 2), q); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize title admitted", c, err)
	}
}

func TestIPTextEditUnknownCommit(t *testing.T) {
	w, r, p := ipTextEditFixture(t)
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	cmd := ipTextEditNewCommand(t, 1, 2, ipTextEditItemRequest(1))
	turn, tx, b := ipTextEditTurn(t, w, r)
	out, err := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if out, e := applyIPTextEdit(t.Context(), turn, p, t.Context(), b, cmd); !ipTextEditOutputZero(out) || e == nil {
		t.Fatal("uncertain frame reserved the saved edit", out, e)
	}
	// Durable outcome is read through a FRESH read-only connection, never the
	// quarantined writer pool.
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_edit_receipts`:                               1,
		`SELECT count(*) FROM metadata_edit_receipt_fields`:                         1,
		`SELECT count(*) FROM metadata_owner_history_fields`:                        1,
		`SELECT count(*) FROM catalog_entities WHERE id=778 AND title='The Voyage'`: 1,
	} {
		if got := ipPolicyFreshInt(t, path, query); got != want {
			t.Fatal(query, got)
		}
	}
}

func TestIPTextEditDescriptors(t *testing.T) {
	for _, expected := range []Descriptor{
		{ID: IPFieldEdit, Name: "ip_field_edit", Durability: Normal},
		{ID: IPFieldLock, Name: "ip_field_lock", Durability: Normal},
		{ID: IPFieldRestore, Name: "ip_field_restore", Durability: Normal},
	} {
		actual, err := Describe(expected.ID)
		if err != nil || actual != expected || actual.ID < 240 || actual.ID > 242 {
			t.Fatal(actual, err)
		}
	}
}
