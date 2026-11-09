package writer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func iaFinishAcceptRequest(w *executor) iaFinishRescanRequest {
	return iaFinishRescanRequest{
		scope:  inventoryScope{runID: iaFinishTestRunID, libraryKey: iaFinishTestLibraryKey, libraryID: iaFinishTestLibraryID},
		source: inventorySource{id: iaFinishTestSourceID, incarnation: "finish-incarnation", rootIdentity: "finish-root-identity", resolvedRoot: []byte(iaFinishTestRoot), generation: 3},
		run: inventoryRun{revision: 5, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13, leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(),
			lastSequence: 1, acknowledgedSequence: 1, phase: "reconciling", scanMode: "first-import"},
		outcome:      iaFinishComplete,
		pinsResolved: true,
	}
}

func iaFinishAcceptReference(t *testing.T, q iaFinishRescanRequest, sequence int64, operation byte) ReceiptReference {
	t.Helper()
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
	command, err := newIAFinishRescanCommand(id, q)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: command.digest}
}

func iaFinishAcceptRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaFinishRescanRequest, ref ReceiptReference) (*commandTurn, iaFinishRescanAcceptOutcome, error) {
	t.Helper()
	turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAFinishRescan, action: iaEntryApply, libraryID: q.scope.libraryID, libraryKey: q.scope.libraryKey})
	out, err := acceptIAFinishRescan(t.Context(), turn, p, t.Context(), budget, ref, q)
	return turn, out, err
}

// A refused finish writes no receipt, does not move the run out of reconciling
// and leaves the completion marker empty.
func iaFinishNoEffects(t *testing.T, w *executor) {
	t.Helper()
	iaFinishNoEffectsAt(t, w, "reconciling", "")
}

// A fresh gate can itself mutate the run before the call; the refusal must
// still leave that pre-call state and the run revision untouched.
func iaFinishNoEffectsAt(t *testing.T, w *executor, phase, finishedAt string) {
	t.Helper()
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_finish'`); got != 0 {
		t.Fatal("refused finish wrote a receipt", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaFinishTestRunID); got != 5 {
		t.Fatal("refused finish advanced the run", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_runs WHERE job_id=? AND phase=? AND finished_at=?`, iaFinishTestRunID, phase, finishedAt); got != 1 {
		t.Fatal("refused finish moved the run", got)
	}
}

// A closed frontier with no unseen retained object completes the rescan with
// truthful counters, and the saved receipt replays without a new effect even
// after live source/lease changes.
func TestIAFinishRescanCompletesAndReplays(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaFinishRegistry(t, keeper)
	q := iaFinishAcceptRequest(w)
	ref := iaFinishAcceptReference(t, q, 2, 3)
	turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.digest == (ResultDigest{}) {
		t.Fatal("fresh finish", out, err)
	}
	facts := out.result.facts
	if facts.acceptedRevision != 6 || facts.appliedMS != w.now().UnixMilli() || facts.phase != iaFinishComplete ||
		facts.authoritative != 1 || facts.processed != 4 || facts.analyzed != 2 || facts.warnings != 0 ||
		facts.finishedMS != facts.appliedMS || facts.beforeKey != iaFinishRootPath || facts.afterKey != iaFinishRootPath ||
		facts.resultVersion != 1 || facts.sourceHealth != "ready" {
		t.Fatal("finish facts", facts)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	finishedAt := iaFinishTimestamp(time.UnixMilli(w.now().UnixMilli()))
	for _, check := range []struct {
		query string
		args  []any
		want  int64
	}{
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_finish' AND result_count=0 AND ia_phase='complete' AND ia_authoritative=1 AND ia_processed=4 AND ia_analyzed=2 AND ia_warnings=0 AND ia_finished_ms=? AND ia_before_key='.' AND ia_after_key='.'`, []any{w.now().UnixMilli()}, 1},
		{`SELECT count(*) FROM inventory_runs WHERE job_id='finish-run' AND job_revision=6 AND last_command_sequence=2 AND acknowledged_command_sequence=1 AND phase='complete' AND authoritative=1 AND finished_at=? AND reconcile_cursor=''`, []any{finishedAt}, 1},
	} {
		if got := iaEpisodeCount(t, w, check.query, check.args...); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// Live source/lease changes never edit or filter the saved result.
	wc008NativeExec(t, w, `UPDATE library_sources SET generation=9,root='/moved',configured_root='/moved' WHERE id='finish-source'`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET job_revision=99,job_lease_expires_ms=1 WHERE job_id='finish-run'`)
	turn, replay, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest ||
		replay.result.facts.phase != facts.phase || replay.result.facts.processed != facts.processed ||
		replay.result.facts.warnings != facts.warnings || replay.result.facts.acceptedRevision != facts.acceptedRevision {
		t.Fatal("saved finish replay", replay, err)
	}
	if got := iaMissingTxCount(t, turn, `SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_finish'`); got != 1 {
		t.Fatal("replay minted a receipt", got)
	}
	turn.transaction.rollback(nil)
}

// Warning counters are durable and truthful: an unreadable subtree forces
// complete_with_warnings and the prepared "complete" claim is a conflict.
func TestIAFinishRescanWarningsAreTruthful(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaFinishRegistry(t, keeper)
	wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('finish-run','Finish/Blocked','finish-blocked-identity','finish-blocked-revision','1','blocked',1,0)`)
	q := iaFinishAcceptRequest(w)
	ref := iaFinishAcceptReference(t, q, 2, 3)
	turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("false complete accepted", out, err)
	}
	turn.transaction.rollback(nil)
	iaFinishNoEffects(t, w)
	q.outcome = iaFinishWithWarnings
	ref = iaFinishAcceptReference(t, q, 2, 3)
	turn, out, err = iaFinishAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.phase != iaFinishWithWarnings || out.result.facts.warnings != 1 {
		t.Fatal("truthful warning finish", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_runs WHERE job_id='finish-run' AND phase='complete_with_warnings' AND warnings=1`); got != 1 {
		t.Fatal("durable warning counter", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_finish' AND ia_phase='complete_with_warnings' AND ia_warnings=1`); got != 1 {
		t.Fatal("receipt warning counter", got)
	}
	// A warning claim without durable evidence is equally untruthful.
	wc008NativeExec(t, w, `UPDATE inventory_runs SET phase='reconciling',finished_at='',acknowledged_command_sequence=2,warnings=0 WHERE job_id='finish-run'`)
	q.outcome = iaFinishWithWarnings
	q.run = inventoryRun{revision: 6, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13, leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(),
		lastSequence: 2, acknowledgedSequence: 2, phase: "reconciling", scanMode: "first-import"}
	wc008NativeExec(t, w, `DELETE FROM inventory_directories WHERE job_id='finish-run' AND relative_path='Finish/Blocked'`)
	ref = iaFinishAcceptReference(t, q, 3, 4)
	turn, out, err = iaFinishAcceptRun(t, w, reg, p, q, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("unearned warning accepted", out, err)
	}
	turn.transaction.rollback(nil)
}

// An empty optional queue is not convergence. An unseen available object is the
// required absence obligation: finish refuses while it remains, and completes
// only after the object is adjudicated missing.
func TestIAFinishRescanOptionalQueueDoesNotConverge(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaFinishRegistry(t, keeper)
	wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(53,'finish-asset-3','/media/finish/Finish/Unseen.mkv',42,7,'','','',0,0,0,8)`)
	wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:finish:3','finish-source','finish-asset-3','finish-incarnation','Finish/Unseen.mkv','ino:53','object-revision:1','{}',42,7,'','Finish','available')`)
	q := iaFinishAcceptRequest(w)
	ref := iaFinishAcceptReference(t, q, 2, 3)
	turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
	if !errors.Is(err, ErrResolveRequired) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("unseen absence obligation converged", out, err)
	}
	turn.transaction.rollback(nil)
	iaFinishNoEffects(t, w)
	// The missing window adjudicates it; convergence now has durable evidence.
	wc008NativeExec(t, w, `UPDATE inventory_objects SET state='missing',missing_since=?,missing_observations=1,last_missing_job='finish-run' WHERE id='object:finish:3'`, iaMissingTestStamp)
	turn, out, err = iaFinishAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.phase != iaFinishComplete {
		t.Fatal("adjudicated finish", out, err)
	}
	turn.transaction.rollback(nil)
}

// A protected path is a truthful warning, not an absence obligation: an unseen
// object inside it never blocks the finish, and its subtree warning is durable.
func TestIAFinishRescanProtectedSubtreeDoesNotBlock(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaFinishRegistry(t, keeper)
	wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(54,'finish-asset-4','/media/finish/Locked/Film.mkv',42,7,'','','',0,0,0,8)`)
	wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:finish:4','finish-source','finish-asset-4','finish-incarnation','Locked/Film.mkv','ino:54','object-revision:1','{}',42,7,'','Locked','available')`)
	wc008NativeExec(t, w, `INSERT INTO inventory_scan_issues(job_id,relative_path,reason,line,protect,warning) VALUES('finish-run','Locked','unreadable_entry',0,1,1)`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET warnings=1 WHERE job_id='finish-run'`)
	q := iaFinishAcceptRequest(w)
	q.outcome = iaFinishWithWarnings
	ref := iaFinishAcceptReference(t, q, 2, 3)
	turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.phase != iaFinishWithWarnings || out.result.facts.warnings != 1 {
		t.Fatal("protected subtree blocked finish", out, err)
	}
	turn.transaction.rollback(nil)
}

// A blocked (unreadable) subtree from an earlier scan leaves its objects
// available and unseen. Those objects are exempt from the absence obligation -
// exactly like a protected path - so finish completes with warnings instead of
// returning ErrResolveRequired on every attempt. A blocked directory the scan
// already charged through a protect+warning issue is not charged twice.
func TestIAFinishRescanBlockedSubtreeDoesNotWedge(t *testing.T) {
	for name, charged := range map[string]bool{"scan_charged": true, "uncharged_blocked_path": false} {
		t.Run(name, func(t *testing.T) {
			w, keeper, p := iaMissingFinishFixture(t)
			reg := iaFinishRegistry(t, keeper)
			wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('finish-run','Finish/Blocked','finish-blocked-identity','finish-blocked-revision','1','blocked',1,0)`)
			wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(55,'finish-asset-5','/media/finish/Finish/Blocked/Old.mkv',42,7,'','','',0,0,0,8)`)
			wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:finish:5','finish-source','finish-asset-5','finish-incarnation','Finish/Blocked/Old.mkv','ino:55','object-revision:1','{}',42,7,'','Finish/Blocked','available')`)
			if charged {
				wc008NativeExec(t, w, `INSERT INTO inventory_scan_issues(job_id,relative_path,reason,line,protect,warning) VALUES('finish-run','Finish/Blocked','directory_unreadable',0,1,1)`)
				wc008NativeExec(t, w, `UPDATE inventory_runs SET warnings=1 WHERE job_id='finish-run'`)
			}
			q := iaFinishAcceptRequest(w)
			q.outcome = iaFinishWithWarnings
			ref := iaFinishAcceptReference(t, q, 2, 3)
			turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
			if err != nil || out.decision != receiptFresh || out.result.facts.phase != iaFinishWithWarnings || out.result.facts.warnings != 1 {
				t.Fatal("blocked subtree wedged finish", out, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

// The exempt path COUNT is never a refusal: nine blocked directories or nine
// protected paths still page to exhaustion and finish with a truthful count.
func TestIAFinishRescanManyExemptPathsDoNotRefuse(t *testing.T) {
	t.Run("blocked_directories", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		for i := 0; i <= iaFinishMaxRows; i++ {
			name := string(rune('a' + i))
			wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('finish-run',?,?,?, '1','blocked',1,0)`,
				"Finish/Blocked"+name, "blocked-identity-"+name, "blocked-revision-"+name)
		}
		q := iaFinishAcceptRequest(w)
		q.outcome = iaFinishWithWarnings
		ref := iaFinishAcceptReference(t, q, 2, 3)
		turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || out.result.facts.phase != iaFinishWithWarnings || out.result.facts.warnings != int64(iaFinishMaxRows+1) {
			t.Fatal("many blocked directories wedged finish", out, err)
		}
		turn.transaction.rollback(nil)
	})
	t.Run("protected_paths", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		for i := 0; i <= iaFinishMaxRows; i++ {
			name := string(rune('a' + i))
			wc008NativeExec(t, w, `INSERT INTO inventory_scan_issues(job_id,relative_path,reason,line,protect,warning) VALUES('finish-run',?,'unreadable_entry',0,1,1)`, "Locked"+name)
		}
		wc008NativeExec(t, w, `UPDATE inventory_runs SET warnings=? WHERE job_id='finish-run'`, iaFinishMaxRows+1)
		q := iaFinishAcceptRequest(w)
		q.outcome = iaFinishWithWarnings
		ref := iaFinishAcceptReference(t, q, 2, 3)
		turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || out.result.facts.phase != iaFinishWithWarnings || out.result.facts.warnings != int64(iaFinishMaxRows+1) {
			t.Fatal("many protected paths wedged finish", out, err)
		}
		turn.transaction.rollback(nil)
	})
}

// Unseen objects under a protected path are exempt by prefix, not by a window
// that stops at the first page: more than one full page of them still finishes.
func TestIAFinishRescanProtectedSubtreePagesPastExemptObjects(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaFinishRegistry(t, keeper)
	wc008NativeExec(t, w, `INSERT INTO inventory_scan_issues(job_id,relative_path,reason,line,protect,warning) VALUES('finish-run','Locked','unreadable_entry',0,1,1)`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET warnings=1 WHERE job_id='finish-run'`)
	for i := 0; i < iaFinishPageRows+1; i++ {
		name := fmt.Sprintf("%03d", i)
		asset := 70 + i
		wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,42,7,'','','',0,0,0,8)`, asset, "finish-locked-token-"+name, "/media/finish/Locked/Film"+name+".mkv")
		wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES(?,?,?,?,?,?,?,?,42,7,'','Locked','available')`,
			"object:finish:l"+name, "finish-source", "finish-locked-token-"+name, "finish-incarnation",
			"Locked/Film"+name+".mkv", "ino:l-"+name, "object-revision:1", "{}")
	}
	q := iaFinishAcceptRequest(w)
	q.outcome = iaFinishWithWarnings
	ref := iaFinishAcceptReference(t, q, 2, 3)
	turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.phase != iaFinishWithWarnings || out.result.facts.warnings != 1 {
		t.Fatal("protected subtree pages wedged finish", out, err)
	}
	turn.transaction.rollback(nil)
}

// A directory that skipped part of its listing never claimed absence for the
// skipped entry, so its unseen objects are exempt from the absence obligation.
func TestIAFinishRescanSkippedSubtreeDoesNotWedge(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaFinishRegistry(t, keeper)
	wc008NativeExec(t, w, `UPDATE inventory_directories SET skipped=1 WHERE job_id='finish-run' AND relative_path='Finish'`)
	wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(56,'finish-asset-6','/media/finish/Finish/Skipped.mkv',42,7,'','','',0,0,0,8)`)
	wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:finish:6','finish-source','finish-asset-6','finish-incarnation','Finish/Skipped.mkv','ino:56','object-revision:1','{}',42,7,'','Finish','available')`)
	q := iaFinishAcceptRequest(w)
	ref := iaFinishAcceptReference(t, q, 2, 3)
	turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.phase != iaFinishComplete || out.result.facts.warnings != 0 {
		t.Fatal("skipped subtree wedged finish", out, err)
	}
	turn.transaction.rollback(nil)
}

// The skipped page forces the test-only partial index. Without the overlay the
// statement cannot prepare, so the finish holds with the same typed marker as
// the IA-18/19 veto instead of treating a failed window as empty coverage.
func TestIAFinishRescanSkippedAccessUnavailableHolds(t *testing.T) {
	w, keeper, p := iaMissingFinishFixtureOverlay(t, false)
	reg := iaFinishRegistry(t, keeper)
	q := iaFinishAcceptRequest(w)
	ref := iaFinishAcceptReference(t, q, 2, 3)
	turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
	if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, iaFinishSkippedUnavailable) ||
		out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.phase) != 0 {
		t.Fatal("skipped bounded access did not hold", out, err)
	}
	turn.transaction.rollback(nil)
	iaFinishNoEffects(t, w)
}

// The skipped frontier is a keyset page, not a single window: more than
// iaFinishPageRows skipped directories are all accounted for, including an
// exempt unseen object under a path past the first page. A skip owned by another
// job never exempts this run's object.
func TestIAFinishRescanSkippedPagesAndOtherJobIsolation(t *testing.T) {
	t.Run("multi_page", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		for i := 0; i <= iaFinishPageRows; i++ {
			path := fmt.Sprintf("Finish/Skip%03d", i)
			wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('finish-run',?,?,?,'1','done',1,1)`,
				path, path+"-identity", path+"-revision")
		}
		last := fmt.Sprintf("Finish/Skip%03d", iaFinishPageRows)
		wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(80,'finish-skip-page','/media/finish/`+last+`/Film.mkv',42,7,'','','',0,0,0,8)`)
		wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:finish:skip-page','finish-source','finish-skip-page','finish-incarnation',?,'ino:80','object-revision:1','{}',42,7,'',?,'available')`,
			last+"/Film.mkv", last)
		q := iaFinishAcceptRequest(w)
		ref := iaFinishAcceptReference(t, q, 2, 3)
		turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || out.result.facts.phase != iaFinishComplete || out.result.facts.warnings != 0 {
			t.Fatal("skipped pages wedged finish", out, err)
		}
		turn.transaction.rollback(nil)
	})
	t.Run("other_job_isolation", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('missing-run','Locked','missing-locked-identity','missing-locked-revision','1','done',1,1)`)
		wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(81,'finish-other-job','/media/finish/Locked/Film.mkv',42,7,'','','',0,0,0,8)`)
		wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:finish:other-job','finish-source','finish-other-job','finish-incarnation','Locked/Film.mkv','ino:81','object-revision:1','{}',42,7,'','Locked','available')`)
		q := iaFinishAcceptRequest(w)
		ref := iaFinishAcceptReference(t, q, 2, 3)
		turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrResolveRequired) || out.decision != 0 || out.digest != (ResultDigest{}) {
			t.Fatal("foreign job skip exempted a local object", out, err)
		}
		turn.transaction.rollback(nil)
		iaFinishNoEffects(t, w)
	})
}

// A sparse frontier (many clean directories between two skipped ones) never
// changes the plan: the keyset stays one SEARCH on the partial skipped index,
// with no full scan and no temporary sort.
func TestIAFinishRescanSkippedIndexSparseGapPlan(t *testing.T) {
	w, _, _ := iaMissingFinishFixture(t)
	wc008NativeExec(t, w, `WITH RECURSIVE c(n) AS (SELECT 0 UNION ALL SELECT n+1 FROM c WHERE n<199)
INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped)
SELECT 'finish-run','Finish/Clean'||printf('%03d',n),'clean-'||n,'clean-rev-'||n,'1','done',1,0 FROM c`)
	wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('finish-run','Finish/A-Skipped','a-identity','a-revision','1','done',1,1)`)
	wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('finish-run','Finish/Z-Skipped','z-identity','z-revision','1','done',1,1)`)
	var def stmt.Definition
	for _, d := range iaFinishRescanStatements() {
		if d.ID == iaFinishSkipped {
			def = d
		}
	}
	if def.ID == "" {
		t.Fatal("skipped statement missing")
	}
	rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+def.SQL, "finish-run", "", int64(iaFinishPageRows))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	details := []string{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "SCAN ") {
			t.Fatal("sparse gap changed the plan", detail)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := "SEARCH inventory_directories USING INDEX inventory_directories_skipped (job_id=? AND relative_path>?)"
	if len(details) != 1 || details[0] != want {
		t.Fatal(details, want)
	}
}

// The exempt accumulation is bounded: nested rows collapse to their topmost
// prefix, exactly the cap finishes, and one over the cap is a typed
// resolve-required hold with zero effects.
func TestIAFinishRescanExemptPrefixCap(t *testing.T) {
	seed := func(t *testing.T, w *executor, count int) {
		t.Helper()
		wc008NativeExec(t, w, `WITH RECURSIVE c(n) AS (SELECT 0 UNION ALL SELECT n+1 FROM c WHERE n<?)
INSERT INTO inventory_scan_issues(job_id,relative_path,reason,line,protect,warning)
SELECT 'finish-run','P'||printf('%05d',n),'unreadable_entry',0,1,1 FROM c`, count-1)
	}
	t.Run("nested_collapses", func(t *testing.T) {
		e := iaFinishExempt()
		for _, path := range []string{"Locked", "Locked/A", "Locked/A/B", "Locked2", "Locked2/X"} {
			if err := e.add(path); err != nil {
				t.Fatal(err)
			}
		}
		if len(e.paths) != 2 {
			t.Fatal("nested exempt paths did not collapse", e.paths)
		}
		if _, ok := e.paths["Locked"]; !ok {
			t.Fatal("topmost prefix missing", e.paths)
		}
		if _, ok := e.paths["Locked2"]; !ok {
			t.Fatal("second topmost prefix missing", e.paths)
		}
		if !e.covers("Locked/A/B/C") || !e.covers("Locked2/Y") || e.covers("Locked2nd") {
			t.Fatal("prefix cover", e.paths)
		}
	})
	t.Run("at_cap_finishes", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		seed(t, w, iaFinishExemptMaxPrefixes)
		q := iaFinishAcceptRequest(w)
		ref := iaFinishAcceptReference(t, q, 2, 3)
		turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || out.result.facts.phase != iaFinishComplete {
			t.Fatal("at-cap exempt set refused", out, err)
		}
		turn.transaction.rollback(nil)
	})
	t.Run("over_cap_holds", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		seed(t, w, iaFinishExemptMaxPrefixes+1)
		q := iaFinishAcceptRequest(w)
		ref := iaFinishAcceptReference(t, q, 2, 3)
		turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, iaFinishExemptUnbounded) || errors.Is(err, ErrCap) ||
			out.decision != 0 || out.digest != (ResultDigest{}) {
			t.Fatal("over-cap exempt set", out, err)
		}
		turn.transaction.rollback(nil)
		iaFinishNoEffects(t, w)
	})
}

// Open durable obligations never converge: a pending directory, an unreadable
// or missing root and an over-bound frontier all refuse.
func TestIAFinishRescanRefusesOpenObligations(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *executor, *iaFinishRescanRequest){
		"pending_directory": func(t *testing.T, w *executor, _ *iaFinishRescanRequest) {
			wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('finish-run','Finish/Pending','finish-pending-identity','finish-pending-revision','1','pending',1,0)`)
		},
		"blocked_root": func(t *testing.T, w *executor, _ *iaFinishRescanRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET state='blocked' WHERE job_id='finish-run' AND relative_path='.'`)
		},
		"missing_root": func(t *testing.T, w *executor, _ *iaFinishRescanRequest) {
			wc008NativeExec(t, w, `DELETE FROM inventory_directories WHERE job_id='finish-run' AND relative_path='.'`)
		},
		"open_traversal": func(t *testing.T, w *executor, _ *iaFinishRescanRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_runs SET inventory_completed_at='' WHERE job_id='finish-run'`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, p := iaMissingFinishFixture(t)
			reg := iaFinishRegistry(t, keeper)
			q := iaFinishAcceptRequest(w)
			ref := iaFinishAcceptReference(t, q, 2, 3)
			mutate(t, w, &q)
			turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
				t.Fatal("open obligation converged", out, err)
			}
			turn.transaction.rollback(nil)
			iaFinishNoEffects(t, w)
		})
	}
	t.Run("too_many_unseen", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		for i := 0; i <= iaFinishMaxRows; i++ {
			name := string(rune('a' + i))
			wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,42,7,'','','',0,0,0,8)`, 60+i, "finish-unseen-token-"+name, "/media/finish/Finish/Unseen"+name+".mkv")
			wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES(?,?,?,?,?,?,?,?,42,7,'','Finish','available')`,
				"object:finish:u"+name, "finish-source", "finish-unseen-token-"+name, "finish-incarnation",
				"Finish/Unseen"+name+".mkv", "ino:u-"+name, "object-revision:1", "{}")
		}
		q := iaFinishAcceptRequest(w)
		ref := iaFinishAcceptReference(t, q, 2, 3)
		turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrCap) || out.decision != 0 {
			t.Fatal("over-bound unseen frontier accepted", out, err)
		}
		turn.transaction.rollback(nil)
	})
	t.Run("unresolved_mp10_pins", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		q := iaFinishAcceptRequest(w)
		q.pinsResolved = false
		ref := iaFinishAcceptReference(t, q, 2, 3)
		turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, iaCoveragePinsUnresolved) || out.decision != 0 {
			t.Fatal("unresolved pins converged", out, err)
		}
		turn.transaction.rollback(nil)
	})
}

// Every fresh witness is re-proven after the receipt is Fresh.
func TestIAFinishRescanFreshGates(t *testing.T) {
	type gate struct {
		mutate     func(*testing.T, *executor, *iaFinishRescanRequest)
		phase      string
		finishedAt string
	}
	for name, g := range map[string]gate{
		"retired_library": {mutate: func(t *testing.T, w *executor, _ *iaFinishRescanRequest) {
			wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=8`)
		}},
		"offline_source": {mutate: func(t *testing.T, w *executor, _ *iaFinishRescanRequest) {
			wc008NativeExec(t, w, `UPDATE library_sources SET health='offline' WHERE id='finish-source'`)
		}},
		"stale_source": {mutate: func(_ *testing.T, _ *executor, q *iaFinishRescanRequest) { q.source.generation++ }},
		"changed_lease": {mutate: func(t *testing.T, w *executor, q *iaFinishRescanRequest) {
			q.run.leaseExpiresMS = w.now().UnixMilli()
		}},
		"stale_run_revision": {mutate: func(_ *testing.T, _ *executor, q *iaFinishRescanRequest) { q.run.revision++ }},
		"changed_run_phase": {phase: "inventory", mutate: func(t *testing.T, w *executor, _ *iaFinishRescanRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_runs SET phase='inventory' WHERE job_id='finish-run'`)
		}},
		"finished_run": {finishedAt: "2026-11-08T00:00:00.000000000Z", mutate: func(t *testing.T, w *executor, _ *iaFinishRescanRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_runs SET finished_at='2026-11-08T00:00:00.000000000Z' WHERE job_id='finish-run'`)
		}},
		"impossible_counters": {mutate: func(t *testing.T, w *executor, _ *iaFinishRescanRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_runs SET analyzed=9 WHERE job_id='finish-run'`)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			w, keeper, p := iaMissingFinishFixture(t)
			reg := iaFinishRegistry(t, keeper)
			q := iaFinishAcceptRequest(w)
			ref := iaFinishAcceptReference(t, q, 2, 3)
			g.mutate(t, w, &q)
			turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.phase) != 0 {
				t.Fatal("fresh gate", out, err)
			}
			turn.transaction.rollback(nil)
			phase := g.phase
			if phase == "" {
				phase = "reconciling"
			}
			iaFinishNoEffectsAt(t, w, phase, g.finishedAt)
		})
	}
}

// Current authority precedes every retained read: a forged issuer, a demoted
// owner and a cancelled caller lifetime all deny with zero public output.
func TestIAFinishRescanAuthorityCells(t *testing.T) {
	t.Run("forged_issuer", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		q := iaFinishAcceptRequest(w)
		ref := iaFinishAcceptReference(t, q, 2, 3)
		fake := *p
		fake.witness.issuer.family = "forged"
		turn, out, err := iaFinishAcceptRun(t, w, reg, &fake, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.phase) != 0 {
			t.Fatal("forged issuer", out, err)
		}
		turn.transaction.rollback(nil)
		iaFinishNoEffects(t, w)
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
				reg := iaFinishRegistry(t, keeper)
				wc008NativeExec(t, w, mutation)
				q := iaFinishAcceptRequest(w)
				ref := iaFinishAcceptReference(t, q, 2, 3)
				turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
				if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.phase) != 0 {
					t.Fatal("stale authority", out, err)
				}
				turn.transaction.rollback(nil)
				iaFinishNoEffects(t, w)
			})
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		q := iaFinishAcceptRequest(w)
		ref := iaFinishAcceptReference(t, q, 2, 3)
		turn, budget := iaEntryTurn(t, w, reg, iaEntryRequest{purpose: IAFinishRescan, action: iaEntryApply, libraryID: q.scope.libraryID, libraryKey: q.scope.libraryKey})
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := acceptIAFinishRescan(cancelled, turn, p, cancelled, budget, ref, q)
		if err == nil || out.decision != 0 || len(out.result.facts.phase) != 0 {
			t.Fatal("cancelled lifetime", out, err)
		}
		turn.transaction.rollback(nil)
		iaFinishNoEffects(t, w)
	})
}

// Current authority is re-observed after the frontier reads and again after the
// receipt write. A same-turn revocation must deny the fresh result.
func TestIAFinishRescanLateAuthorityRechecks(t *testing.T) {
	t.Run("before_writes", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		state := installIAEntryQueryRevocation(t, w, "FROM inventory_directories")
		q := iaFinishAcceptRequest(w)
		ref := iaFinishAcceptReference(t, q, 2, 3)
		turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
		if !state.fired || state.err != nil {
			t.Fatal("directory-read mutation did not run", state.err)
		}
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.phase) != 0 {
			t.Fatal("pre-write recheck released a stale result", out, err)
		}
		turn.transaction.rollback(nil)
		iaFinishNoEffects(t, w)
	})
	t.Run("after_writes", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		wc008NativeExec(t, w, `CREATE TRIGGER ia_finish_late_revocation AFTER INSERT ON scan_command_receipts BEGIN UPDATE identity_security_fences SET denied=1; END`)
		q := iaFinishAcceptRequest(w)
		ref := iaFinishAcceptReference(t, q, 2, 3)
		turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.phase) != 0 {
			t.Fatal("post-write recheck released a stale result", out, err)
		}
		var written int64
		if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_finish'`).Scan(&written); err != nil || written != 1 {
			t.Fatal("receipt write not reached before the final recheck", written, err)
		}
		turn.transaction.rollback(nil)
		iaFinishNoEffects(t, w)
	})
}

// A saved receipt must be exact before it is served: the phase/warning
// invariant, the completion marker and the result digest are all checked.
func TestIAFinishRescanReplayRejectsTamperedReceipt(t *testing.T) {
	for name, mutate := range map[string]string{
		"result_digest": `UPDATE scan_command_receipts SET result_digest=zeroblob(32) WHERE command_kind='inventory_finish'`,
		"phase":         `UPDATE scan_command_receipts SET ia_phase='complete_with_warnings' WHERE command_kind='inventory_finish'`,
		"warnings":      `UPDATE scan_command_receipts SET ia_warnings=3 WHERE command_kind='inventory_finish'`,
		"finished_ms":   `UPDATE scan_command_receipts SET ia_finished_ms=1 WHERE command_kind='inventory_finish'`,
		"after_key":     `UPDATE scan_command_receipts SET ia_after_key='Finish' WHERE command_kind='inventory_finish'`,
		"processed":     `UPDATE scan_command_receipts SET ia_processed=99 WHERE command_kind='inventory_finish'`,
		"authoritative": `UPDATE scan_command_receipts SET ia_authoritative=0 WHERE command_kind='inventory_finish'`,
	} {
		unchecked := name == "finished_ms"
		t.Run(name, func(t *testing.T) {
			w, keeper, p := iaMissingFinishFixture(t)
			reg := iaFinishRegistry(t, keeper)
			q := iaFinishAcceptRequest(w)
			ref := iaFinishAcceptReference(t, q, 2, 3)
			turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
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
			turn, replay, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
			if err == nil || replay.decision != 0 || replay.digest != (ResultDigest{}) || len(replay.result.facts.phase) != 0 {
				t.Fatal("tampered receipt served", replay, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

// A lost COMMIT reply is never success. The committed file is read back through
// a FRESH read-only connection, never the fenced writer pool.
func TestIAFinishRescanUnknownCommitIsNotSuccess(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaFinishRegistry(t, keeper)
	path := iaEpisodePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	q := iaFinishAcceptRequest(w)
	ref := iaFinishAcceptReference(t, q, 2, 3)
	turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.phase != iaFinishComplete {
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
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_finish' AND result_count=0 AND ia_phase='complete'`, 1},
		{`SELECT job_revision FROM inventory_runs WHERE job_id='finish-run'`, 6},
		{`SELECT count(*) FROM inventory_runs WHERE job_id='finish-run' AND phase='complete'`, 1},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	if _, err = w.begin(t.Context(), IAFinishRescan, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

// At or below the durable ACK floor the original is Retired: no result, no
// digest and no new effect, even while the physical receipt still exists.
func TestIAFinishRescanRetiredAfterAck(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaFinishRegistry(t, keeper)
	q := iaFinishAcceptRequest(w)
	ref := iaFinishAcceptReference(t, q, 2, 3)
	turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='finish-run'`)
	before := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`)
	beforeRevision := iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaFinishTestRunID)
	turn, out, err = iaFinishAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptRetired || out.digest != (ResultDigest{}) || len(out.result.facts.phase) != 0 {
		t.Fatal("retired original", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != before {
		t.Fatal("retired replay wrote a receipt", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaFinishTestRunID); got != beforeRevision {
		t.Fatal("retired replay advanced the run", got)
	}
}

// A conflicting digest, a non-contiguous sequence and a malformed original are
// conflicts or invalid input, never a fresh admission or a new receipt.
func TestIAFinishRescanRejectsConflictingOriginal(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaFinishRegistry(t, keeper)
	q := iaFinishAcceptRequest(w)
	ref := iaFinishAcceptReference(t, q, 2, 3)
	changed := q
	changed.outcome = iaFinishWithWarnings
	turn, out, err := iaFinishAcceptRun(t, w, reg, p, changed, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("changed request digest", out, err)
	}
	turn.transaction.rollback(nil)
	gap := q
	gap.run.revision, gap.run.lastSequence, gap.run.acknowledgedSequence = 6, 2, 2
	gapRef := iaFinishAcceptReference(t, gap, 3, 4)
	turn, out, err = iaFinishAcceptRun(t, w, reg, p, gap, gapRef)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("sequence gap", out, err)
	}
	turn.transaction.rollback(nil)
	for name, corrupt := range map[string]func(*iaFinishRescanRequest){
		"unknown_outcome": func(q *iaFinishRescanRequest) { q.outcome = "finished" },
		"escaping_root":   func(q *iaFinishRescanRequest) { q.source.resolvedRoot = []byte("../escape") },
		"wrong_mode":      func(q *iaFinishRescanRequest) { q.run.scanMode = "sideways" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := q
			corrupt(&bad)
			if _, err = newIAFinishRescanCommand(CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{3}}, bad); !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed original accepted", err)
			}
		})
	}
	iaFinishNoEffects(t, w)
}

func TestIAFinishRescanRollbackLeavesNoEffect(t *testing.T) {
	w, keeper, p := iaMissingFinishFixture(t)
	reg := iaFinishRegistry(t, keeper)
	q := iaFinishAcceptRequest(w)
	ref := iaFinishAcceptReference(t, q, 2, 3)
	turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	iaFinishNoEffects(t, w)
}

func TestIAFinishRescanDescriptor(t *testing.T) {
	got, err := Describe(IAFinishRescan)
	want := Descriptor{ID: 123, Name: "ia_finish_rescan", Durability: Normal}
	if err != nil || got != want {
		t.Fatalf("Describe(123) = %+v, %v; want %+v", got, err, want)
	}
	if kind, err := iaEntryCommandKind(IAFinishRescan); err != nil || kind != "inventory_finish" {
		t.Fatalf("finish kind = %q, %v", kind, err)
	}
	if class, err := commandClass(IAFinishRescan); err != nil || class != governor.Background {
		t.Fatalf("finish class = %v, %v", class, err)
	}
}
