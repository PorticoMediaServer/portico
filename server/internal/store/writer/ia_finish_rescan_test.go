package writer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
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
	t.Run("too_many_blocked", func(t *testing.T) {
		w, keeper, p := iaMissingFinishFixture(t)
		reg := iaFinishRegistry(t, keeper)
		for i := 0; i <= iaFinishMaxRows; i++ {
			wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('finish-run',?,?,?, '1','blocked',1,0)`,
				"Finish/Blocked"+string(rune('a'+i)), "blocked-identity-"+string(rune('a'+i)), "blocked-revision-"+string(rune('a'+i)))
		}
		q := iaFinishAcceptRequest(w)
		q.outcome = iaFinishWithWarnings
		ref := iaFinishAcceptReference(t, q, 2, 3)
		turn, out, err := iaFinishAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrCap) || out.decision != 0 {
			t.Fatal("over-bound frontier accepted", out, err)
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
