package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const (
	iaMissingTestLibraryID  int64 = 7
	iaMissingTestLibraryKey       = "missing-library"
	iaMissingTestSourceID         = "missing-source"
	iaMissingTestRunID            = "missing-run"
	iaMissingTestRoot             = "/media/missing"
	iaMissingTestDirectory        = "Missing"
	iaMissingTestIdentity         = "missing-directory-identity"
	iaMissingTestRevision         = "missing-directory-revision"
	iaMissingTestCursor           = "42"
	iaMissingTestStamp            = "2026-11-08T00:00:00.000000000Z"
	iaFinishTestLibraryID   int64 = 8
	iaFinishTestLibraryKey        = "finish-library"
	iaFinishTestSourceID          = "finish-source"
	iaFinishTestRunID             = "finish-run"
	iaFinishTestRoot              = "/media/finish"
)

// One private W frame, the frozen clock and the exact group C draft subset. The
// fixture seeds only prepared sources/runs, sealed directories and retained
// objects; it is not a qualified population, admission profile or product
// wiring. The skip-veto index overlay is test-only: with skippedIndex false the
// mark path runs without the bounded access and must fail closed.
func iaMissingFinishFixture(t *testing.T) (*executor, *checkpointComponent, *usOwnerAuthorityProof) {
	t.Helper()
	return iaMissingFinishFixtureOverlay(t, true)
}

func iaMissingFinishFixtureOverlay(t *testing.T, skippedIndex bool) (*executor, *checkpointComponent, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	ddlFiles := []string{"testdata/ia-missing-finish-draft.sql"}
	if skippedIndex {
		ddlFiles = append(ddlFiles, "testdata/ia-skipped-directory-index-draft.sql")
	}
	for _, name := range ddlFiles {
		ddl, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
			t.Fatal(name, err)
		}
	}
	expiry := w.now().Add(time.Hour).UnixMilli()
	inc := iaEpisodeBytes(1)
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`, nil},
		{`INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES('missing-library','Missing','movies','/media/missing',1,1,1)`, nil},
		{`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'missing-library','/media/missing')`, nil},
		{`INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES('missing-source','missing-library','Missing','/media/missing','/media/missing','local','missing-root-identity',0,0,'',1,'missing-incarnation',3,'ready',1,'local')`, nil},
		{`INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES('missing-source-2','missing-library','Missing Two','/media/missing/Two','/media/missing/Two','local','missing-root-identity-2',0,0,'',1,'missing-incarnation-2',3,'ready',1,'local')`, nil},
		{`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('missing-run','missing-library','running',0,'')`, nil},
		{`INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,authoritative,inventory_completed_at,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence,discovered,analyzed,warnings) VALUES('missing-run',7,'missing-library','/media/missing',0,?,?,'missing-source',3,'missing-incarnation','missing-root-identity',4,'reconciling',0,?, 'first-import',11,13,?,5,?,2,2,5,3,1)`,
			[]any{iaEpisodeBytes(3), make([]byte, 32), iaMissingTestStamp, expiry, inc}},
		{`INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES('finish-library','Finish','movies','/media/finish',1,1,1)`, nil},
		{`INSERT INTO catalog_libraries(id,library_id,root) VALUES(8,'finish-library','/media/finish')`, nil},
		{`INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES('finish-source','finish-library','Finish','/media/finish','/media/finish','local','finish-root-identity',0,0,'',1,'finish-incarnation',3,'ready',1,'local')`, nil},
		{`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('finish-run','finish-library','running',0,'')`, nil},
		{`INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,authoritative,inventory_completed_at,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence,discovered,analyzed,warnings) VALUES('finish-run',8,'finish-library','/media/finish',0,?,?,'finish-source',3,'finish-incarnation','finish-root-identity',4,'reconciling',0,?, 'first-import',11,13,?,5,?,1,1,4,2,0)`,
			[]any{iaEpisodeBytes(4), make([]byte, 32), iaMissingTestStamp, expiry, inc}},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('missing-run','.','missing-root-directory-identity','missing-root-directory-revision','1','done',1,0)`, nil},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('missing-run','Missing',? ,?,'42','done',1,0)`, []any{iaMissingTestIdentity, iaMissingTestRevision}},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('missing-run','Missing/Sub','missing-sub-directory-identity','missing-sub-directory-revision','7','done',1,0)`, nil},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('missing-run','Missing/Empty','missing-empty-identity','missing-empty-revision','empty-cursor','done',1,0)`, nil},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('finish-run','.','finish-root-directory-identity','finish-root-directory-revision','1','done',1,0)`, nil},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('finish-run','Finish','finish-directory-identity','finish-directory-revision','9','done',1,0)`, nil},
		{`INSERT INTO scan_command_receipts(run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,source_id,source_incarnation,next_cursor,result_count,result_digest,ia_before_key,ia_after_key,ia_phase,ia_authoritative,ia_processed,ia_analyzed,ia_warnings,ia_finished_ms,ia_source_health,ia_verified_directory,ia_verified_identity,ia_verified_revision) VALUES('missing-run',?,1,?,?,'inventory_verify',4,1,'missing-source','missing-incarnation','',0,?,'Missing/Empty','Missing/Empty','reconciling',0,0,0,0,0,'ready','Missing/Empty',?,?)`,
			[]any{inc, iaEpisodeBytes(5), make([]byte, 32), make([]byte, 32), []byte("missing-empty-identity"), []byte("missing-empty-revision")}},
		{`INSERT INTO scan_command_receipts(run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,source_id,source_incarnation,next_cursor,result_count,result_digest,ia_before_key,ia_after_key,ia_phase,ia_authoritative,ia_processed,ia_analyzed,ia_warnings,ia_finished_ms,ia_source_health,ia_verified_directory,ia_verified_identity,ia_verified_revision) VALUES('missing-run',?,2,?,?,'inventory_verify',4,1,'missing-source','missing-incarnation','',0,?,'Missing','Missing','reconciling',0,2,1,1,0,'ready','Missing',?,?)`,
			[]any{inc, iaEpisodeBytes(6), make([]byte, 32), make([]byte, 32), []byte(iaMissingTestIdentity), []byte(iaMissingTestRevision)}},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(41,'missing-asset-1','/media/missing/Missing/One.mkv',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(42,'missing-asset-2','/media/missing/Missing/Two.mkv',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(43,'missing-asset-3','/media/missing/Missing/Seen.mkv',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(44,'missing-asset-4','/media/missing/Missing/Old.mkv',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(45,'missing-asset-5','/media/missing/Missing/Retired.mkv',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(46,'missing-asset-6','/media/missing/Missing/Sub/Nested.mkv',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(47,'missing-asset-7','/media/missing/Missing/Skipped/Film.mkv',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(48,'missing-asset-8','/media/missing/Two/Missing/Foreign.mkv',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:missing:1','missing-source','missing-asset-1','missing-incarnation','Missing/One.mkv','ino:41','object-revision:1','{}',42,7,'','Missing','available')`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:missing:2','missing-source','missing-asset-2','missing-incarnation','Missing/Two.mkv','ino:42','object-revision:1','{}',42,7,'','Missing','available')`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:missing:3','missing-source','missing-asset-3','missing-incarnation','Missing/Seen.mkv','ino:43','object-revision:1','{}',42,7,'missing-run','Missing','available')`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state,missing_since,missing_observations,last_missing_job) VALUES('object:missing:4','missing-source','missing-asset-4','missing-incarnation','Missing/Old.mkv','ino:44','object-revision:1','{}',42,7,'','Missing','missing','2026-11-01T00:00:00.000000000Z',2,'older-run')`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state,retired) VALUES('object:missing:5','missing-source','missing-asset-5','missing-incarnation','Missing/Retired.mkv','ino:45','object-revision:1','{}',42,7,'','Missing','available',1)`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:missing:6','missing-source','missing-asset-6','missing-incarnation','Missing/Sub/Nested.mkv','ino:46','object-revision:1','{}',42,7,'','Missing/Sub','available')`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:missing:7','missing-source','missing-asset-7','missing-incarnation','Missing/Skipped/Film.mkv','ino:47','object-revision:1','{}',42,7,'','Missing/Skipped','available')`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:missing:8','missing-source-2','missing-asset-8','missing-incarnation-2','Missing/Foreign.mkv','ino:48','object-revision:1','{}',42,7,'','Missing','available')`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(51,'finish-asset-1','/media/finish/Finish/One.mkv',42,7,'','','',0,0,0,8)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(52,'finish-asset-2','/media/finish/Finish/Two.mkv',42,7,'','','',0,0,0,8)`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:finish:1','finish-source','finish-asset-1','finish-incarnation','Finish/One.mkv','ino:51','object-revision:1','{}',42,7,'finish-run','Finish','available')`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:finish:2','finish-source','finish-asset-2','finish-incarnation','Finish/Two.mkv','ino:52','object-revision:1','{}',42,7,'finish-run','Finish','available')`, nil},
	} {
		if _, err := w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}
	return w, keeper, proof
}

func iaMissingRegistry(t *testing.T, keeper *checkpointComponent) *iaEntryRegistry {
	t.Helper()
	registry, err := stmttest.New(t, iaMissingDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return &iaEntryRegistry{registry, keeper}
}

func iaFinishRegistry(t *testing.T, keeper *checkpointComponent) *iaEntryRegistry {
	t.Helper()
	registry, err := stmttest.New(t, iaFinishRescanDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return &iaEntryRegistry{registry, keeper}
}

func iaMissingAcceptRequest(w *executor) iaMissingRequest {
	return iaMissingRequest{
		scope:  inventoryScope{runID: iaMissingTestRunID, libraryKey: iaMissingTestLibraryKey, libraryID: iaMissingTestLibraryID},
		source: inventorySource{id: iaMissingTestSourceID, incarnation: "missing-incarnation", rootIdentity: "missing-root-identity", resolvedRoot: []byte(iaMissingTestRoot), generation: 3},
		run: inventoryRun{revision: 5, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13, leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(),
			lastSequence: 2, acknowledgedSequence: 2, phase: "reconciling", scanMode: "first-import"},
		directory:        iaMissingSeal{relativePath: iaMissingTestDirectory, identity: iaMissingTestIdentity, revision: iaMissingTestRevision, cursor: iaMissingTestCursor, state: "done", pages: 1},
		coverageSequence: 2,
		pinsResolved:     true,
	}
}

func iaMissingAcceptReference(t *testing.T, q iaMissingRequest, sequence int64, operation byte) ReceiptReference {
	t.Helper()
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
	command, err := newIAMissingCommand(id, q)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: command.digest}
}

func iaMissingAcceptRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaMissingRequest, ref ReceiptReference) (*commandTurn, iaMissingAcceptOutcome, error) {
	t.Helper()
	turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAMarkMissing, action: iaEntryApply, libraryID: q.scope.libraryID, libraryKey: q.scope.libraryKey})
	out, err := acceptIAMissing(t.Context(), turn, p, t.Context(), budget, ref, q)
	return turn, out, err
}

// iaMissingTxCount reads through the open writer transaction itself. The replay
// frame still holds the single writer connection, so a pool read here would wait
// on itself instead of proving that no new receipt was minted.
func iaMissingTxCount(t *testing.T, turn *commandTurn, query string) int64 {
	t.Helper()
	var n int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), query).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

// A tampered saved original is a test-only counterfactual: some receipt shapes
// are impossible through the schema CHECKs, so enforcement is disabled on one
// dedicated connection for the direct UPDATE and restored before it returns to
// the pool. A writer turn never sees the relaxed connection.
func execIATamperUnchecked(t *testing.T, w *executor, query string) {
	t.Helper()
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), `PRAGMA ignore_check_constraints=ON`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.ExecContext(t.Context(), `PRAGMA ignore_check_constraints=OFF`); err != nil {
			t.Fatal(err)
		}
	}()
	if _, err := conn.ExecContext(t.Context(), query); err != nil {
		t.Fatal(query, err)
	}
}

// A refused request writes no receipt, does not advance the run and leaves the
// retained objects untouched.
func iaMissingNoEffects(t *testing.T, w *executor) {
	t.Helper()
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 2 {
		t.Fatal("refused request wrote a receipt", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaMissingTestRunID); got != 5 {
		t.Fatal("refused request advanced the run", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_objects WHERE id=? AND state='available'`, "object:missing:1"); got != 1 {
		t.Fatal("refused request marked an object", got)
	}
}

// One fresh verified window marks exactly its unseen available children, records
// the exact disposition, makes the run absence-authoritative and replays the
// same result after live source/lease/object changes without a new effect.
func TestIAMissingMarksVerifiedWindowAndReplay(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaMissingRegistry(t, keeper)
	q := iaMissingAcceptRequest(w)
	ref := iaMissingAcceptReference(t, q, 3, 2)
	turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.digest == (ResultDigest{}) {
		t.Fatal("fresh missing window", out, err)
	}
	facts := out.result.facts
	if facts.acceptedRevision != 6 || facts.appliedMS != w.now().UnixMilli() || facts.phase != "reconciling" ||
		facts.sourceHealth != "ready" || facts.beforeKey != "" || facts.afterKey != "object:missing:2" ||
		facts.processed != 2 || facts.analyzed != 2 || facts.warnings != 0 || facts.resultVersion != 1 ||
		len(facts.members) != 2 || facts.members[0].objectID != "object:missing:1" || facts.members[1].objectID != "object:missing:2" {
		t.Fatal("missing facts", facts)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		args  []any
		want  int64
	}{
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_missing' AND result_count=2 AND ia_phase='reconciling' AND ia_authoritative=1 AND ia_processed=2 AND ia_analyzed=2 AND ia_warnings=0 AND ia_before_key='' AND ia_after_key='object:missing:2'`, nil, 1},
		{`SELECT count(*) FROM scan_reconcile_members WHERE command_kind='inventory_missing' AND disposition='missing' AND before_path=after_path AND before_revision=after_revision AND prior_object_id IS NULL AND ordinal IN(0,1)`, nil, 2},
		{`SELECT count(*) FROM inventory_runs WHERE job_id='missing-run' AND job_revision=6 AND last_command_sequence=3 AND acknowledged_command_sequence=2 AND authoritative=1 AND phase='reconciling'`, nil, 1},
		{`SELECT count(*) FROM inventory_objects WHERE id IN('object:missing:1','object:missing:2') AND state='missing' AND missing_since=? AND missing_observations=1 AND last_missing_job='missing-run'`, []any{iaMissingTimestamp(w.now())}, 2},
		{`SELECT count(*) FROM inventory_objects WHERE id='object:missing:3' AND state='available' AND seen_job='missing-run'`, nil, 1},
		{`SELECT count(*) FROM inventory_objects WHERE id='object:missing:4' AND state='missing' AND missing_observations=2 AND last_missing_job='older-run'`, nil, 1},
		{`SELECT count(*) FROM inventory_objects WHERE id='object:missing:5' AND state='available' AND retired=1`, nil, 1},
		{`SELECT count(*) FROM inventory_objects WHERE id='object:missing:6' AND state='available'`, nil, 1},
	} {
		if got := iaEpisodeCount(t, w, check.query, check.args...); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_reconcile_members WHERE command_kind='inventory_missing' AND disposition='missing' AND before_path=after_path AND before_revision=after_revision AND prior_object_id IS NULL AND ordinal IN(0,1)`); got != 2 {
		t.Fatal("missing member cohort", got)
	}
	// Live source/lease/object changes never edit or filter the saved result.
	wc008NativeExec(t, w, `UPDATE library_sources SET generation=9,root='/moved',configured_root='/moved' WHERE id='missing-source'`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET job_revision=99,job_lease_expires_ms=1 WHERE job_id='missing-run'`)
	wc008NativeExec(t, w, `UPDATE inventory_objects SET state='available' WHERE id='object:missing:1'`)
	turn, replay, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest ||
		replay.result.facts.afterKey != facts.afterKey || replay.result.facts.processed != facts.processed ||
		replay.result.facts.acceptedRevision != facts.acceptedRevision || len(replay.result.facts.members) != 2 {
		t.Fatal("saved missing replay", replay, err)
	}
	if got := iaMissingTxCount(t, turn, `SELECT count(*) FROM scan_command_receipts`); got != 3 {
		t.Fatal("replay minted a receipt", got)
	}
	turn.transaction.rollback(nil)
}

// Offline, seen, already-missing, retired and out-of-directory objects are
// distinct states. Only the unseen available direct child is marked.
func TestIAMissingObjectStatesDistinct(t *testing.T) {
	t.Run("offline_source", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaMissingRegistry(t, keeper)
		wc008NativeExec(t, w, `UPDATE library_sources SET health='offline' WHERE id='missing-source'`)
		q := iaMissingAcceptRequest(w)
		ref := iaMissingAcceptReference(t, q, 3, 2)
		turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 {
			t.Fatal("offline source accepted", out, err)
		}
		turn.transaction.rollback(nil)
		iaMissingNoEffects(t, w)
	})
	t.Run("existing_states", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaMissingRegistry(t, keeper)
		q := iaMissingAcceptRequest(w)
		ref := iaMissingAcceptReference(t, q, 3, 2)
		turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || len(out.result.facts.members) != 2 {
			t.Fatal("states window", out, err)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_objects WHERE id='object:missing:4' AND state='missing' AND missing_since='2026-11-01T00:00:00.000000000Z' AND missing_observations=2 AND last_missing_job='older-run'`); got != 1 {
			t.Fatal("already-missing object rewritten", got)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_objects WHERE id='object:missing:3' AND state='available' AND last_missing_job=''`); got != 1 {
			t.Fatal("seen object marked missing", got)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_objects WHERE id IN('object:missing:5','object:missing:6') AND state='available' AND last_missing_job=''`); got != 2 {
			t.Fatal("retired or nested object marked missing", got)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_objects WHERE id='object:missing:8' AND state='available' AND seen_job=''`); got != 1 {
			t.Fatal("another source's unseen object marked missing", got)
		}
	})
}

// A verified directory with no eligible child records a truthful zero-member
// window and keeps the cursor. It is not an EOF alias for a wider absence.
func TestIAMissingZeroMemberWindow(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaMissingRegistry(t, keeper)
	q := iaMissingAcceptRequest(w)
	q.directory = iaMissingSeal{relativePath: "Missing/Empty", identity: "missing-empty-identity", revision: "missing-empty-revision", cursor: "empty-cursor", state: "done", pages: 1}
	q.coverageSequence = 1
	q.beforeKey = "object:missing:0"
	ref := iaMissingAcceptReference(t, q, 3, 2)
	turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.processed != 0 ||
		out.result.facts.analyzed != 0 || out.result.facts.resultCount != 0 ||
		out.result.facts.beforeKey != "object:missing:0" || out.result.facts.afterKey != "object:missing:0" {
		t.Fatal("zero-member window", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_missing' AND result_count=0 AND ia_processed=0 AND ia_analyzed=0 AND ia_after_key='object:missing:0'`); got != 1 {
		t.Fatal("zero-member receipt", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_reconcile_members WHERE command_kind='inventory_missing'`); got != 0 {
		t.Fatal("zero-member window wrote a child row", got)
	}
}

// Absence needs IA-18's exact durable coverage receipt: a done directory with an
// EOF cursor alone, a coverage receipt for another directory or a mismatched
// seal can never authorize a mark.
func TestIAMissingRequiresVerifiedCoverage(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *executor, *iaMissingRequest){
		"no_coverage_receipt": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `DELETE FROM scan_command_receipts WHERE command_sequence=2`)
		},
		"coverage_for_other_directory": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE scan_command_receipts SET ia_verified_directory='Missing/Sub',ia_verified_identity=CAST('missing-sub-directory-identity' AS BLOB),ia_verified_revision=CAST('missing-sub-directory-revision' AS BLOB) WHERE command_sequence=2`)
		},
		"coverage_seal_drift": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE scan_command_receipts SET ia_verified_revision=CAST('older-revision' AS BLOB) WHERE command_sequence=2`)
		},
		"coverage_not_verify": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE scan_command_receipts SET command_kind='inventory_reconcile',ia_verified_directory=NULL,ia_verified_identity=NULL,ia_verified_revision=NULL WHERE command_sequence=2`)
		},
		"coverage_after_command": func(_ *testing.T, _ *executor, q *iaMissingRequest) { q.coverageSequence = 3 },
		"coverage_unfinished": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE scan_command_receipts SET ia_phase='verifying' WHERE command_sequence=2`)
		},
		"coverage_counter_drift": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE scan_command_receipts SET ia_analyzed=0 WHERE command_sequence=2`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, p := iaMissingFinishFixture(t)
			reg := iaMissingRegistry(t, keeper)
			q := iaMissingAcceptRequest(w)
			ref := iaMissingAcceptReference(t, q, 3, 2)
			mutate(t, w, &q)
			turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
			if name == "coverage_after_command" {
				if !errors.Is(err, ErrInvalid) || out.decision != 0 {
					t.Fatal("future coverage sequence accepted", out, err)
				}
				return
			}
			if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) {
				t.Fatal("unverified window accepted", out, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

// A window is unsafe when its frontier is partial, unreadable or refused, when
// the resolved source pins are absent, or when a skipped listing would create a
// first absence.
func TestIAMissingRefusesUnsafeWindows(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *executor, *iaMissingRequest){
		"partial_subtree": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('missing-run','Missing/Partial','partial-identity','partial-revision','1','pending',1,0)`)
		},
		"unreadable_subtree": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('missing-run','Missing/Blocked','blocked-identity','blocked-revision','1','blocked',1,0)`)
		},
		"protected_issue": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `INSERT INTO inventory_scan_issues(job_id,relative_path,reason,line,protect,warning) VALUES('missing-run','Missing/Secret','unreadable_entry',0,1,1)`)
		},
		"unstable_identity": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET identity='other-identity' WHERE job_id='missing-run' AND relative_path='Missing'`)
		},
		"unstable_revision": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET revision='other-revision' WHERE job_id='missing-run' AND relative_path='Missing'`)
		},
		"changed_cursor": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET cursor='43' WHERE job_id='missing-run' AND relative_path='Missing'`)
		},
		"not_done_directory": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET state='blocked' WHERE job_id='missing-run' AND relative_path='Missing'`)
		},
		"unresolved_mp10_pins": func(_ *testing.T, _ *executor, q *iaMissingRequest) { q.pinsResolved = false },
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, p := iaMissingFinishFixture(t)
			reg := iaMissingRegistry(t, keeper)
			q := iaMissingAcceptRequest(w)
			mutate(t, w, &q)
			ref := iaMissingAcceptReference(t, q, 3, 2)
			turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
			if name == "unresolved_mp10_pins" {
				if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, iaCoveragePinsUnresolved) || out.decision != 0 {
					t.Fatal("unresolved pins accepted", out, err)
				}
			} else if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
				t.Fatal("unsafe window accepted", out, err)
			}
			turn.transaction.rollback(nil)
			iaMissingNoEffects(t, w)
		})
	}
	t.Run("selected_skip_prepared", func(t *testing.T) {
		w, _, _ := iaMissingFinishFixture(t)
		q := iaMissingAcceptRequest(w)
		q.directory = iaMissingSeal{relativePath: "Missing/Skipped", identity: "missing-skipped-identity", revision: "missing-skipped-revision", cursor: "skipped-cursor", state: "done", pages: 1, skipped: 1}
		q.coverageSequence = 1
		if _, err := newIAMissingCommand(CommandIdentity{Incarnation: [16]byte{1}, Sequence: 3, OperationID: [16]byte{2}}, q); !errors.Is(err, ErrInvalid) {
			t.Fatal("prepared skip accepted", err)
		}
	})
}

// The skipped-directory veto is re-read natively in the mark transaction: a skip
// on the selected directory, the root or any descendant blocks new missing effects,
// while skips outside the exact half-open bound or under another job do not.
func TestIAMissingSkippedSubtreeVeto(t *testing.T) {
	seedDir := func(t *testing.T, w *executor, job, relativePath string, skipped int64) {
		t.Helper()
		wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES(?,?,?,?,?,'done',?,?)`,
			job, relativePath, relativePath+"-identity", relativePath+"-revision", "1", skipped, skipped)
	}
	rootSeal := func() *iaMissingSeal {
		return &iaMissingSeal{relativePath: ".", identity: "missing-root-directory-identity",
			revision: "missing-root-directory-revision", cursor: "1", state: "done", pages: 1}
	}
	for name, tc := range map[string]struct {
		directory  *iaMissingSeal
		coverage   int64
		beforeKey  string
		seed       func(*testing.T, *executor)
		fresh      bool
		expectRows int
	}{
		"selected_native_skip": {seed: func(t *testing.T, w *executor) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET skipped=1 WHERE job_id='missing-run' AND relative_path='Missing'`)
		}},
		"root_selected_skip": {directory: rootSeal(), seed: func(t *testing.T, w *executor) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET skipped=1 WHERE job_id='missing-run' AND relative_path='.'`)
		}},
		"first_descendant_skip": {seed: func(t *testing.T, w *executor) { seedDir(t, w, "missing-run", "Missing/000", 1) }},
		"end_descendant_skip":   {seed: func(t *testing.T, w *executor) { seedDir(t, w, "missing-run", "Missing/zzz", 1) }},
		"deep_descendant_skip":  {seed: func(t *testing.T, w *executor) { seedDir(t, w, "missing-run", "Missing/Sub/Deep/Leaf", 1) }},
		"sibling_prefix_skip": {fresh: true, expectRows: 2, seed: func(t *testing.T, w *executor) {
			seedDir(t, w, "missing-run", "MissingTwo", 1)
			seedDir(t, w, "missing-run", "Missings", 1)
		}},
		"out_of_subtree_skip": {fresh: true, expectRows: 2, seed: func(t *testing.T, w *executor) { seedDir(t, w, "missing-run", "Other/Skip", 1) }},
		"other_job_skip":      {fresh: true, expectRows: 2, seed: func(t *testing.T, w *executor) { seedDir(t, w, "finish-run", "Finish/Skipped", 1) }},
		"zero_skip_empty": {fresh: true, expectRows: 0, coverage: 1, beforeKey: "object:missing:0",
			directory: &iaMissingSeal{relativePath: "Missing/Empty", identity: "missing-empty-identity",
				revision: "missing-empty-revision", cursor: "empty-cursor", state: "done", pages: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, p := iaMissingFinishFixture(t)
			reg := iaMissingRegistry(t, keeper)
			q := iaMissingAcceptRequest(w)
			if tc.directory != nil {
				q.directory = *tc.directory
				if tc.coverage != 0 {
					q.coverageSequence = tc.coverage
				}
				q.beforeKey = tc.beforeKey
			}
			if tc.seed != nil {
				tc.seed(t, w)
			}
			ref := iaMissingAcceptReference(t, q, 3, 2)
			turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
			if tc.fresh {
				if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != int64(tc.expectRows) ||
					len(out.result.facts.members) != tc.expectRows {
					t.Fatal("skip-free window refused", out, err)
				}
				turn.transaction.rollback(nil)
				return
			}
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
				t.Fatal("skip authorized a missing mark", out, err)
			}
			turn.transaction.rollback(nil)
			iaMissingNoEffects(t, w)
		})
	}
	// A prepared seal that already carries a skip is not a valid absence proof.
	w, _, _ := iaMissingFinishFixture(t)
	q := iaMissingAcceptRequest(w)
	q.directory.skipped = 1
	if _, err := newIAMissingCommand(CommandIdentity{Incarnation: [16]byte{1}, Sequence: 3, OperationID: [16]byte{2}}, q); !errors.Is(err, ErrInvalid) {
		t.Fatal("prepared skip accepted", err)
	}
}

// A skip that appears after preparation, seal and coverage but before the mark is
// caught by the same-turn native proof: the whole unit rolls back with zero
// missing effects.
func TestIAMissingLateSkipBlocksMark(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaMissingRegistry(t, keeper)
	state := installIASkipMutation(t, w, "FROM inventory_objects",
		`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('missing-run','Missing/Late','late-identity','late-revision','1','done',1,1)`)
	q := iaMissingAcceptRequest(w)
	ref := iaMissingAcceptReference(t, q, 3, 2)
	turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
	if !state.fired || state.err != nil {
		t.Fatal("late skip mutation did not run", state.err)
	}
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
		t.Fatal("late skip authorized a missing mark", out, err)
	}
	turn.transaction.rollback(nil)
	iaMissingNoEffects(t, w)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_objects WHERE state='missing' AND last_missing_job='missing-run'`); got != 0 {
		t.Fatal("late skip left missing effects", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_reconcile_members WHERE command_kind='inventory_missing'`); got != 0 {
		t.Fatal("late skip left member rows", got)
	}
}

// The same base draft without the test-only bounded index is a typed hold, never a
// successful empty window and never a raw crash.
func TestIAMissingSkippedAccessUnavailableHolds(t *testing.T) {
	w, keeper, p := iaMissingFinishFixtureOverlay(t, false)
	reg := iaMissingRegistry(t, keeper)
	q := iaMissingAcceptRequest(w)
	ref := iaMissingAcceptReference(t, q, 3, 2)
	turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
	if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, iaMissingSkippedUnavailable) ||
		out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
		t.Fatal("missing bounded access did not hold", out, err)
	}
	turn.transaction.rollback(nil)
	iaMissingNoEffects(t, w)
}

// Native fault injection for the late-skip cell: after the window rows close, the
// same writer transaction inserts a skipped descendant, so only the in-turn proof
// can see it.
type iaMissingSkipMutation struct {
	fired bool
	err   error
}
type iaMissingSkipMutationConn struct {
	writerDriverConn
	query    string
	mutation string
	state    *iaMissingSkipMutation
}

func (c *iaMissingSkipMutationConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, query, args)
	if err != nil || !strings.Contains(query, c.query) {
		return rows, err
	}
	return &receiptCloseRows{Rows: rows, hook: func() {
		if c.state.fired {
			return
		}
		c.state.fired = true
		_, c.state.err = c.writerDriverConn.ExecContext(ctx, c.mutation, nil)
	}}, nil
}
func installIASkipMutation(t *testing.T, w *executor, query, mutation string) *iaMissingSkipMutation {
	t.Helper()
	var sequence int
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	state := &iaMissingSkipMutation{}
	w.openPool(connector, func(conn driver.Conn) driver.Conn {
		return &iaMissingSkipMutationConn{writerDriverConn: conn.(writerDriverConn), query: query, mutation: mutation, state: state}
	})
	return state
}

// One window changes at most eight rows; the ninth waits for the next keyset
// page and the cursor advances exactly to the eighth.
func TestIAMissingBoundsWindow(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaMissingRegistry(t, keeper)
	for i := 10; i <= 17; i++ {
		wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,42,7,'','','',0,0,0,7)`, 100+i, "missing-bound-token-"+string(rune('a'+i)), "/media/missing/Missing/Bound"+string(rune('a'+i))+".mkv")
		wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES(?,?,?,?,?,?,?,?,42,7,'','Missing','available')`,
			"object:missing:bound:"+string(rune('a'+i)), "missing-source", "missing-bound-token-"+string(rune('a'+i)), "missing-incarnation",
			"Missing/Bound"+string(rune('a'+i))+".mkv", "ino:"+string(rune('0'+i)), "object-revision:1", "{}")
	}
	// Eight more objects join the two seeded children: ten eligible in all.
	q := iaMissingAcceptRequest(w)
	ref := iaMissingAcceptReference(t, q, 3, 2)
	turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || len(out.result.facts.members) != iaMissingMaxRows {
		t.Fatal("bounded window", out, err, len(out.result.facts.members))
	}
	if out.result.facts.afterKey != out.result.facts.members[iaMissingMaxRows-1].objectID {
		t.Fatal("bounded cursor", out.result.facts.afterKey)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_objects WHERE state='missing' AND last_missing_job='missing-run'`); got != iaMissingMaxRows {
		t.Fatal("window changed too many rows", got)
	}
	next := q
	next.run = inventoryRun{revision: 6, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13, leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(),
		lastSequence: 3, acknowledgedSequence: 3, phase: "reconciling", scanMode: "first-import"}
	next.beforeKey = out.result.facts.afterKey
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=3 WHERE job_id='missing-run'`)
	nextRef := iaMissingAcceptReference(t, next, 4, 3)
	turn, out2, err := iaMissingAcceptRun(t, w, reg, p, next, nextRef)
	if err != nil || out2.decision != receiptFresh || out2.result.facts.resultCount != 2 {
		t.Fatal("second page", out2, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_objects WHERE state='missing' AND last_missing_job='missing-run'`); got != 10 {
		t.Fatal("all eligible rows marked", got)
	}
}

// One window also stays inside the stated byte bound even when corrupt stored
// paths would otherwise inflate it: eight huge children refuse rather than
// change an unbounded absence set.
func TestIAMissingWindowByteCap(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaMissingRegistry(t, keeper)
	huge := strings.Repeat("a", 40000)
	for i := 0; i < 8; i++ {
		name := string(rune('a' + i))
		wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,42,7,'','','',0,0,0,7)`, 200+i, "missing-big-token-"+name, "/media/missing/Missing/Big"+name+".mkv")
		wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES(?,?,?,?,?,?,?,?,42,7,'','Missing','available')`,
			"object:missing:0"+name, "missing-source", "missing-big-token-"+name, "missing-incarnation",
			"Missing/"+huge+name+".mkv", "ino:big-"+name, "object-revision:1", "{}")
	}
	q := iaMissingAcceptRequest(w)
	ref := iaMissingAcceptReference(t, q, 3, 2)
	turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
	if !errors.Is(err, ErrCap) || out.decision != 0 || len(out.result.facts.members) != 0 {
		t.Fatal("over-bound window accepted", out, err)
	}
	turn.transaction.rollback(nil)
	iaMissingNoEffects(t, w)
}

// Every fresh witness is re-proven after the receipt is Fresh. A changed
// library, source, run, lease or closed traversal refuses the whole unit.
func TestIAMissingFreshGates(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *executor, *iaMissingRequest){
		"retired_library": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=7`)
		},
		"stale_source": func(_ *testing.T, _ *executor, q *iaMissingRequest) { q.source.generation++ },
		"changed_lease": func(t *testing.T, w *executor, q *iaMissingRequest) {
			q.run.leaseExpiresMS = w.now().UnixMilli()
		},
		"stale_run_revision": func(_ *testing.T, _ *executor, q *iaMissingRequest) { q.run.revision++ },
		"changed_run_phase": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_runs SET phase='inventory' WHERE job_id='missing-run'`)
		},
		"finished_run": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_runs SET finished_at='2026-11-08T00:00:00.000000000Z' WHERE job_id='missing-run'`)
		},
		"open_traversal": func(t *testing.T, w *executor, _ *iaMissingRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_runs SET inventory_completed_at='' WHERE job_id='missing-run'`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, p := iaMissingFinishFixture(t)
			reg := iaMissingRegistry(t, keeper)
			q := iaMissingAcceptRequest(w)
			mutate(t, w, &q)
			ref := iaMissingAcceptReference(t, q, 3, 2)
			turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
				t.Fatal("fresh gate", out, err)
			}
			turn.transaction.rollback(nil)
			iaMissingNoEffects(t, w)
		})
	}
}

// Current authority precedes every retained read: a forged issuer, a demoted
// owner and a cancelled caller lifetime all deny with zero public output.
func TestIAMissingAuthorityCells(t *testing.T) {
	t.Run("forged_issuer", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaMissingRegistry(t, keeper)
		q := iaMissingAcceptRequest(w)
		ref := iaMissingAcceptReference(t, q, 3, 2)
		fake := *p
		fake.witness.issuer.family = "forged"
		turn, out, err := iaMissingAcceptRun(t, w, reg, &fake, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("forged issuer", out, err)
		}
		turn.transaction.rollback(nil)
		iaMissingNoEffects(t, w)
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
				w, keeper, p := iaMissingFinishFixture(t)
				reg := iaMissingRegistry(t, keeper)
				wc008NativeExec(t, w, mutation)
				q := iaMissingAcceptRequest(w)
				ref := iaMissingAcceptReference(t, q, 3, 2)
				turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
				if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
					t.Fatal("stale authority", out, err)
				}
				turn.transaction.rollback(nil)
				iaMissingNoEffects(t, w)
			})
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaMissingRegistry(t, keeper)
		q := iaMissingAcceptRequest(w)
		ref := iaMissingAcceptReference(t, q, 3, 2)
		turn, budget := iaEntryTurn(t, w, reg, iaEntryRequest{purpose: IAMarkMissing, action: iaEntryApply, libraryID: q.scope.libraryID, libraryKey: q.scope.libraryKey})
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := acceptIAMissing(cancelled, turn, p, cancelled, budget, ref, q)
		if err == nil || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("cancelled lifetime", out, err)
		}
		turn.transaction.rollback(nil)
		iaMissingNoEffects(t, w)
	})
}

// Current authority is re-observed after the object-window read and again after
// the receipt write. A same-turn revocation must deny the fresh result.
func TestIAMissingLateAuthorityRechecks(t *testing.T) {
	t.Run("before_writes", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaMissingRegistry(t, keeper)
		state := installIAEntryQueryRevocation(t, w, "FROM inventory_objects")
		q := iaMissingAcceptRequest(w)
		ref := iaMissingAcceptReference(t, q, 3, 2)
		turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
		if !state.fired || state.err != nil {
			t.Fatal("object-window mutation did not run", state.err)
		}
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("pre-write recheck released a stale result", out, err)
		}
		turn.transaction.rollback(nil)
		iaMissingNoEffects(t, w)
	})
	t.Run("after_writes", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaMissingRegistry(t, keeper)
		wc008NativeExec(t, w, `CREATE TRIGGER ia_missing_late_revocation AFTER INSERT ON scan_command_receipts BEGIN UPDATE identity_security_fences SET denied=1; END`)
		q := iaMissingAcceptRequest(w)
		ref := iaMissingAcceptReference(t, q, 3, 2)
		turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("post-write recheck released a stale result", out, err)
		}
		var written int64
		if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_missing'`).Scan(&written); err != nil || written != 1 {
			t.Fatal("receipt write not reached before the final recheck", written, err)
		}
		turn.transaction.rollback(nil)
		iaMissingNoEffects(t, w)
	})
}

// A saved receipt must be exact before it is served: the keyset window, the
// counter invariant, the member cohort and the result digest are all checked.
func TestIAMissingReplayRejectsTamperedReceipt(t *testing.T) {
	for name, mutate := range map[string]string{
		"result_digest":  `UPDATE scan_command_receipts SET result_digest=zeroblob(32)`,
		"after_key":      `UPDATE scan_command_receipts SET ia_after_key='object:missing:9'`,
		"processed":      `UPDATE scan_command_receipts SET ia_processed=9`,
		"authoritative":  `UPDATE scan_command_receipts SET ia_authoritative=0`,
		"finished_ms":    `UPDATE scan_command_receipts SET ia_finished_ms=1`,
		"missing_member": `DELETE FROM scan_reconcile_members WHERE command_kind='inventory_missing' AND ordinal=1`,
		"member_path":    `UPDATE scan_reconcile_members SET before_path='Missing/Other.mkv' WHERE command_kind='inventory_missing' AND ordinal=0`,
	} {
		unchecked := name == "authoritative" || name == "finished_ms"
		t.Run(name, func(t *testing.T) {
			w, keeper, p := iaMissingFinishFixture(t)
			reg := iaMissingRegistry(t, keeper)
			q := iaMissingAcceptRequest(w)
			ref := iaMissingAcceptReference(t, q, 3, 2)
			turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
			if err != nil || out.decision != receiptFresh {
				t.Fatal(out, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			if unchecked {
				execIATamperUnchecked(t, w, mutate)
			} else {
				wc008NativeExec(t, w, mutate)
			}
			turn, replay, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
			if err == nil || replay.decision != 0 || replay.digest != (ResultDigest{}) || len(replay.result.facts.members) != 0 {
				t.Fatal("tampered receipt served", replay, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

// A lost COMMIT reply is never success. The committed file is read back through
// a FRESH read-only connection, never the fenced writer pool.
func TestIAMissingUnknownCommitIsNotSuccess(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaMissingRegistry(t, keeper)
	path := iaEpisodePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	q := iaMissingAcceptRequest(w)
	ref := iaMissingAcceptReference(t, q, 3, 2)
	turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.processed != 2 {
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
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_missing' AND result_count=2 AND ia_after_key='object:missing:2'`, 1},
		{`SELECT job_revision FROM inventory_runs WHERE job_id='missing-run'`, 6},
		{`SELECT count(*) FROM inventory_objects WHERE id IN('object:missing:1','object:missing:2') AND state='missing'`, 2},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	if _, err = w.begin(t.Context(), IAMarkMissing, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

// At or below the durable ACK floor the original is Retired: no result, no
// digest and no new effect, even while the physical receipt still exists.
func TestIAMissingRetiredAfterAck(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaMissingRegistry(t, keeper)
	q := iaMissingAcceptRequest(w)
	ref := iaMissingAcceptReference(t, q, 3, 2)
	turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=3 WHERE job_id='missing-run'`)
	before := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`)
	beforeRevision := iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaMissingTestRunID)
	turn, out, err = iaMissingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptRetired || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
		t.Fatal("retired original", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != before {
		t.Fatal("retired replay wrote a receipt", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaMissingTestRunID); got != beforeRevision {
		t.Fatal("retired replay advanced the run", got)
	}
}

// A conflicting digest, a non-contiguous sequence and malformed original bounds
// are conflicts or invalid input, never a fresh admission or a new receipt.
func TestIAMissingRejectsConflictingOriginal(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaMissingRegistry(t, keeper)
	q := iaMissingAcceptRequest(w)
	ref := iaMissingAcceptReference(t, q, 3, 2)
	changed := q
	changed.beforeKey = "object:missing:0"
	turn, out, err := iaMissingAcceptRun(t, w, reg, p, changed, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("changed request digest", out, err)
	}
	turn.transaction.rollback(nil)
	gap := q
	gap.run.lastSequence, gap.run.acknowledgedSequence = 3, 3
	gapRef := iaMissingAcceptReference(t, gap, 4, 4)
	turn, out, err = iaMissingAcceptRun(t, w, reg, p, gap, gapRef)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("sequence gap", out, err)
	}
	turn.transaction.rollback(nil)
	// The seal must be an exact done row with a native identity, revision and
	// relative path.
	for name, corrupt := range map[string]func(*iaMissingRequest){
		"over_bound_identity": func(q *iaMissingRequest) { q.directory.identity = strings.Repeat("i", iaMissingMaxKeyBytes+1) },
		"pending_state":       func(q *iaMissingRequest) { q.directory.state = "pending" },
		"escaping_path":       func(q *iaMissingRequest) { q.directory.relativePath = "../escape" },
		"skipped_over_pages":  func(q *iaMissingRequest) { q.directory.skipped = 2 },
		"control_cursor":      func(q *iaMissingRequest) { q.beforeKey = "bad\x01key" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := q
			corrupt(&bad)
			if _, err = newIAMissingCommand(CommandIdentity{Incarnation: [16]byte{1}, Sequence: 3, OperationID: [16]byte{2}}, bad); !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed original accepted", err)
			}
		})
	}
	iaMissingNoEffects(t, w)
}

func TestIAMissingRollbackLeavesNoEffect(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaMissingRegistry(t, keeper)
	q := iaMissingAcceptRequest(w)
	ref := iaMissingAcceptReference(t, q, 3, 2)
	turn, out, err := iaMissingAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	iaMissingNoEffects(t, w)
}

func TestIAMissingDescriptor(t *testing.T) {
	got, err := Describe(IAMarkMissing)
	want := Descriptor{ID: 122, Name: "ia_mark_missing", Durability: Normal}
	if err != nil || got != want {
		t.Fatalf("Describe(122) = %+v, %v; want %+v", got, err, want)
	}
	if kind, err := iaEntryCommandKind(IAMarkMissing); err != nil || kind != "inventory_missing" {
		t.Fatalf("missing kind = %q, %v", kind, err)
	}
	if class, err := commandClass(IAMarkMissing); err != nil || class != governor.Background {
		t.Fatalf("missing class = %v, %v", class, err)
	}
}
