package writer

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const (
	iaReadingTestLibraryID  int64 = 7
	iaReadingTestLibraryKey       = "reading-library"
	iaReadingTestSourceID         = "reading-source"
	iaReadingTestRunID            = "reading-run"
	iaReadingTestItemKey          = "reading-item-key"
)

func iaReadingRoot(kind int64) string {
	if kind == 21 {
		return "/media/comics"
	}
	return "/media/books"
}

func iaReadingDirectoryPath(kind int64) string {
	if kind == 21 {
		return "Comics/Novel"
	}
	return "Books/Novel"
}

// Every admitted reading source format for one item kind, in the exact closed
// order the accept uses. PDF belongs to both kinds.
func iaReadingFormats(kind int64) []string {
	if kind == 21 {
		return []string{"cbz", "pdf", "cb7", "cbr"}
	}
	return []string{"epub", "pdf", "mobi", "azw3"}
}

func iaReadingAssetID(kind int64, format string) int64 {
	base := int64(41)
	if kind == 21 {
		base = 45
	}
	for i, candidate := range iaReadingFormats(kind) {
		if candidate == format {
			return base + int64(i)
		}
	}
	return 0
}

// One private W frame, the frozen clock, the exact group C draft subset and the
// embedded reading catalogue tables. The fixture seeds only prepared source/run/
// job/directory/asset/search witnesses; it is not a qualified population,
// admission profile or product wiring.
func iaReadingAcceptFixture(t *testing.T, kind int64) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ia-reading-accept-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	root, dir := iaReadingRoot(kind), iaReadingDirectoryPath(kind)
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(20,'ebook',0,0,1,1,0),(21,'comic',0,0,1,1,0)`, nil},
		{`INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES('reading-library','Reading',?,?,1,1,1)`,
			[]any{iaReadingLibraryKindName(kind), root}},
		{`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'reading-library',?)`, []any{root}},
		{`INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES('reading-source','reading-library','Reading',?,?,'local','reading-root-identity',0,0,'',1,'reading-incarnation',3,'ready',1,'local')`,
			[]any{root, root}},
		{`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('reading-run','reading-library','running',0,'')`, nil},
		{`INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES('reading-run',7,'reading-library',?,0,?,?,'reading-source',3,'reading-incarnation','reading-root-identity',4,'inventory','first-import',11,13,?,5,?,1,1)`,
			[]any{root, iaEpisodeBytes(3), make([]byte, 32), w.now().Add(time.Hour).UnixMilli(), iaEpisodeBytes(1)}},
		{`INSERT INTO inventory_reading_jobs(job_id,source_id,incarnation,generation,root_identity,root,coverage_revision) VALUES('reading-run','reading-source','reading-incarnation',3,'reading-root-identity',?,0)`,
			[]any{root}},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('reading-run',?,'reading-directory-identity','reading-directory-revision','','pending',2,0)`,
			[]any{dir}},
		{`INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'raw-active',?,?,?,'active',1000)`,
			[]any{searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint}},
		{`UPDATE search_revision SET revision=1,active_generation_id=1,building_generation_id=NULL,input_sequence=0 WHERE singleton=1`, nil},
	} {
		if _, err = w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}
	for _, format := range iaReadingFormats(kind) {
		path := root + "/" + dir + "/novel." + format
		if _, err = w.db.ExecContext(t.Context(), `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,42,7,'','','',0,0,0,7)`,
			iaReadingAssetID(kind, format), "reading-token-"+format, path); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := stmttest.New(t, iaReadingDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{registry, keeper}, proof
}

func iaReadingAcceptRequest(w *executor, kind int64, format string) iaReadingInventoryRequest {
	root, dir := iaReadingRoot(kind), iaReadingDirectoryPath(kind)
	relative := dir + "/novel." + format
	return iaReadingInventoryRequest{
		scope:  inventoryScope{runID: iaReadingTestRunID, libraryKey: iaReadingTestLibraryKey, libraryID: iaReadingTestLibraryID},
		source: inventorySource{id: iaReadingTestSourceID, incarnation: "reading-incarnation", rootIdentity: "reading-root-identity", resolvedRoot: []byte(root), generation: 3},
		run: inventoryRun{revision: 5, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13,
			leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(), lastSequence: 1, acknowledgedSequence: 1, phase: "inventory", scanMode: "first-import"},
		directory: inventoryDirectory{path: dir, identity: "reading-directory-identity", revision: "reading-directory-revision",
			cursorKind: inventoryLinuxCursor, expectedCursor: "", nextCursor: "42", pages: 2, skipped: 0, rawNames: 1, complete: true},
		files: []inventoryFile{{sourceKey: "reading-asset-source", relativePath: relative, assetPath: root + "/" + relative,
			objectIdentity: "object:" + format, revision: "object-revision:" + format, size: 42, modifiedNS: 7, title: "novel",
			metadataLanguage: "en-US", year: 2020}},
		complete:        true,
		itemKey:         iaReadingTestItemKey,
		itemTitle:       "Novel",
		itemKind:        kind,
		format:          format,
		publicationType: "book",
		pathHead:        true,
		pathSequence:    1,
	}
}

func iaReadingAcceptReference(t *testing.T, r iaReadingInventoryRequest, sequence int64, operation byte) ReceiptReference {
	t.Helper()
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
	command, err := newIAReadingInventoryCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: command.digest}
}

func iaReadingRawReference(sequence int64, operation byte) ReceiptReference {
	return ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}, RequestDigest: RequestDigest{1}}
}

func iaReadingAcceptTurn(t *testing.T, w *executor, r *iaEntryRegistry, q iaReadingInventoryRequest) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAAcceptReadingInventory, action: iaEntryApply, libraryID: q.scope.libraryID, libraryKey: q.scope.libraryKey})
}

func iaReadingAcceptRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaReadingInventoryRequest, ref ReceiptReference) (*commandTurn, iaReadingAcceptOutcome, error) {
	t.Helper()
	turn, budget := iaReadingAcceptTurn(t, w, r, q)
	out, err := acceptIAReadingInventory(t.Context(), turn, p, t.Context(), budget, ref, q)
	return turn, out, err
}

// iaReadingAcceptCommitted runs one fresh accept and commits it, leaving the
// frame clean for a following sequence.
func iaReadingAcceptCommitted(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaReadingInventoryRequest, sequence int64, operation byte) iaReadingAcceptOutcome {
	t.Helper()
	ref := iaReadingAcceptReference(t, q, sequence, operation)
	turn, out, err := iaReadingAcceptRun(t, w, r, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	return out
}

// iaReadingNextRequest advances the caller's prepared witnesses to the durable
// state left by the previous committed accept. The old original is acknowledged
// by the separate ACK path this fixture stands in for, so the next request's
// floor is contiguous rather than reopening an unacknowledged command.
func iaReadingNextRequest(t *testing.T, w *executor, q iaReadingInventoryRequest) iaReadingInventoryRequest {
	t.Helper()
	q.run.revision++
	q.run.lastSequence++
	q.run.acknowledgedSequence = q.run.lastSequence
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=? WHERE job_id=?`, q.run.acknowledgedSequence, iaReadingTestRunID)
	q.directory.expectedCursor = q.directory.nextCursor
	q.directory.pages++
	q.directory.nextCursor += "1"
	return q
}

func iaReadingCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

func iaReadingNoEffects(t *testing.T, w *executor) {
	t.Helper()
	for _, table := range []string{"catalog_subjects", "catalog_identities", "catalog_entities", "catalog_asset_links",
		"catalog_asset_contributions", "reading_publications", "scan_command_receipts", "scan_media_result_members", "search_publish_jobs"} {
		if got := iaReadingCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
			t.Fatal("refused request wrote", table, got)
		}
	}
	if got := iaReadingCount(t, w, `SELECT last_command_sequence FROM inventory_runs WHERE job_id=?`, iaReadingTestRunID); got != 1 {
		t.Fatal("refused request advanced the run", got)
	}
	if got := iaReadingCount(t, w, `SELECT input_sequence FROM search_revision WHERE singleton=1`); got != 0 {
		t.Fatal("refused request advanced search input", got)
	}
}

// iaReadingSeedItem creates one already accepted reading item, optionally with
// one attached publication on the first admitted asset. It is fixture evidence
// only, never a bypass of the accept path.
func iaReadingSeedItem(t *testing.T, w *executor, key string, kind int64, publicationType string, withPublication bool) int64 {
	t.Helper()
	var publication, publicationSource any
	if publicationType != "" {
		publication, publicationSource = publicationType, "file-metadata"
	}
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(99,X'99000000000000000000000000000000',7,?)`, kind)
	wc008NativeExec(t, w, `INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(7,?,99)`, key)
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,reading_publication_type,reading_publication_type_source) VALUES(99,X'99000000000000000000000000000000',7,?,'Novel','novel','n','novel','n',1,zeroblob(16),zeroblob(16),?,?)`,
		kind, publication, publicationSource)
	if withPublication {
		asset := iaReadingAssetID(kind, iaReadingFormats(kind)[0])
		wc008NativeExec(t, w, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,available,start_seconds,edition,playback_origin_incarnation,playback_origin_revision,library_id) VALUES(99,?,0,1,0,'',X'00000000000000000000000000000001',1,7)`, asset)
		wc008NativeExec(t, w, `INSERT INTO reading_publications(item_id,asset_id,format,has_reading_path_head,path_publisher_sequence,path_object_id,path_source_revision) VALUES(99,?,?,1,1,'object:seed','revision:seed')`, asset, iaReadingFormats(kind)[0])
	}
	return 99
}

// Fresh birth writes one complete unit: the reading item identity, its exact
// publication attachment, search intent, the directory/run advance and the typed
// receipt/child cohort. Replay returns the saved result after live source/lease
// changes without any new effect.
func TestIAReadingAcceptFreshReplayAndLiveCohort(t *testing.T) {
	w, reg, p := iaReadingAcceptFixture(t, 20)
	q := iaReadingAcceptRequest(w, 20, "epub")
	ref := iaReadingAcceptReference(t, q, 2, 2)
	turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.digest == (ResultDigest{}) {
		t.Fatal("fresh reading accept", out, err)
	}
	facts := out.result.facts
	if facts.acceptedRevision != 6 || facts.appliedMS != w.now().UnixMilli() || facts.resultCount != 1 || facts.memberBytes <= 0 ||
		facts.objectID != "object:epub" || facts.objectRevision != "object-revision:epub" || string(facts.nextPayload) != "42" {
		t.Fatal("fresh reading facts", facts)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		args  []any
		want  int64
	}{
		{`SELECT count(*) FROM catalog_identities WHERE library_id=7 AND source_key='reading-item-key'`, nil, 1},
		{`SELECT count(*) FROM catalog_entities WHERE library_id=7 AND kind=20 AND reading_publication_type='book' AND reading_publication_type_source='file-metadata'`, nil, 1},
		{`SELECT count(*) FROM catalog_asset_links WHERE entity_id=(SELECT subject_id FROM catalog_identities WHERE source_key='reading-item-key') AND asset_id=?`, []any{iaReadingAssetID(20, "epub")}, 1},
		{`SELECT count(*) FROM catalog_asset_contributions WHERE asset_id=? AND membership_refs=1 AND file_size=42`, []any{iaReadingAssetID(20, "epub")}, 1},
		{`SELECT count(*) FROM reading_publications WHERE item_id=(SELECT subject_id FROM catalog_identities WHERE source_key='reading-item-key') AND asset_id=? AND format='epub' AND has_reading_path_head=1 AND path_publisher_sequence=1 AND path_object_id='object:epub' AND path_source_revision='object-revision:epub'`, []any{iaReadingAssetID(20, "epub")}, 1},
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='reading_inventory_accept' AND result_count=1 AND accepted_revision=6`, nil, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='reading_inventory_accept' AND ordinal=0 AND member_kind='reading' AND reading_format='epub' AND reading_path_head=1 AND reading_path_sequence=1`, nil, 1},
		{`SELECT count(*) FROM search_publish_jobs`, nil, 1},
		{`SELECT count(*) FROM search_generations WHERE pending_subjects=1`, nil, 1},
		{`SELECT count(*) FROM search_revision WHERE input_sequence=1`, nil, 1},
		{`SELECT count(*) FROM inventory_directories WHERE cursor='42' AND pages=3`, nil, 1},
		{`SELECT count(*) FROM inventory_runs WHERE job_revision=6 AND last_command_sequence=2 AND acknowledged_command_sequence=1`, nil, 1},
	} {
		if got := iaReadingCount(t, w, check.query, check.args...); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// Live source/lease/publication changes never edit or filter the saved result.
	wc008NativeExec(t, w, `UPDATE library_sources SET generation=9,root='/moved',configured_root='/moved' WHERE id='reading-source'`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET job_revision=99,job_lease_expires_ms=1 WHERE job_id='reading-run'`)
	wc008NativeExec(t, w, `UPDATE reading_publications SET format='pdf'`)
	wc008NativeExec(t, w, `DELETE FROM inventory_reading_jobs WHERE job_id='reading-run'`)
	turn, replay, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest || replay.result.facts.resultCount != 1 ||
		replay.result.facts.acceptedRevision != facts.acceptedRevision || *replay.result.facts.members[0].readingFormat != "epub" {
		t.Fatal("saved reading replay", replay, err)
	}
	// The replay frame still holds the single writer connection, so the
	// no-new-receipt check reads the open transaction itself.
	var receipts int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("replay minted a receipt", receipts, err)
	}
	turn.transaction.rollback(nil)
}

// Every admitted format is accepted only for its own item kind, and the
// publication carries the exact observed format. CB7/CBR stay behind the
// existing build capability: without it the whole unit is refused.
func TestIAReadingAcceptFormats(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   int64
		format string
		admits bool
	}{
		{"epub", 20, "epub", true},
		{"pdf_book", 20, "pdf", true},
		{"mobi", 20, "mobi", true},
		{"azw3", 20, "azw3", true},
		{"cbz", 21, "cbz", true},
		{"pdf_comic", 21, "pdf", true},
		{"cb7", 21, "cb7", readingcap.CB7()},
		{"cbr", 21, "cbr", readingcap.CBR()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, reg, p := iaReadingAcceptFixture(t, tc.kind)
			q := iaReadingAcceptRequest(w, tc.kind, tc.format)
			if !tc.admits {
				turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, iaReadingRawReference(2, 2))
				if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
					t.Fatal("unadmitted format", tc.format, out, err)
				}
				turn.transaction.rollback(nil)
				iaReadingNoEffects(t, w)
				return
			}
			out := iaReadingAcceptCommitted(t, w, reg, p, q, 2, 2)
			if out.result.facts.resultCount != 1 {
				t.Fatal("format cohort", tc.format, out.result.facts)
			}
			if got := iaReadingCount(t, w, `SELECT count(*) FROM reading_publications WHERE format=? AND has_reading_path_head=1`, tc.format); got != 1 {
				t.Fatal("publication format", tc.format, got)
			}
			if got := iaReadingCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=?`, tc.kind); got != 1 {
				t.Fatal("item kind", tc.kind, got)
			}
			if got := iaReadingCount(t, w, `SELECT count(*) FROM scan_media_result_members WHERE reading_format=? AND reading_path_head=1 AND reading_path_sequence=1`, tc.format); got != 1 {
				t.Fatal("member format", tc.format, got)
			}
		})
	}
}

// A publication type is descriptive evidence, never an item or library identity.
// Absence stays distinct from a present value and only the closed set is stored.
func TestIAReadingAcceptPublicationTypes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  string
		stored bool
	}{
		{"absent", "", false},
		{"book", "book", true},
		{"magazine", "magazine", true},
		{"newspaper", "newspaper", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, reg, p := iaReadingAcceptFixture(t, 20)
			q := iaReadingAcceptRequest(w, 20, "epub")
			q.publicationType = tc.value
			iaReadingAcceptCommitted(t, w, reg, p, q, 2, 2)
			if tc.stored {
				if got := iaReadingCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=20 AND reading_publication_type=? AND reading_publication_type_source='file-metadata'`, tc.value); got != 1 {
					t.Fatal("stored publication type", tc.value, got)
				}
				return
			}
			if got := iaReadingCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=20 AND reading_publication_type IS NULL AND reading_publication_type_source IS NULL`); got != 1 {
				t.Fatal("absent publication type invented", got)
			}
		})
	}
}

// Every bound is enforced before the first effect.
func TestIAReadingAcceptBounds(t *testing.T) {
	for name, mutate := range map[string]func(*iaReadingInventoryRequest){
		"no_file":          func(q *iaReadingInventoryRequest) { q.files = nil },
		"blank_title":      func(q *iaReadingInventoryRequest) { q.itemTitle = "   " },
		"empty_key":        func(q *iaReadingInventoryRequest) { q.itemKey = "" },
		"unknown_kind":     func(q *iaReadingInventoryRequest) { q.itemKind = 19 },
		"format_mismatch":  func(q *iaReadingInventoryRequest) { q.format = "pdf" },
		"head_without_seq": func(q *iaReadingInventoryRequest) { q.pathSequence = 0 },
		"seq_without_head": func(q *iaReadingInventoryRequest) { q.pathHead, q.pathSequence = false, 1 },
		"bad_publication":  func(q *iaReadingInventoryRequest) { q.publicationType = "journal" },
		"control_text":     func(q *iaReadingInventoryRequest) { q.itemTitle = "Novel\x00" },
		"overlong_title":   func(q *iaReadingInventoryRequest) { q.itemTitle = strings.Repeat("a", MaxTitleCharacters+1) },
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaReadingAcceptFixture(t, 20)
			q := iaReadingAcceptRequest(w, 20, "epub")
			mutate(&q)
			turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, iaReadingRawReference(2, 2))
			if err == nil || !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrCap) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
				t.Fatal("bound", name, out, err)
			}
			turn.transaction.rollback(nil)
			iaReadingNoEffects(t, w)
		})
	}
	t.Run("empty_payload", func(t *testing.T) {
		w, reg, p := iaReadingAcceptFixture(t, 20)
		q := iaReadingAcceptRequest(w, 20, "epub")
		q.files[0].size = 0
		wc008NativeExec(t, w, `UPDATE catalog_assets SET size=0 WHERE id=?`, iaReadingAssetID(20, "epub"))
		turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, iaReadingRawReference(2, 2))
		if err == nil || !errors.Is(err, ErrInvalid) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
			t.Fatal("empty payload", out, err)
		}
		turn.transaction.rollback(nil)
		iaReadingNoEffects(t, w)
	})
	t.Run("max_title", func(t *testing.T) {
		w, reg, p := iaReadingAcceptFixture(t, 20)
		q := iaReadingAcceptRequest(w, 20, "epub")
		q.itemTitle = strings.Repeat("a", MaxTitleCharacters)
		iaReadingAcceptCommitted(t, w, reg, p, q, 2, 2)
		var length int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT length(title) FROM catalog_entities WHERE kind=20`).Scan(&length); err != nil {
			t.Fatal(err)
		}
		if length != int64(MaxTitleCharacters) {
			t.Fatal("max title length", length)
		}
	})
}

// Every fresh witness is re-proven after the receipt is Fresh. A changed
// library kind, source, reading job, directory, asset or publication cohort
// refuses the complete unit before any catalogue/publication/receipt row exists.
func TestIAReadingAcceptFreshGates(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *executor, *iaReadingInventoryRequest){
		"retired_library": func(t *testing.T, w *executor, _ *iaReadingInventoryRequest) {
			wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=7`)
		},
		"stale_source": func(_ *testing.T, _ *executor, q *iaReadingInventoryRequest) { q.source.generation++ },
		"missing_reading_job": func(t *testing.T, w *executor, _ *iaReadingInventoryRequest) {
			wc008NativeExec(t, w, `DELETE FROM inventory_reading_jobs WHERE job_id='reading-run'`)
		},
		"stale_reading_job": func(t *testing.T, w *executor, _ *iaReadingInventoryRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_reading_jobs SET generation=9 WHERE job_id='reading-run'`)
		},
		"stale_directory": func(_ *testing.T, _ *executor, q *iaReadingInventoryRequest) { q.directory.expectedCursor = "9" },
		"changed_asset":   func(_ *testing.T, _ *executor, q *iaReadingInventoryRequest) { q.files[0].size++ },
		"changed_lease": func(t *testing.T, w *executor, q *iaReadingInventoryRequest) {
			q.run.leaseExpiresMS = w.now().UnixMilli()
		},
		"stale_run_revision": func(_ *testing.T, _ *executor, q *iaReadingInventoryRequest) { q.run.revision++ },
		"already_attached_asset": func(t *testing.T, w *executor, _ *iaReadingInventoryRequest) {
			iaReadingSeedItem(t, w, "reading-other-key", 20, "book", true)
		},
		"reused_item_type_mismatch": func(t *testing.T, w *executor, _ *iaReadingInventoryRequest) {
			iaReadingSeedItem(t, w, iaReadingTestItemKey, 20, "magazine", false)
		},
		"reused_item_kind_mismatch": func(t *testing.T, w *executor, _ *iaReadingInventoryRequest) {
			iaReadingSeedItem(t, w, iaReadingTestItemKey, 21, "book", false)
		},
		"retired_item": func(t *testing.T, w *executor, _ *iaReadingInventoryRequest) {
			iaReadingSeedItem(t, w, iaReadingTestItemKey, 20, "book", false)
			wc008NativeExec(t, w, `UPDATE catalog_entities SET retired=1 WHERE id=99`)
		},
		"new_item_without_head": func(_ *testing.T, _ *executor, q *iaReadingInventoryRequest) {
			q.pathHead, q.pathSequence = false, 0
		},
		"reused_item_head_conflict": func(t *testing.T, w *executor, q *iaReadingInventoryRequest) {
			iaReadingSeedItem(t, w, iaReadingTestItemKey, 20, "book", true)
			q.format, q.files[0].relativePath, q.files[0].assetPath = "pdf", iaReadingDirectoryPath(20)+"/novel.pdf", iaReadingRoot(20)+"/"+iaReadingDirectoryPath(20)+"/novel.pdf"
			q.files[0].objectIdentity, q.files[0].revision = "object:pdf", "object-revision:pdf"
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaReadingAcceptFixture(t, 20)
			q := iaReadingAcceptRequest(w, 20, "epub")
			mutate(t, w, &q)
			ref := iaReadingAcceptReference(t, q, 2, 2)
			turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
				t.Fatal("fresh gate", out, err)
			}
			turn.transaction.rollback(nil)
			if got := iaReadingCount(t, w, `SELECT count(*) FROM catalog_entities WHERE id<>99`); got != 0 {
				t.Fatal("gate wrote a catalogue entity", got)
			}
			if got := iaReadingCount(t, w, `SELECT count(*) FROM catalog_asset_links WHERE entity_id<>99`); got != 0 {
				t.Fatal("gate wrote a link", got)
			}
			if got := iaReadingCount(t, w, `SELECT count(*) FROM reading_publications WHERE item_id<>99`); got != 0 {
				t.Fatal("gate wrote a publication", got)
			}
			if got := iaReadingCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 0 {
				t.Fatal("gate wrote a receipt", got)
			}
		})
	}
	t.Run("wrong_library_kind", func(t *testing.T) {
		w, reg, p := iaReadingAcceptFixture(t, 20)
		// A comic source under the book library root passes every other witness;
		// only the closed item-kind/library-kind pair refuses it.
		wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(60,'reading-token-cbz',?,42,7,'','','',0,0,0,7)`, iaReadingRoot(20)+"/"+iaReadingDirectoryPath(20)+"/novel.cbz")
		q := iaReadingAcceptRequest(w, 20, "epub")
		q.itemKind, q.format = 21, "cbz"
		q.files[0].relativePath = iaReadingDirectoryPath(20) + "/novel.cbz"
		q.files[0].assetPath = iaReadingRoot(20) + "/" + q.files[0].relativePath
		q.files[0].objectIdentity, q.files[0].revision = "object:cbz", "object-revision:cbz"
		ref := iaReadingAcceptReference(t, q, 2, 2)
		turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 {
			t.Fatal("library kind", out, err)
		}
		turn.transaction.rollback(nil)
		iaReadingNoEffects(t, w)
	})
}

// A later admitted format of one title is a real alternative publication, never
// a second path head. The first accepted publication keeps its exact head facts.
func TestIAReadingAcceptReusedItemAlternativePublication(t *testing.T) {
	w, reg, p := iaReadingAcceptFixture(t, 20)
	q := iaReadingAcceptRequest(w, 20, "epub")
	first := iaReadingAcceptCommitted(t, w, reg, p, q, 2, 2)
	second := iaReadingNextRequest(t, w, q)
	second.format = "pdf"
	second.files[0].relativePath = iaReadingDirectoryPath(20) + "/novel.pdf"
	second.files[0].assetPath = iaReadingRoot(20) + "/" + second.files[0].relativePath
	second.files[0].objectIdentity, second.files[0].revision = "object:pdf", "object-revision:pdf"
	second.pathHead, second.pathSequence = false, 0
	out := iaReadingAcceptCommitted(t, w, reg, p, second, 3, 3)
	if out.result.facts.resultCount != 1 || out.result.facts.members[0].readingPathHead == nil || *out.result.facts.members[0].readingPathHead != 0 ||
		out.result.facts.members[0].readingPathSequence != nil {
		t.Fatal("alternative member", out.result.facts.members[0])
	}
	if got := iaReadingCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=20`); got != 1 {
		t.Fatal("alternative publication minted a second item", got)
	}
	if got := iaReadingCount(t, w, `SELECT count(*) FROM reading_publications`); got != 2 {
		t.Fatal("publication cohort", got)
	}
	if got := iaReadingCount(t, w, `SELECT count(*) FROM reading_publications WHERE has_reading_path_head=1 AND format='epub' AND path_publisher_sequence=1 AND path_object_id='object:epub' AND path_source_revision='object-revision:epub'`); got != 1 {
		t.Fatal("first head facts changed", got)
	}
	if got := iaReadingCount(t, w, `SELECT count(*) FROM reading_publications WHERE has_reading_path_head=0 AND format='pdf' AND path_publisher_sequence IS NULL AND path_object_id IS NULL AND path_source_revision IS NULL`); got != 1 {
		t.Fatal("alternative head facts invented", got)
	}
	if first.result.facts.members[0].readingPathHead == nil || *first.result.facts.members[0].readingPathHead != 1 {
		t.Fatal("first member head", first.result.facts.members[0])
	}
}

// Current authority precedes every retained read: a forged issuer, a demoted
// owner and a cancelled caller lifetime all deny with zero public output.
func TestIAReadingAcceptAuthorityCells(t *testing.T) {
	t.Run("forged_issuer", func(t *testing.T) {
		w, reg, p := iaReadingAcceptFixture(t, 20)
		q := iaReadingAcceptRequest(w, 20, "epub")
		ref := iaReadingAcceptReference(t, q, 2, 2)
		fake := *p
		fake.witness.issuer.family = "forged"
		turn, out, err := iaReadingAcceptRun(t, w, reg, &fake, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("forged issuer", out, err)
		}
		turn.transaction.rollback(nil)
		iaReadingNoEffects(t, w)
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
				w, reg, p := iaReadingAcceptFixture(t, 20)
				wc008NativeExec(t, w, mutation)
				q := iaReadingAcceptRequest(w, 20, "epub")
				ref := iaReadingAcceptReference(t, q, 2, 2)
				turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
				if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
					t.Fatal("stale authority", out, err)
				}
				turn.transaction.rollback(nil)
				iaReadingNoEffects(t, w)
			})
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, reg, p := iaReadingAcceptFixture(t, 20)
		q := iaReadingAcceptRequest(w, 20, "epub")
		ref := iaReadingAcceptReference(t, q, 2, 2)
		turn, budget := iaReadingAcceptTurn(t, w, reg, q)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := acceptIAReadingInventory(cancelled, turn, p, cancelled, budget, ref, q)
		if err == nil || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("cancelled lifetime", out, err)
		}
		turn.transaction.rollback(nil)
		iaReadingNoEffects(t, w)
	})
}

// Current authority is re-observed after the fresh gates and again after the
// writes. A same-turn revocation injected by the last read or by a catalogue
// write must deny the fresh result and leave no committed effect.
func TestIAReadingAcceptLateAuthorityRechecks(t *testing.T) {
	t.Run("before_writes", func(t *testing.T) {
		w, reg, p := iaReadingAcceptFixture(t, 20)
		state := installIAEntryQueryRevocation(t, w, "FROM catalog_assets")
		q := iaReadingAcceptRequest(w, 20, "epub")
		ref := iaReadingAcceptReference(t, q, 2, 2)
		turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
		if !state.fired || state.err != nil {
			t.Fatal("asset-close mutation did not run", state.err)
		}
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("pre-write recheck released a stale result", out, err)
		}
		turn.transaction.rollback(nil)
		iaReadingNoEffects(t, w)
	})
	t.Run("after_writes", func(t *testing.T) {
		w, reg, p := iaReadingAcceptFixture(t, 20)
		wc008NativeExec(t, w, `CREATE TRIGGER ia_reading_late_revocation AFTER INSERT ON catalog_asset_links BEGIN UPDATE identity_security_fences SET denied=1; END`)
		q := iaReadingAcceptRequest(w, 20, "epub")
		ref := iaReadingAcceptReference(t, q, 2, 2)
		turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("post-write recheck released a stale result", out, err)
		}
		var links int64
		if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_asset_links`).Scan(&links); err != nil || links != 1 {
			t.Fatal("link write not reached before the final recheck", links, err)
		}
		turn.transaction.rollback(nil)
		iaReadingNoEffects(t, w)
	})
}

// A saved cohort must be complete and digest-exact before it is served. A
// missing member, a wrong retained-byte ledger and a changed result digest all
// refuse the replay with zero public output.
func TestIAReadingAcceptReplayRejectsTamperedCohort(t *testing.T) {
	for name, mutate := range map[string]string{
		"missing_member": `DELETE FROM scan_media_result_members WHERE ordinal=0`,
		"member_bytes":   `UPDATE scan_command_receipts SET ia_media_member_bytes=1`,
		"result_digest":  `UPDATE scan_command_receipts SET result_digest=zeroblob(32)`,
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaReadingAcceptFixture(t, 20)
			q := iaReadingAcceptRequest(w, 20, "epub")
			ref := iaReadingAcceptReference(t, q, 2, 2)
			turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
			if err != nil || out.decision != receiptFresh {
				t.Fatal(out, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			wc008NativeExec(t, w, mutate)
			turn, replay, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrResolveRequired) || replay.decision != 0 || replay.digest != (ResultDigest{}) || len(replay.result.facts.members) != 0 {
				t.Fatal("tampered cohort served", replay, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

// A stored search bank/profile that cannot be honored refuses the whole unit
// instead of accepting a catalogue cohort with no search intent.
func TestIAReadingAcceptRefusesUnusableSearchBank(t *testing.T) {
	w, reg, p := iaReadingAcceptFixture(t, 20)
	wc008NativeExec(t, w, `UPDATE search_generations SET unicode_fingerprint='bad'`)
	q := iaReadingAcceptRequest(w, 20, "epub")
	ref := iaReadingAcceptReference(t, q, 2, 2)
	turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
	if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
		t.Fatal("unusable search bank", out, err)
	}
	turn.transaction.rollback(nil)
	iaReadingNoEffects(t, w)
}

// A lost COMMIT reply is never success. The committed file is read back through
// a FRESH read-only connection, never the fenced writer pool.
func TestIAReadingAcceptUnknownCommitIsNotSuccess(t *testing.T) {
	w, reg, p := iaReadingAcceptFixture(t, 20)
	path := iaEpisodePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	q := iaReadingAcceptRequest(w, 20, "epub")
	ref := iaReadingAcceptReference(t, q, 2, 2)
	turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
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
		{`SELECT count(*) FROM catalog_entities WHERE library_id=7 AND kind=20`, 1},
		{`SELECT count(*) FROM reading_publications WHERE format='epub' AND has_reading_path_head=1`, 1},
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='reading_inventory_accept' AND result_count=1`, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='reading_inventory_accept'`, 1},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	if _, err = w.begin(t.Context(), IAAcceptReadingInventory, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

// At or below the durable ACK floor the original is Retired: no result, no
// digest and no new effect, even while the physical rows still exist.
func TestIAReadingAcceptRetiredAfterAck(t *testing.T) {
	w, reg, p := iaReadingAcceptFixture(t, 20)
	q := iaReadingAcceptRequest(w, 20, "epub")
	ref := iaReadingAcceptReference(t, q, 2, 2)
	turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='reading-run'`)
	before := map[string]int64{}
	for _, table := range []string{"catalog_subjects", "catalog_entities", "reading_publications", "scan_command_receipts", "scan_media_result_members", "search_publish_jobs"} {
		before[table] = iaReadingCount(t, w, `SELECT count(*) FROM `+table)
	}
	turn, out, err = iaReadingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptRetired || out.digest != (ResultDigest{}) || out.result.facts.resultCount != 0 || len(out.result.facts.members) != 0 {
		t.Fatal("retired original", out, err)
	}
	turn.transaction.rollback(nil)
	for table, want := range before {
		if got := iaReadingCount(t, w, `SELECT count(*) FROM `+table); got != want {
			t.Fatal("retired replay wrote", table, got, want)
		}
	}
}

// A conflicting digest and a non-contiguous sequence are conflicts, never a
// fresh admission or a new receipt.
func TestIAReadingAcceptRejectsConflictingOriginal(t *testing.T) {
	w, reg, p := iaReadingAcceptFixture(t, 20)
	q := iaReadingAcceptRequest(w, 20, "epub")
	ref := iaReadingAcceptReference(t, q, 2, 2)
	changed := q
	changed.publicationType = "magazine"
	turn, out, err := iaReadingAcceptRun(t, w, reg, p, changed, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("changed request digest", out, err)
	}
	turn.transaction.rollback(nil)
	gap := q
	// The request is internally consistent for sequence 3, but the durable owner
	// is still at sequence 1: a gap, never a fresh admission.
	gap.run.lastSequence, gap.run.acknowledgedSequence = 2, 2
	gapRef := iaReadingAcceptReference(t, gap, 3, 4)
	turn, out, err = iaReadingAcceptRun(t, w, reg, p, gap, gapRef)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("sequence gap", out, err)
	}
	turn.transaction.rollback(nil)
	iaReadingNoEffects(t, w)
}

func TestIAReadingAcceptRollbackLeavesNoEffect(t *testing.T) {
	w, reg, p := iaReadingAcceptFixture(t, 20)
	q := iaReadingAcceptRequest(w, 20, "epub")
	ref := iaReadingAcceptReference(t, q, 2, 2)
	turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	iaReadingNoEffects(t, w)
	if got := iaReadingCount(t, w, `SELECT count(*) FROM inventory_directories WHERE cursor='' AND pages=2`); got != 1 {
		t.Fatal("rollback advanced the directory", got)
	}
}
