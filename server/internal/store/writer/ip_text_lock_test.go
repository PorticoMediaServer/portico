package writer

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func ipTextLockFixture(t *testing.T) (*executor, *ipEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, p := ipEntrySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ip-text-lock-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT INTO libraries(id,name,kind,root,preference_present,preference_revision,preference_group_collections,preference_local_tags,preference_lyrics_source,preference_title_source,preference_episode_numbering,preference_find_extras,preference_sort_ignore_articles) VALUES('ip-library','IP','tv','/media',1,1,0,'off','files','tags','absolute',0,1)`,
		`INSERT OR IGNORE INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(2,'show',1,0,1,1,0),(8,'book',1,0,1,1,0)`,
	} {
		wc008NativeExec(t, w, sql)
	}
	itemToken, bookToken, showToken, inc := [16]byte{8}, [16]byte{9}, [16]byte{10}, [16]byte{4}
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(778,?,777,1),(779,?,777,8),(780,?,777,2)`, itemToken[:], bookToken[:], showToken[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision) VALUES(778,?,777,1,'Title','title','t','title','t',0,?,?,1,1,1)`, itemToken[:], inc[:], inc[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision) VALUES(779,?,777,8,'Book','book','b','book','b',0,?,?,1,1,1)`, bookToken[:], inc[:], inc[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision) VALUES(780,?,777,2,'Show','show','s','show','s',0,?,?,1,1,1)`, showToken[:], inc[:], inc[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(778,'person-token','person:key','Person','person',?)`, inc[:])
	r, err := stmttest.New(t, ipTextLockDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipEntryRegistry{r, keeper}, p
}

func ipTextLockTurn(t *testing.T, w *executor, r *ipEntryRegistry, kind CommandID) (*commandTurn, *transaction, ipEntryBudget) {
	t.Helper()
	turn, b := ipEntryTurn(t, w, r, ipEntryRequest{purpose: kind})
	return turn, turn.transaction, b
}

func ipTextLockItemRequest(kind CommandID) ipTextLockRequest {
	return ipTextLockRequest{Kind: kind, Source: ipTextEditSource{
		EntityID: 778, Kind: "item", TargetKind: 1, LibraryID: 777, LibraryKey: "ip-library",
		Actor: "Owner", OwnerRevision: 1, SubjectRevision: 1, MetadataRevision: 1,
		SourceRevision: 1, IdentityRevision: 1, MetadataIncarnation: [16]byte{4},
	}, Field: fieldmeta.Title, Before: fieldmeta.TextState{Effective: fieldmeta.Text{Value: "Title", Present: true}}}
}

func ipTextLockNewCommand(t *testing.T, sequence int64, operation byte, request ipTextLockRequest) *ipTextLockCommand {
	t.Helper()
	cmd, err := newIPTextFieldLockCommand(ipTextEditIdentity(sequence, operation), request)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func ipTextLockResultZero(v ipTextLockValue) bool {
	return v.OwnerID == 0 && v.EntityID == 0 && v.PersonID == 0 && v.Revision == 0 && v.SubjectRevision == 0 &&
		v.AppliedMS == 0 && !v.History.Present && v.Field == "" && v.Change == (fieldmeta.TextChange{})
}

func ipTextLockOutputZero(v ipTextLockCommandResult) bool {
	return v.Outcome.State == 0 && !v.Outcome.Replayed && v.Outcome.Evidence == nil &&
		v.Outcome.Reference == (ReceiptReference{}) && ipTextLockResultZero(v.Value) && v.Digest == (ResultDigest{})
}

func ipTextLockAck(t *testing.T, w *executor, target int64) {
	t.Helper()
	ipTextExec(t, w, `UPDATE metadata_edit_owners SET acknowledged_command_sequence=last_command_sequence WHERE entity_id=?`, target)
}

func TestIPTextFieldLockFreshReplayRetired(t *testing.T) {
	w, r, p := ipTextLockFixture(t)
	cmd := ipTextLockNewCommand(t, 1, 2, ipTextLockItemRequest(IPFieldLock))
	turn, tx, b := ipTextLockTurn(t, w, r, IPFieldLock)
	out, err := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.Revision != 2 ||
		out.Value.SubjectRevision != 1 || out.Value.AppliedMS != w.now().UnixMilli() || out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	got := out.Value.Change
	if !got.State.Locked || !got.Changed || got.EffectiveChanged || !got.State.Effective.Present ||
		got.State.Effective.Value != "Title" || got.State.Automatic.Present {
		t.Fatal("lock change", got)
	}
	ipTextCommit(t, turn, tx)
	original := out
	for query, want := range map[string]int64{
		`SELECT revision FROM catalog_entities WHERE id=778`:          1,
		`SELECT metadata_revision FROM catalog_entities WHERE id=778`: 1,
		`SELECT count(*) FROM metadata_owner_fields WHERE kind='item' AND entity_id=778 AND field='title' AND value='Title' AND automatic_value='' AND value_present=1 AND automatic_present=0 AND locked=1`: 1,
		`SELECT count(*) FROM metadata_edit_owners WHERE entity_id=778`:                                                                                    1,
		`SELECT revision FROM metadata_edit_owners WHERE entity_id=778`:                                                                                    2,
		`SELECT last_command_sequence FROM metadata_edit_owners WHERE entity_id=778`:                                                                       1,
		`SELECT count(*) FROM metadata_edit_receipts WHERE command_kind='field_lock' AND field='title' AND locked=1 AND changed=1 AND effective_changed=0`: 1,
		`SELECT count(*) FROM metadata_owner_history_fields WHERE field='title' AND before_locked=0 AND after_locked=1`:                                    1,
	} {
		if value := ipTextQueryInt(t, w, query); value != want {
			t.Fatal(query, value, want)
		}
	}

	// Source, projection and automatic observation move after acceptance; the
	// original request still returns its exact saved result with no new effects.
	ipTextExec(t, w, `UPDATE catalog_entities SET title='Later',sort_key='later',revision=99 WHERE id=778`)
	ipTextExec(t, w, `UPDATE metadata_owner_fields SET automatic_value='Provider',automatic_present=1 WHERE entity_id=778 AND field='title'`)
	turn, tx, b = ipTextLockTurn(t, w, r, IPFieldLock)
	out, err = applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !out.Outcome.Replayed || out.Digest != original.Digest || !ipTextLockResultEqual(out.Value, original.Value) {
		t.Fatal("source change altered original replay", out, err)
	}
	ipTextRollback(t, turn, tx)
	if value := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_edit_receipts`); value != 1 {
		t.Fatal("replay wrote receipts", value)
	}

	// At or below ACK is Retired even while the physical receipt remains.
	ipTextLockAck(t, w, 778)
	turn, tx, b = ipTextLockTurn(t, w, r, IPFieldLock)
	out, err = applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != ResultRetired || !ipTextLockResultZero(out.Value) || out.Digest != (ResultDigest{}) {
		t.Fatal("retired result served", out, err)
	}
	ipTextRollback(t, turn, tx)
}

func TestIPTextFieldRestoreInheritance(t *testing.T) {
	w, r, p := ipTextLockFixture(t)
	ipTextExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES('item',778,'description','Owner line','Provider line',1,1,1,'prior','prior')`)
	ipTextExec(t, w, `UPDATE catalog_entities SET overview='Owner line' WHERE id=778`)
	request := ipTextLockItemRequest(IPFieldRestore)
	request.Field = fieldmeta.Description
	request.Before = fieldmeta.TextState{Effective: fieldmeta.Text{Value: "Owner line", Present: true},
		Automatic: fieldmeta.Text{Value: "Provider line", Present: true}, Locked: true}
	cmd := ipTextLockNewCommand(t, 1, 2, request)
	turn, tx, b := ipTextLockTurn(t, w, r, IPFieldRestore)
	out, err := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied || out.Value.Revision != 2 || out.Value.SubjectRevision != 2 {
		t.Fatal(out, err)
	}
	if change := out.Value.Change; !change.Changed || !change.EffectiveChanged || change.State.Locked ||
		change.State.Effective.Value != "Provider line" || change.State.Automatic.Value != "Provider line" {
		t.Fatal("restore change", change)
	}
	ipTextCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778 AND field='description' AND value='Provider line' AND automatic_value='Provider line' AND value_present=1 AND automatic_present=1 AND locked=0`: 1,
		`SELECT count(*) FROM catalog_entities WHERE id=778 AND overview='Provider line' AND revision=2 AND metadata_revision=2`:                                                                                        1,
		`SELECT count(*) FROM metadata_owner_history_fields WHERE field='description' AND before_value='Owner line' AND before_locked=1 AND after_value='Provider line' AND after_locked=0`:                             1,
		`SELECT count(*) FROM metadata_edit_receipts WHERE command_kind='field_restore' AND locked=0 AND changed=1 AND effective_changed=1 AND value='Provider line'`:                                                   1,
	} {
		if value := ipTextQueryInt(t, w, query); value != want {
			t.Fatal(query, value, want)
		}
	}

	// Restoring a locked value whose retained automatic source is absent clears
	// the effective value to absence, not to an inferred empty string.
	ipTextLockAck(t, w, 778)
	ipTextExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES('item',778,'tagline','Tagline value','',1,0,1,'prior','prior')`)
	ipTextExec(t, w, `UPDATE catalog_entities SET tagline='Tagline value' WHERE id=778`)
	request = ipTextLockItemRequest(IPFieldRestore)
	request.Field = fieldmeta.Tagline
	request.Source.OwnerRevision, request.Source.SubjectRevision, request.Source.MetadataRevision = 2, 2, 2
	request.Before = fieldmeta.TextState{Effective: fieldmeta.Text{Value: "Tagline value", Present: true}, Locked: true}
	cmd = ipTextLockNewCommand(t, 2, 3, request)
	turn, tx, b = ipTextLockTurn(t, w, r, IPFieldRestore)
	out, err = applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	if change := out.Value.Change; change.State.Effective.Present || change.State.Effective.Value != "" || change.State.Locked {
		t.Fatal("restore to absent", change)
	}
	ipTextCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778 AND field='tagline' AND value='' AND value_present=0 AND automatic_present=0 AND locked=0`: 1,
		`SELECT count(*) FROM catalog_entities WHERE id=778 AND tagline='' AND revision=3 AND metadata_revision=3`:                                                 1,
	} {
		if value := ipTextQueryInt(t, w, query); value != want {
			t.Fatal(query, value, want)
		}
	}
}

func TestIPTextFieldLockedEmptyClear(t *testing.T) {
	w, r, p := ipTextLockFixture(t)
	ipTextExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES('item',778,'description','','Provider line',1,1,0,'prior','prior')`)
	request := ipTextLockItemRequest(IPFieldLock)
	request.Field = fieldmeta.Description
	request.Before = fieldmeta.TextState{Effective: fieldmeta.Text{Value: "", Present: true},
		Automatic: fieldmeta.Text{Value: "Provider line", Present: true}}
	cmd := ipTextLockNewCommand(t, 1, 2, request)
	turn, tx, b := ipTextLockTurn(t, w, r, IPFieldLock)
	out, err := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	if change := out.Value.Change; !change.State.Locked || !change.State.Effective.Present || change.State.Effective.Value != "" ||
		!change.State.Automatic.Present || change.State.Automatic.Value != "Provider line" || change.EffectiveChanged {
		t.Fatal("locked-empty clear", change)
	}
	ipTextCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778 AND field='description' AND value='' AND value_present=1 AND automatic_value='Provider line' AND automatic_present=1 AND locked=1`: 1,
		`SELECT count(*) FROM catalog_entities WHERE id=778 AND overview='' AND revision=1`:                                                                                                                1,
	} {
		if value := ipTextQueryInt(t, w, query); value != want {
			t.Fatal(query, value, want)
		}
	}

	// A lock observed against a stale automatic source is a conflict, never an
	// overwrite of the newer observation.
	ipTextLockAck(t, w, 778)
	ipTextExec(t, w, `UPDATE metadata_owner_fields SET automatic_value='Changed auto',automatic_present=1 WHERE entity_id=778 AND field='description'`)
	request.Source.OwnerRevision = 2
	cmd = ipTextLockNewCommand(t, 2, 3, request)
	turn, tx, b = ipTextLockTurn(t, w, r, IPFieldLock)
	if out, err = applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd); !ipTextLockOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("stale automatic observation admitted", out, err)
	}
	ipTextRollback(t, turn, tx)
	if value := ipTextQueryInt(t, w, `SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778 AND field='description' AND automatic_value='Changed auto'`); value != 1 {
		t.Fatal("conflict overwrote the observation", value)
	}
}

func TestIPTextFieldRestoreKeyProjection(t *testing.T) {
	w, r, p := ipTextLockFixture(t)
	before, err := ProjectFilenameMetadata("Owner Title", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ipTextExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES('item',778,'title','Owner Title','Auto Title',1,1,1,'prior','prior')`)
	ipTextExec(t, w, `UPDATE catalog_entities SET title=?,sort_title=?,metadata_language=?,sort_key=?,sort_head=?,sort_key_keep_articles=?,sort_head_keep_articles=? WHERE id=778`,
		before.Title, before.SortTitle, before.MetadataLanguage, before.SortKey, before.SortHead, before.SortKeyKeepArticles, before.SortHeadKeepArticles)
	request := ipTextLockItemRequest(IPFieldRestore)
	request.Before = fieldmeta.TextState{Effective: fieldmeta.Text{Value: "Owner Title", Present: true},
		Automatic: fieldmeta.Text{Value: "Auto Title", Present: true}, Locked: true}
	cmd := ipTextLockNewCommand(t, 1, 2, request)
	turn, tx, b := ipTextLockTurn(t, w, r, IPFieldRestore)
	out, err := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	next, err := ProjectFilenameMetadata("Auto Title", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string]string{
		`SELECT title FROM catalog_entities WHERE id=778`:                   next.Title,
		`SELECT sort_key FROM catalog_entities WHERE id=778`:                next.SortKey,
		`SELECT sort_head FROM catalog_entities WHERE id=778`:               next.SortHead,
		`SELECT sort_key_keep_articles FROM catalog_entities WHERE id=778`:  next.SortKeyKeepArticles,
		`SELECT sort_head_keep_articles FROM catalog_entities WHERE id=778`: next.SortHeadKeepArticles,
	} {
		var value string
		if err = w.db.QueryRowContext(t.Context(), query).Scan(&value); err != nil {
			t.Fatal(query, err)
		}
		if value != want {
			t.Fatal(query, value, want)
		}
	}
}

func TestIPTextFieldLockPersonBiography(t *testing.T) {
	w, r, p := ipTextLockFixture(t)
	ipTextExec(t, w, `UPDATE catalog_people SET biography='A life' WHERE id=778`)
	request := ipTextLockRequest{Kind: IPFieldLock, Source: ipTextEditSource{
		PersonID: 778, Kind: "person", PersonToken: "person-token", Actor: "Owner",
		OwnerRevision: 1, SubjectRevision: 1,
	}, Field: fieldmeta.Description, Before: fieldmeta.TextState{Effective: fieldmeta.Text{Value: "A life", Present: true}}}
	cmd := ipTextLockNewCommand(t, 1, 2, request)
	turn, tx, b := ipTextLockTurn(t, w, r, IPFieldLock)
	out, err := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied || out.Value.PersonID != 778 || out.Value.SubjectRevision != 1 {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_edit_owners WHERE person_id=778 AND entity_id IS NULL AND kind='person'`:                 1,
		`SELECT count(*) FROM catalog_people WHERE id=778 AND biography='A life' AND revision=1`:                                1,
		`SELECT count(*) FROM metadata_owner_fields WHERE kind='person' AND entity_id=778 AND field='description' AND locked=1`: 1,
	} {
		if value := ipTextQueryInt(t, w, query); value != want {
			t.Fatal(query, value, want)
		}
	}
}

func TestIPTextFieldLockConflictRollbackBounds(t *testing.T) {
	w, r, p := ipTextLockFixture(t)
	cmd := ipTextLockNewCommand(t, 1, 2, ipTextLockItemRequest(IPFieldLock))
	if _, err := w.db.ExecContext(t.Context(), `CREATE TRIGGER ip_lock_fail BEFORE INSERT ON metadata_edit_receipts BEGIN SELECT RAISE(ABORT,'receipt');END`); err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipTextLockTurn(t, w, r, IPFieldLock)
	out, err := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err == nil || !ipTextLockOutputZero(out) {
		t.Fatal("failed lock returned public output", out, err)
	}
	ipTextRollback(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_owner_fields`:  0,
		`SELECT count(*) FROM metadata_edit_owners`:   0,
		`SELECT count(*) FROM metadata_owner_history`: 0,
		`SELECT count(*) FROM metadata_edit_receipts`: 0,
	} {
		if value := ipTextQueryInt(t, w, query); value != want {
			t.Fatal(query, value, want)
		}
	}

	// A mismatched before cohort is a conflict, never an overwrite.
	w2, r2, p2 := ipTextLockFixture(t)
	ipTextExec(t, w2, `UPDATE catalog_entities SET title='Other' WHERE id=778`)
	turn2, tx2, b2 := ipTextLockTurn(t, w2, r2, IPFieldLock)
	if out, err = applyIPTextFieldLock(t.Context(), turn2, p2, t.Context(), b2, cmd); !ipTextLockOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("mismatched before admitted", out, err)
	}
	ipTextRollback(t, turn2, tx2)

	// The same retained sequence with a different original request digest is a
	// conflict, never a second effect under the first result.
	w3, r3, p3 := ipTextLockFixture(t)
	first := ipTextLockNewCommand(t, 1, 2, ipTextLockItemRequest(IPFieldLock))
	turn3, tx3, b3 := ipTextLockTurn(t, w3, r3, IPFieldLock)
	if _, err = applyIPTextFieldLock(t.Context(), turn3, p3, t.Context(), b3, first); err != nil {
		t.Fatal(err)
	}
	ipTextCommit(t, turn3, tx3)
	ipTextLockAck(t, w3, 778)
	conflicting := ipTextLockItemRequest(IPFieldLock)
	conflicting.Field = fieldmeta.Tagline
	conflicting.Before = fieldmeta.TextState{Effective: fieldmeta.Text{Value: "Line", Present: true}}
	cmd3 := ipTextLockNewCommand(t, 1, 2, conflicting)
	turn3, tx3, b3 = ipTextLockTurn(t, w3, r3, IPFieldLock)
	if out, err = applyIPTextFieldLock(t.Context(), turn3, p3, t.Context(), b3, cmd3); !ipTextLockOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting request admitted", out, err)
	}
	ipTextRollback(t, turn3, tx3)

	// A sequence gap and a stale owner revision are conflicts.
	ipTextExec(t, w3, `UPDATE catalog_entities SET title='Later' WHERE id=778`)
	gap := ipTextLockItemRequest(IPFieldLock)
	gap.Before = fieldmeta.TextState{Effective: fieldmeta.Text{Value: "Later", Present: true}, Automatic: fieldmeta.Text{Value: "Provider", Present: true}, Locked: true}
	cmd4 := ipTextLockNewCommand(t, 3, 4, gap)
	turn3, tx3, b3 = ipTextLockTurn(t, w3, r3, IPFieldLock)
	if out, err = applyIPTextFieldLock(t.Context(), turn3, p3, t.Context(), b3, cmd4); !ipTextLockOutputZero(out) || !errors.Is(err, ErrConflict) {
		t.Fatal("sequence gap admitted", out, err)
	}
	ipTextRollback(t, turn3, tx3)

	for name, mutate := range map[string]func(*ipTextLockRequest){
		"empty_actor":        func(q *ipTextLockRequest) { q.Source.Actor = "" },
		"no_target":          func(q *ipTextLockRequest) { q.Source.EntityID = 0 },
		"both_namespaces":    func(q *ipTextLockRequest) { q.Source.PersonID = 778 },
		"unknown_field":      func(q *ipTextLockRequest) { q.Field = "rating" },
		"edition_rejected":   func(q *ipTextLockRequest) { q.Field = fieldmeta.Edition },
		"author_deferred":    func(q *ipTextLockRequest) { q.Source.Kind, q.Source.TargetKind, q.Field = "book", 8, ipTextAuthorField },
		"kind_mismatch":      func(q *ipTextLockRequest) { q.Source.Kind = "book" },
		"missing_source_rev": func(q *ipTextLockRequest) { q.Source.SourceRevision = 0 },
		"zero_owner_rev":     func(q *ipTextLockRequest) { q.Source.OwnerRevision = 0 },
		"zero_subject_rev":   func(q *ipTextLockRequest) { q.Source.SubjectRevision = 0 },
		"projection_input":   func(q *ipTextLockRequest) { q.Source.Projection = ipTextProjection{Title: "Title"} },
		"locked_absent":      func(q *ipTextLockRequest) { q.Before = fieldmeta.TextState{Locked: true} },
		"person_mixed":       func(q *ipTextLockRequest) { q.Source.PersonID, q.Source.Kind = 778, "person" },
		"restore_on_lock":    func(q *ipTextLockRequest) { q.Kind = IPFieldEdit },
	} {
		t.Run(name, func(t *testing.T) {
			q := ipTextLockItemRequest(IPFieldLock)
			mutate(&q)
			if cmd, err := newIPTextFieldLockCommand(ipTextEditIdentity(1, 2), q); cmd != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(cmd, err)
			}
		})
	}
	// The accepted source-text envelope is bounded, not field-truncated.
	long := strings.Repeat("é", 4<<20/2)
	q := ipTextLockItemRequest(IPFieldLock)
	q.Before.Effective.Value = long
	if _, err := newIPTextFieldLockCommand(ipTextEditIdentity(1, 2), q); err != nil {
		t.Fatal("bounded maximum source refused", err)
	}
	q.Before.Effective.Value = long + "é"
	if cmd, err := newIPTextFieldLockCommand(ipTextEditIdentity(1, 2), q); cmd != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize source admitted", cmd, err)
	}
}

func TestIPTextFieldLockCurrentAuthority(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := ipTextLockFixture(t)
			cmd := ipTextLockNewCommand(t, 1, 2, ipTextLockItemRequest(IPFieldLock))
			turn, tx, b := ipTextLockTurn(t, w, r, IPFieldLock)
			if out, err := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd); err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			}
			ipTextCommit(t, turn, tx)
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b = ipTextLockTurn(t, w, r, IPFieldLock)
			out, err := applyIPTextFieldLock(t.Context(), turn, proof, t.Context(), b, cmd)
			if !ipTextLockOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale authority served the saved lock", out, err)
			}
			ipTextRollback(t, turn, tx)
			// Current authority precedes receipt disclosure even for a conflicting
			// original request.
			conflicting := ipTextLockItemRequest(IPFieldLock)
			conflicting.Field = fieldmeta.Tagline
			conflicting.Before = fieldmeta.TextState{Effective: fieldmeta.Text{Value: "Line", Present: true}}
			cmd = ipTextLockNewCommand(t, 1, 2, conflicting)
			turn, tx, b = ipTextLockTurn(t, w, r, IPFieldLock)
			out, err = applyIPTextFieldLock(t.Context(), turn, proof, t.Context(), b, cmd)
			if !ipTextLockOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrConflict) {
				t.Fatal("stale authority disclosed the retained receipt", out, err)
			}
			ipTextRollback(t, turn, tx)
		})
	}
}

func TestIPTextFieldLockUnknownCommit(t *testing.T) {
	w, r, p := ipTextLockFixture(t)
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	cmd := ipTextLockNewCommand(t, 1, 2, ipTextLockItemRequest(IPFieldLock))
	turn, tx, b := ipTextLockTurn(t, w, r, IPFieldLock)
	out, err := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if out, e := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd); !ipTextLockOutputZero(out) || e == nil {
		t.Fatal("uncertain frame reserved the saved lock", out, e)
	}
	// Durable outcome is read through a FRESH read-only connection, never the
	// quarantined writer pool.
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778 AND field='title' AND locked=1`: 1,
		`SELECT count(*) FROM metadata_edit_receipts`:                                                   1,
		`SELECT count(*) FROM metadata_owner_history_fields`:                                            1,
		`SELECT count(*) FROM metadata_edit_owners`:                                                     1,
	} {
		if value := ipPolicyFreshInt(t, path, query); value != want {
			t.Fatal(query, value, want)
		}
	}
}

func TestIPTextFieldLockDescriptors(t *testing.T) {
	for _, expected := range []Descriptor{
		{ID: IPFieldLock, Name: "ip_field_lock", Durability: Normal},
		{ID: IPFieldRestore, Name: "ip_field_restore", Durability: Normal},
	} {
		actual, err := Describe(expected.ID)
		if err != nil || actual != expected || actual.ID < 240 || actual.ID > 242 {
			t.Fatal(actual, err)
		}
	}
}

func ipTextRestoreRequest(t *testing.T, w *executor) ipTextLockRequest {
	t.Helper()
	ipTextExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES('item',778,'description','Owner line','Provider line',1,1,1,'prior','prior')`)
	ipTextExec(t, w, `UPDATE catalog_entities SET overview='Owner line' WHERE id=778`)
	request := ipTextLockItemRequest(IPFieldRestore)
	request.Field = fieldmeta.Description
	request.Before = fieldmeta.TextState{Effective: fieldmeta.Text{Value: "Owner line", Present: true},
		Automatic: fieldmeta.Text{Value: "Provider line", Present: true}, Locked: true}
	return request
}

func TestIPTextFieldRestoreReplayAuthorityLifetime(t *testing.T) {
	w, r, p := ipTextLockFixture(t)
	cmd := ipTextLockNewCommand(t, 1, 2, ipTextRestoreRequest(t, w))
	turn, tx, b := ipTextLockTurn(t, w, r, IPFieldRestore)
	out, err := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	original := out
	ipTextExec(t, w, `UPDATE catalog_entities SET overview='Later provider line',revision=99 WHERE id=778`)
	turn, tx, b = ipTextLockTurn(t, w, r, IPFieldRestore)
	out, err = applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !out.Outcome.Replayed || out.Digest != original.Digest || !ipTextLockResultEqual(out.Value, original.Value) {
		t.Fatal("source change altered original restore replay", out, err)
	}
	ipTextRollback(t, turn, tx)
	if value := ipTextQueryInt(t, w, `SELECT count(*) FROM catalog_entities WHERE id=778 AND overview='Later provider line'`); value != 1 {
		t.Fatal("restore replay wrote", value)
	}

	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run("authority_"+mode, func(t *testing.T) {
			w, r, p := ipTextLockFixture(t)
			cmd := ipTextLockNewCommand(t, 1, 2, ipTextRestoreRequest(t, w))
			turn, tx, b := ipTextLockTurn(t, w, r, IPFieldRestore)
			if out, err := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd); err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			}
			ipTextCommit(t, turn, tx)
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b = ipTextLockTurn(t, w, r, IPFieldRestore)
			out, err := applyIPTextFieldLock(t.Context(), turn, proof, t.Context(), b, cmd)
			if !ipTextLockOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale authority served the saved restore", out, err)
			}
			ipTextRollback(t, turn, tx)
			conflicting := ipTextLockItemRequest(IPFieldRestore)
			conflicting.Field = fieldmeta.Tagline
			conflicting.Before = fieldmeta.TextState{Effective: fieldmeta.Text{Value: "Line", Present: true}}
			conflicting.Source.OwnerRevision = 2
			conflict := ipTextLockNewCommand(t, 1, 2, conflicting)
			turn, tx, b = ipTextLockTurn(t, w, r, IPFieldRestore)
			out, err = applyIPTextFieldLock(t.Context(), turn, proof, t.Context(), b, conflict)
			if !ipTextLockOutputZero(out) || !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrConflict) {
				t.Fatal("stale authority disclosed the retained restore", out, err)
			}
			ipTextRollback(t, turn, tx)
		})
	}

	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, r, p := ipTextLockFixture(t)
		cmd := ipTextLockNewCommand(t, 1, 2, ipTextRestoreRequest(t, w))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		turn, tx, b := ipTextLockTurn(t, w, r, IPFieldRestore)
		out, err := applyIPTextFieldLock(ctx, turn, p, t.Context(), b, cmd)
		if !ipTextLockOutputZero(out) || err == nil {
			t.Fatal("cancelled lifetime released a result", out, err)
		}
		ipTextRollback(t, turn, tx)
	})
}

func TestIPTextFieldLockCancelledLifetime(t *testing.T) {
	w, r, p := ipTextLockFixture(t)
	cmd := ipTextLockNewCommand(t, 1, 2, ipTextLockItemRequest(IPFieldLock))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	turn, tx, b := ipTextLockTurn(t, w, r, IPFieldLock)
	out, err := applyIPTextFieldLock(ctx, turn, p, t.Context(), b, cmd)
	if !ipTextLockOutputZero(out) || err == nil {
		t.Fatal("cancelled lifetime released a result", out, err)
	}
	ipTextRollback(t, turn, tx)
}

func TestIPTextFieldRestoreUnknownCommit(t *testing.T) {
	w, r, p := ipTextLockFixture(t)
	path := ipPolicyDatabasePath(t, w)
	cmd := ipTextLockNewCommand(t, 1, 2, ipTextRestoreRequest(t, w))
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipTextLockTurn(t, w, r, IPFieldRestore)
	out, err := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if out, e := applyIPTextFieldLock(t.Context(), turn, p, t.Context(), b, cmd); !ipTextLockOutputZero(out) || e == nil {
		t.Fatal("uncertain frame reserved the saved restore", out, e)
	}
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_owner_fields WHERE entity_id=778 AND field='description' AND value='Provider line' AND locked=0`: 1,
		`SELECT count(*) FROM catalog_entities WHERE id=778 AND overview='Provider line'`:                                               1,
		`SELECT count(*) FROM metadata_edit_receipts`:                                                                                   1,
		`SELECT count(*) FROM metadata_owner_history_fields`:                                                                            1,
	} {
		if value := ipPolicyFreshInt(t, path, query); value != want {
			t.Fatal(query, value, want)
		}
	}
}
