package writer

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const (
	iaBookTestLibraryID      int64 = 7
	iaBookTestLibraryKey           = "books-library"
	iaBookTestSourceID             = "books-source"
	iaBookTestRunID                = "books-run"
	iaBookTestAssetID        int64 = 41
	iaBookTestRoot                 = "/media/books"
	iaBookTestObjectID             = "object:book:1"
	iaBookTestObjectRevision       = "object-revision:1"
	iaBookTestDirectory            = "Author/Book"
)

// One private W frame, the frozen clock and the exact group C draft subset plus
// the C2 author-key column. The fixture seeds only prepared source/run/asset/
// search witnesses; it is not a qualified population, admission profile or
// product wiring.
func iaBookAcceptFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ia-audiobook-accept-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(8,'book',0,1,1,1,0),(9,'audiobook_file',1,0,1,1,1),(13,'author',0,1,1,1,0)`, nil},
		{`INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES('books-library','Books','audiobook','/media/books',1,1,1)`, nil},
		{`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'books-library','/media/books')`, nil},
		{`INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES('books-source','books-library','Books','/media/books','/media/books','local','books-root-identity',0,0,'',1,'books-incarnation',3,'ready',1,'local')`, nil},
		{`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('books-run','books-library','running',0,'')`, nil},
		{`INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES('books-run',7,'books-library','/media/books',0,?,?,'books-source',3,'books-incarnation','books-root-identity',4,'inventory','first-import',11,13,?,5,?,1,1)`,
			[]any{iaEpisodeBytes(3), make([]byte, 32), w.now().Add(time.Hour).UnixMilli(), iaEpisodeBytes(1)}},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('books-run','Author/Book','books-directory-identity','books-directory-revision','','pending',2,0)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(41,'book-token','/media/books/Author/Book/part1.m4b',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'raw-active',?,?,?,'active',1000)`,
			[]any{searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint}},
		{`UPDATE search_revision SET revision=1,active_generation_id=1,building_generation_id=NULL,input_sequence=0 WHERE singleton=1`, nil},
	} {
		if _, err = w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}
	registry, err := stmttest.New(t, iaBookDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{registry, keeper}, proof
}

func iaBookAcceptRequest(w *executor) iaBookInventoryRequest {
	return iaBookInventoryRequest{
		scope:  inventoryScope{runID: iaBookTestRunID, libraryKey: iaBookTestLibraryKey, libraryID: iaBookTestLibraryID},
		source: inventorySource{id: iaBookTestSourceID, incarnation: "books-incarnation", rootIdentity: "books-root-identity", resolvedRoot: []byte(iaBookTestRoot), generation: 3},
		run: inventoryRun{revision: 5, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13,
			leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(), lastSequence: 1, acknowledgedSequence: 1, phase: "inventory", scanMode: "first-import"},
		directory: inventoryDirectory{path: iaBookTestDirectory, identity: "books-directory-identity", revision: "books-directory-revision",
			cursorKind: inventoryLinuxCursor, expectedCursor: "", nextCursor: "42", pages: 2, skipped: 0, rawNames: 1, complete: true},
		files: []inventoryFile{{sourceKey: "book-asset-source", relativePath: "Author/Book/part1.m4b", assetPath: iaBookTestRoot + "/Author/Book/part1.m4b",
			objectIdentity: iaBookTestObjectID, revision: iaBookTestObjectRevision, size: 42, modifiedNS: 7, title: "part1",
			metadataLanguage: "en-US", year: 2020}},
		complete:       true,
		bookKey:        "book-key",
		bookTitle:      "Book",
		author:         "Émile",
		narrator:       "Reader",
		series:         "Saga",
		seriesPosition: "1",
		fileKey:        "file-key",
		fileTitle:      "Part 1",
		year:           2020,
	}
}

func iaBookAcceptReference(t *testing.T, r iaBookInventoryRequest, sequence int64, operation byte) ReceiptReference {
	t.Helper()
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
	command, err := newIABookInventoryCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: command.digest}
}

func iaBookAcceptTurn(t *testing.T, w *executor, r *iaEntryRegistry, q iaBookInventoryRequest) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAAcceptAudioInventory, action: iaEntryApply, libraryID: q.scope.libraryID, libraryKey: q.scope.libraryKey})
}

func iaBookAcceptRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaBookInventoryRequest, ref ReceiptReference) (*commandTurn, iaBookAcceptOutcome, error) {
	t.Helper()
	turn, budget := iaBookAcceptTurn(t, w, r, q)
	out, err := acceptIABookInventory(t.Context(), turn, p, t.Context(), budget, ref, q)
	return turn, out, err
}

// iaBookAcceptCommitted runs one fresh accept and commits it, leaving the frame
// clean for a following sequence.
func iaBookAcceptCommitted(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaBookInventoryRequest, sequence int64, operation byte) iaBookAcceptOutcome {
	t.Helper()
	ref := iaBookAcceptReference(t, q, sequence, operation)
	turn, out, err := iaBookAcceptRun(t, w, r, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	return out
}

// iaBookNextRequest advances the caller's prepared witnesses to the durable
// state left by the previous committed accept. The old original is acknowledged
// by the separate ACK path this fixture stands in for, so the next request's
// floor is contiguous rather than reopening an unacknowledged command.
func iaBookNextRequest(t *testing.T, w *executor, q iaBookInventoryRequest) iaBookInventoryRequest {
	t.Helper()
	q.run.revision++
	q.run.lastSequence++
	q.run.acknowledgedSequence = q.run.lastSequence
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=? WHERE job_id=?`, q.run.acknowledgedSequence, iaBookTestRunID)
	q.directory.expectedCursor = q.directory.nextCursor
	q.directory.pages++
	q.directory.nextCursor += "1"
	return q
}

func iaBookCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

func iaBookNoEffects(t *testing.T, w *executor) {
	t.Helper()
	for _, table := range []string{"catalog_subjects", "catalog_identities", "catalog_entities", "catalog_asset_links",
		"catalog_asset_contributions", "scan_command_receipts", "scan_media_result_members", "search_publish_jobs"} {
		if got := iaBookCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
			t.Fatal("refused request wrote", table, got)
		}
	}
	if got := iaBookCount(t, w, `SELECT last_command_sequence FROM inventory_runs WHERE job_id=?`, iaBookTestRunID); got != 1 {
		t.Fatal("refused request advanced the run", got)
	}
	if got := iaBookCount(t, w, `SELECT input_sequence FROM search_revision WHERE singleton=1`); got != 0 {
		t.Fatal("refused request advanced search input", got)
	}
}

// Fresh birth writes one complete unit: the author identity, the book with an
// author and key together, the audiobook file under that book, the file
// membership, search intent, the directory/run advance and the typed
// receipt/child cohort. Replay returns the saved result after live source/lease
// changes without any new effect.
func TestIABookAcceptFreshReplayAndLiveCohort(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	ref := iaBookAcceptReference(t, q, 2, 2)
	turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.digest == (ResultDigest{}) {
		t.Fatal("fresh book accept", out, err)
	}
	if out.result.facts.acceptedRevision != 6 || out.result.facts.appliedMS != w.now().UnixMilli() ||
		out.result.facts.resultCount != 4 || out.result.facts.memberBytes <= 0 ||
		out.result.facts.objectID != iaBookTestObjectID || string(out.result.facts.nextPayload) != "42" {
		t.Fatal("fresh book facts", out.result.facts)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		args  []any
		want  int64
	}{
		{`SELECT count(*) FROM catalog_identities WHERE library_id=7`, nil, 3},
		{`SELECT count(*) FROM catalog_entities WHERE library_id=7`, nil, 3},
		{`SELECT count(*) FROM catalog_entities WHERE kind=13 AND title='Émile'`, nil, 1},
		{`SELECT count(*) FROM catalog_identities WHERE source_key='author:émile'`, nil, 1},
		{`SELECT count(*) FROM catalog_identities WHERE source_key IN ('book-key','file-key')`, nil, 2},
		{`SELECT count(*) FROM catalog_entities WHERE kind=8 AND author='Émile' AND book_author_identity_key='author:émile' AND narrator='Reader' AND series='Saga' AND series_position='1'`, nil, 1},
		{`SELECT count(*) FROM catalog_entities WHERE kind=8 AND book_author_identity_key IS NULL`, nil, 0},
		{`SELECT count(*) FROM catalog_entities WHERE kind=8 AND parent_id=(SELECT subject_id FROM catalog_identities WHERE source_key='author:émile')`, nil, 1},
		{`SELECT count(*) FROM catalog_entities WHERE kind=9 AND book_id=(SELECT subject_id FROM catalog_identities WHERE source_key='book-key') AND parent_id=(SELECT subject_id FROM catalog_identities WHERE source_key='book-key')`, nil, 1},
		{`SELECT count(*) FROM catalog_asset_links WHERE asset_id=41 AND part_index=0 AND cue_ordinal IS NULL AND start_seconds=0 AND end_seconds IS NULL`, nil, 1},
		{`SELECT count(*) FROM catalog_asset_contributions WHERE asset_id=41 AND membership_refs=1 AND file_size=42`, nil, 1},
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='audio_inventory_accept' AND result_count=4 AND accepted_revision=6`, nil, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='audio_inventory_accept' AND ordinal BETWEEN 0 AND 3`, nil, 4},
		{`SELECT count(*) FROM search_publish_jobs`, nil, 3},
		{`SELECT count(*) FROM search_generations WHERE pending_subjects=3`, nil, 1},
		{`SELECT count(*) FROM search_revision WHERE input_sequence=1`, nil, 1},
		{`SELECT count(*) FROM inventory_directories WHERE cursor='42' AND pages=3`, nil, 1},
		{`SELECT count(*) FROM inventory_runs WHERE job_revision=6 AND last_command_sequence=2 AND acknowledged_command_sequence=1`, nil, 1},
	} {
		if got := iaBookCount(t, w, check.query, check.args...); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	var kinds string
	if err = w.db.QueryRowContext(t.Context(), `SELECT group_concat(subject_kind,',') FROM (SELECT subject_kind FROM scan_media_result_members WHERE command_kind='audio_inventory_accept' ORDER BY ordinal)`).Scan(&kinds); err != nil || kinds != "13,8,9,9" {
		t.Fatal("member chronology", kinds, err)
	}
	// Live source/lease changes never edit or filter the saved result.
	wc008NativeExec(t, w, `UPDATE library_sources SET generation=9,root='/moved',configured_root='/moved' WHERE id='books-source'`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET job_revision=99,job_lease_expires_ms=1 WHERE job_id='books-run'`)
	turn, replay, err := iaBookAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest || replay.result.facts.resultCount != 4 ||
		replay.result.facts.acceptedRevision != out.result.facts.acceptedRevision {
		t.Fatal("saved book replay", replay, err)
	}
	if got := iaBookCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 1 {
		t.Fatal("replay minted a receipt", got)
	}
	turn.transaction.rollback(nil)
}

// The author is one exact Unicode identity: Go lowercase only, no trim, no
// normalization and no full case fold. The book always carries its own exact
// author bytes beside the shared key, and a reused author is never renamed.
func TestIABookAcceptAuthorIdentityExactness(t *testing.T) {
	t.Run("emile_case_reuses_author_without_rename", func(t *testing.T) {
		w, reg, p := iaBookAcceptFixture(t)
		q := iaBookAcceptRequest(w)
		iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
		second := iaBookNextRequest(t, w, q)
		second.bookKey, second.fileKey = "book-key-2", "file-key-2"
		second.author = "émile"
		iaBookAcceptCommitted(t, w, reg, p, second, 3, 3)
		if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=13`); got != 1 {
			t.Fatal("lowercase form minted a second author", got)
		}
		if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=13 AND title='Émile'`); got != 1 {
			t.Fatal("reused author was renamed", got)
		}
		if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=8 AND book_author_identity_key='author:émile'`); got != 2 {
			t.Fatal("books do not share the exact key", got)
		}
		if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=8 AND author='Émile'`); got != 1 {
			t.Fatal("first book author bytes changed", got)
		}
		if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=8 AND author='émile'`); got != 1 {
			t.Fatal("second book author bytes not exact", got)
		}
		if got := iaBookCount(t, w, `SELECT count(DISTINCT parent_id) FROM catalog_entities WHERE kind=8`); got != 1 {
			t.Fatal("books do not share the author parent", got)
		}
	})

	t.Run("strasse_and_strasse_upper_stay_distinct", func(t *testing.T) {
		w, reg, p := iaBookAcceptFixture(t)
		q := iaBookAcceptRequest(w)
		q.author = "Straße"
		iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
		second := iaBookNextRequest(t, w, q)
		second.author = "STRASSE"
		second.bookKey, second.fileKey = "book-key-2", "file-key-2"
		iaBookAcceptCommitted(t, w, reg, p, second, 3, 3)
		if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=13`); got != 2 {
			t.Fatal("full case fold substituted an author", got)
		}
		for _, key := range []string{"author:straße", "author:strasse"} {
			if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=8 AND book_author_identity_key=?`, key); got != 1 {
				t.Fatal("missing exact key", key, got)
			}
			if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_identities WHERE source_key=?`, key); got != 1 {
				t.Fatal("missing exact author identity", key, got)
			}
		}
	})

	t.Run("composed_and_decomposed_stay_distinct", func(t *testing.T) {
		w, reg, p := iaBookAcceptFixture(t)
		q := iaBookAcceptRequest(w)
		q.author = "\u00c9"
		iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
		second := iaBookNextRequest(t, w, q)
		second.author = "E\u0301"
		second.bookKey, second.fileKey = "book-key-2", "file-key-2"
		iaBookAcceptCommitted(t, w, reg, p, second, 3, 3)
		if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=13`); got != 2 {
			t.Fatal("normalization substituted an author", got)
		}
		for _, key := range []string{"author:\u00e9", "author:e\u0301"} {
			if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=8 AND book_author_identity_key=?`, key); got != 1 {
				t.Fatal("missing exact key", key, got)
			}
		}
	})

	t.Run("blank_authors_keep_exact_keys_without_membership", func(t *testing.T) {
		w, reg, p := iaBookAcceptFixture(t)
		q := iaBookAcceptRequest(w)
		q.author = ""
		q.bookKey, q.fileKey = "book-empty", "file-empty"
		out := iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
		if out.result.facts.resultCount != 3 {
			t.Fatal("empty author gained a membership", out.result.facts.resultCount)
		}
		second := iaBookNextRequest(t, w, q)
		second.author = " "
		second.bookKey, second.fileKey = "book-space", "file-space"
		out = iaBookAcceptCommitted(t, w, reg, p, second, 3, 3)
		if out.result.facts.resultCount != 3 {
			t.Fatal("space author gained a membership", out.result.facts.resultCount)
		}
		if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=13`); got != 0 {
			t.Fatal("blank author minted an author entity", got)
		}
		if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=8 AND author='' AND book_author_identity_key='author:' AND parent_id IS NULL`); got != 1 {
			t.Fatal("empty author bytes and key not stored together", got)
		}
		if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=8 AND author=' ' AND book_author_identity_key='author: ' AND parent_id IS NULL`); got != 1 {
			t.Fatal("space author was trimmed or detached", got)
		}
	})
}

// Parent IDs are assigned before children: the author subject is born first,
// then the book under it, then the audiobook file under the book.
func TestIABookAcceptChronology(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
	var authorID, bookID, fileID int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT subject_id FROM catalog_identities WHERE source_key='author:émile'`).Scan(&authorID); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT subject_id FROM catalog_identities WHERE source_key='book-key'`).Scan(&bookID); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT subject_id FROM catalog_identities WHERE source_key='file-key'`).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	if !(authorID > 0 && authorID < bookID && bookID < fileID) {
		t.Fatal("creation order", authorID, bookID, fileID)
	}
	if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=? AND kind=8 AND parent_id=?`, bookID, authorID); got != 1 {
		t.Fatal("book parent chronology", got)
	}
	if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id=? AND kind=9 AND parent_id=? AND book_id=?`, fileID, bookID, bookID); got != 1 {
		t.Fatal("file parent chronology", got)
	}
	type row struct {
		ordinal, subjectKind, parentKind int64
	}
	var got []row
	rows, err := w.db.QueryContext(t.Context(), `SELECT ordinal,subject_kind,COALESCE(parent_kind,0) FROM scan_media_result_members WHERE command_kind='audio_inventory_accept' ORDER BY ordinal`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.ordinal, &r.subjectKind, &r.parentKind); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []row{{0, 13, 0}, {1, 8, 13}, {2, 9, 8}, {3, 9, 0}}
	if len(got) != len(want) {
		t.Fatal("member cohort", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal("member chronology", got)
		}
	}
}

// Every bound is enforced before the first effect.
func TestIABookAcceptBounds(t *testing.T) {
	rawRef := func() ReceiptReference {
		return ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}, RequestDigest: RequestDigest{1}}
	}
	for name, mutate := range map[string]func(*iaBookInventoryRequest){
		"no_file":        func(q *iaBookInventoryRequest) { q.files = nil },
		"blank_title":    func(q *iaBookInventoryRequest) { q.bookTitle = "   " },
		"blank_file":     func(q *iaBookInventoryRequest) { q.fileTitle = "   " },
		"empty_book_key": func(q *iaBookInventoryRequest) { q.bookKey = "" },
		"series_position_without_series": func(q *iaBookInventoryRequest) {
			q.series, q.seriesPosition = "", "1"
		},
		"overlong_author": func(q *iaBookInventoryRequest) {
			q.author = strings.Repeat("a", MaxTitleCharacters+1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaBookAcceptFixture(t)
			q := iaBookAcceptRequest(w)
			mutate(&q)
			turn, out, err := iaBookAcceptRun(t, w, reg, p, q, rawRef())
			if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
				t.Fatal("bound", name, out, err)
			}
			turn.transaction.rollback(nil)
			iaBookNoEffects(t, w)
		})
	}
	t.Run("max_author", func(t *testing.T) {
		w, reg, p := iaBookAcceptFixture(t)
		q := iaBookAcceptRequest(w)
		q.author = strings.Repeat("a", MaxTitleCharacters)
		iaBookAcceptCommitted(t, w, reg, p, q, 2, 2)
		var length int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT length(book_author_identity_key) FROM catalog_entities WHERE kind=8`).Scan(&length); err != nil {
			t.Fatal(err)
		}
		if want := int64(len("author:") + MaxTitleCharacters); length != want {
			t.Fatal("max author key length", length, want)
		}
	})
}

// Every fresh witness is re-proven after the receipt is Fresh. A changed
// library, source, directory, asset or author key refuses the complete unit
// before any catalogue/receipt row exists.
func TestIABookAcceptFreshGates(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *executor, *iaBookInventoryRequest){
		"retired_library": func(t *testing.T, w *executor, _ *iaBookInventoryRequest) {
			wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=7`)
		},
		"stale_source":    func(_ *testing.T, _ *executor, q *iaBookInventoryRequest) { q.source.generation++ },
		"stale_directory": func(_ *testing.T, _ *executor, q *iaBookInventoryRequest) { q.directory.expectedCursor = "9" },
		"changed_asset":   func(_ *testing.T, _ *executor, q *iaBookInventoryRequest) { q.files[0].size++ },
		"changed_lease": func(t *testing.T, w *executor, q *iaBookInventoryRequest) {
			q.run.leaseExpiresMS = w.now().UnixMilli()
		},
		"stale_run_revision": func(_ *testing.T, _ *executor, q *iaBookInventoryRequest) { q.run.revision++ },
		"stale_book_author_key": func(t *testing.T, w *executor, _ *iaBookInventoryRequest) {
			wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(99,X'99000000000000000000000000000000',7,8)`)
			wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,author,book_author_identity_key) VALUES(99,X'99000000000000000000000000000000',7,8,'Book','book','b','book','b',1,zeroblob(16),zeroblob(16),'Émile','author:other')`)
			wc008NativeExec(t, w, `INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(7,'book-key',99)`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaBookAcceptFixture(t)
			q := iaBookAcceptRequest(w)
			mutate(t, w, &q)
			ref := iaBookAcceptReference(t, q, 2, 2)
			turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
				t.Fatal("fresh gate", out, err)
			}
			turn.transaction.rollback(nil)
			if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind IN(8,9,13) AND id<>99`); got != 0 {
				t.Fatal("gate wrote a catalogue entity", got)
			}
			if got := iaBookCount(t, w, `SELECT count(*) FROM catalog_asset_links`); got != 0 {
				t.Fatal("gate wrote a link", got)
			}
			if got := iaBookCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 0 {
				t.Fatal("gate wrote a receipt", got)
			}
		})
	}
}

// Current authority precedes every retained read: a forged issuer, a demoted
// owner and a cancelled caller lifetime all deny with zero public output.
func TestIABookAcceptAuthorityCells(t *testing.T) {
	t.Run("forged_issuer", func(t *testing.T) {
		w, reg, p := iaBookAcceptFixture(t)
		q := iaBookAcceptRequest(w)
		ref := iaBookAcceptReference(t, q, 2, 2)
		fake := *p
		fake.witness.issuer.family = "forged"
		turn, out, err := iaBookAcceptRun(t, w, reg, &fake, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("forged issuer", out, err)
		}
		turn.transaction.rollback(nil)
		iaBookNoEffects(t, w)
	})
	t.Run("stale_authority", func(t *testing.T) {
		for name, mutation := range map[string]string{
			"demoted_owner":  `UPDATE direct_memberships SET role='member'`,
			"retired_token":  `UPDATE authorization_family_tokens SET retired=1`,
			"denied_device":  `UPDATE identity_security_fences SET denied=1`,
			"profile_reset":  `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
			"reset_session":  `INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','owner')`,
			"narrowed_grant": `INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','cast_target')`,
		} {
			t.Run(name, func(t *testing.T) {
				w, reg, p := iaBookAcceptFixture(t)
				wc008NativeExec(t, w, mutation)
				q := iaBookAcceptRequest(w)
				ref := iaBookAcceptReference(t, q, 2, 2)
				turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
				if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
					t.Fatal("stale authority", out, err)
				}
				turn.transaction.rollback(nil)
				iaBookNoEffects(t, w)
			})
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, reg, p := iaBookAcceptFixture(t)
		q := iaBookAcceptRequest(w)
		ref := iaBookAcceptReference(t, q, 2, 2)
		turn, budget := iaBookAcceptTurn(t, w, reg, q)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := acceptIABookInventory(cancelled, turn, p, cancelled, budget, ref, q)
		if err == nil || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("cancelled lifetime", out, err)
		}
		turn.transaction.rollback(nil)
		iaBookNoEffects(t, w)
	})
}

// Current authority is re-observed after the fresh gates and again after the
// writes. A same-turn revocation injected by the last read or by a catalogue
// write must deny the fresh result and leave no committed effect.
func TestIABookAcceptLateAuthorityRechecks(t *testing.T) {
	t.Run("before_writes", func(t *testing.T) {
		w, reg, p := iaBookAcceptFixture(t)
		state := installIAEntryQueryRevocation(t, w, "FROM catalog_assets")
		q := iaBookAcceptRequest(w)
		ref := iaBookAcceptReference(t, q, 2, 2)
		turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
		if !state.fired || state.err != nil {
			t.Fatal("asset-close mutation did not run", state.err)
		}
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("pre-write recheck released a stale result", out, err)
		}
		turn.transaction.rollback(nil)
		iaBookNoEffects(t, w)
	})
	t.Run("after_writes", func(t *testing.T) {
		w, reg, p := iaBookAcceptFixture(t)
		wc008NativeExec(t, w, `CREATE TRIGGER ia_book_late_revocation AFTER INSERT ON catalog_asset_links BEGIN UPDATE identity_security_fences SET denied=1; END`)
		q := iaBookAcceptRequest(w)
		ref := iaBookAcceptReference(t, q, 2, 2)
		turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("post-write recheck released a stale result", out, err)
		}
		var links int64
		if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_asset_links`).Scan(&links); err != nil || links != 1 {
			t.Fatal("link write not reached before the final recheck", links, err)
		}
		turn.transaction.rollback(nil)
		iaBookNoEffects(t, w)
	})
}

// A saved cohort must be complete and digest-exact before it is served. A
// missing member, a wrong retained-byte ledger and a changed result digest all
// refuse the replay with zero public output.
func TestIABookAcceptReplayRejectsTamperedCohort(t *testing.T) {
	for name, mutate := range map[string]string{
		"missing_member": `DELETE FROM scan_media_result_members WHERE ordinal=3`,
		"member_bytes":   `UPDATE scan_command_receipts SET ia_media_member_bytes=1`,
		"result_digest":  `UPDATE scan_command_receipts SET result_digest=zeroblob(32)`,
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaBookAcceptFixture(t)
			q := iaBookAcceptRequest(w)
			ref := iaBookAcceptReference(t, q, 2, 2)
			turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
			if err != nil || out.decision != receiptFresh {
				t.Fatal(out, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			wc008NativeExec(t, w, mutate)
			turn, replay, err := iaBookAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrResolveRequired) || replay.decision != 0 || replay.digest != (ResultDigest{}) || len(replay.result.facts.members) != 0 {
				t.Fatal("tampered cohort served", replay, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

// A stored search bank/profile that cannot be honored refuses the whole unit
// instead of accepting a catalogue cohort with no search intent.
func TestIABookAcceptRefusesUnusableSearchBank(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	wc008NativeExec(t, w, `UPDATE search_generations SET unicode_fingerprint='bad'`)
	q := iaBookAcceptRequest(w)
	ref := iaBookAcceptReference(t, q, 2, 2)
	turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
	if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
		t.Fatal("unusable search bank", out, err)
	}
	turn.transaction.rollback(nil)
	iaBookNoEffects(t, w)
}

// A lost COMMIT reply is never success. The committed file is read back through
// a FRESH read-only connection, never the fenced writer pool.
func TestIABookAcceptUnknownCommitIsNotSuccess(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	path := iaEpisodePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	q := iaBookAcceptRequest(w)
	ref := iaBookAcceptReference(t, q, 2, 2)
	turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal("fresh before commit", out, err)
	}
	result, err := turn.transaction.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	readCfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, readCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM catalog_entities WHERE library_id=7`, 3},
		{`SELECT count(*) FROM catalog_entities WHERE kind=8 AND author='Émile' AND book_author_identity_key='author:émile'`, 1},
		{`SELECT count(*) FROM catalog_entities WHERE kind=9 AND book_id=(SELECT subject_id FROM catalog_identities WHERE source_key='book-key')`, 1},
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='audio_inventory_accept' AND result_count=4`, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='audio_inventory_accept'`, 4},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	if _, err = w.begin(t.Context(), IAAcceptAudioInventory, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

// At or below the durable ACK floor the original is Retired: no result, no
// digest and no new effect, even while the physical rows still exist.
func TestIABookAcceptRetiredAfterAck(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	ref := iaBookAcceptReference(t, q, 2, 2)
	turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='books-run'`)
	before := map[string]int64{}
	for _, table := range []string{"catalog_subjects", "catalog_entities", "scan_command_receipts", "scan_media_result_members", "search_publish_jobs"} {
		before[table] = iaBookCount(t, w, `SELECT count(*) FROM `+table)
	}
	turn, out, err = iaBookAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptRetired || out.digest != (ResultDigest{}) || out.result.facts.resultCount != 0 || len(out.result.facts.members) != 0 {
		t.Fatal("retired original", out, err)
	}
	turn.transaction.rollback(nil)
	for table, want := range before {
		if got := iaBookCount(t, w, `SELECT count(*) FROM `+table); got != want {
			t.Fatal("retired replay wrote", table, got, want)
		}
	}
}

// A conflicting digest and a non-contiguous sequence are conflicts, never a
// fresh admission or a new receipt.
func TestIABookAcceptRejectsConflictingOriginal(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	ref := iaBookAcceptReference(t, q, 2, 2)
	changed := q
	changed.author = "Someone Else"
	turn, out, err := iaBookAcceptRun(t, w, reg, p, changed, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("changed request digest", out, err)
	}
	turn.transaction.rollback(nil)
	gap := q
	// The request is internally consistent for sequence 3, but the durable owner
	// is still at sequence 1: a gap, never a fresh admission.
	gap.run.lastSequence, gap.run.acknowledgedSequence = 2, 2
	gapRef := iaBookAcceptReference(t, gap, 3, 4)
	turn, out, err = iaBookAcceptRun(t, w, reg, p, gap, gapRef)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("sequence gap", out, err)
	}
	turn.transaction.rollback(nil)
	iaBookNoEffects(t, w)
}

func TestIABookAcceptRollbackLeavesNoEffect(t *testing.T) {
	w, reg, p := iaBookAcceptFixture(t)
	q := iaBookAcceptRequest(w)
	ref := iaBookAcceptReference(t, q, 2, 2)
	turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	iaBookNoEffects(t, w)
	if got := iaBookCount(t, w, `SELECT count(*) FROM inventory_directories WHERE cursor='' AND pages=2`); got != 1 {
		t.Fatal("rollback advanced the directory", got)
	}
}
