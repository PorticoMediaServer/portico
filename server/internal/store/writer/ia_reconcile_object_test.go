package writer

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const (
	iaReconcileTestLibraryID      int64 = 7
	iaReconcileTestLibraryKey           = "reconcile-library"
	iaReconcileTestSourceID             = "reconcile-source"
	iaReconcileTestRunID                = "reconcile-run"
	iaReconcileTestAssetToken           = "reconcile-token"
	iaReconcileTestRoot                 = "/media/reconcile"
	iaReconcileTestObjectID             = "object:reconcile:1"
	iaReconcileTestObjectRevision       = "object-revision:1"
	iaReconcileTestPath                 = "Reconcile/Movie.mkv"
	iaReconcileTestMovedPath            = "Reconcile/Moved.mkv"
)

// One private W frame, the frozen clock and the exact group C draft subset. The
// fixture seeds only a prepared source/run/retained object; it is not a
// qualified population, admission profile or product wiring.
func iaReconcileObjectFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ia-reconcile-object-draft.sql")
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
		{`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`, nil},
		{`INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES('reconcile-library','Reconcile','movies','/media/reconcile',1,1,1)`, nil},
		{`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'reconcile-library','/media/reconcile')`, nil},
		{`INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES('reconcile-source','reconcile-library','Reconcile','/media/reconcile','/media/reconcile','local','reconcile-root-identity',0,0,'',1,'reconcile-incarnation',3,'ready',1,'local')`, nil},
		{`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('reconcile-run','reconcile-library','running',0,'')`, nil},
		{`INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES('reconcile-run',7,'reconcile-library','/media/reconcile',0,?,?,'reconcile-source',3,'reconcile-incarnation','reconcile-root-identity',4,'inventory','first-import',11,13,?,5,?,1,1)`,
			[]any{iaEpisodeBytes(3), make([]byte, 32), w.now().Add(time.Hour).UnixMilli(), iaEpisodeBytes(1)}},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(41,'reconcile-token','/media/reconcile/Reconcile/Movie.mkv',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state,analysis_state) VALUES('object:reconcile:1','reconcile-source','reconcile-token','reconcile-incarnation','Reconcile/Movie.mkv','ino:41','object-revision:1','{}',42,7,'reconcile-run','Reconcile','available','complete')`, nil},
	} {
		if _, err = w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}
	registry, err := stmttest.New(t, iaReconcileObjectDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{registry, keeper}, proof
}

func iaReconcileAcceptRequest(w *executor) iaReconcileObjectRequest {
	retained := iaReconcileWitness{objectID: iaReconcileTestObjectID, relativePath: iaReconcileTestPath,
		objectIdentity: "ino:41", revision: iaReconcileTestObjectRevision, size: 42, modifiedNS: 7}
	return iaReconcileObjectRequest{
		scope:              inventoryScope{runID: iaReconcileTestRunID, libraryKey: iaReconcileTestLibraryKey, libraryID: iaReconcileTestLibraryID},
		source:             inventorySource{id: iaReconcileTestSourceID, incarnation: "reconcile-incarnation", rootIdentity: "reconcile-root-identity", resolvedRoot: []byte(iaReconcileTestRoot), generation: 3},
		run:                inventoryRun{revision: 5, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13, leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(), lastSequence: 1, acknowledgedSequence: 1, phase: "inventory", scanMode: "first-import"},
		accepted:           retained,
		current:            retained,
		moveWindowComplete: true,
		phase:              "reconciling",
	}
}

func iaReconcileAcceptReference(t *testing.T, r iaReconcileObjectRequest, sequence int64, operation byte) ReceiptReference {
	t.Helper()
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
	command, err := newIAReconcileObjectCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: command.digest}
}

func iaReconcileAcceptTurn(t *testing.T, w *executor, r *iaEntryRegistry, q iaReconcileObjectRequest) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAReconcileObject, action: iaEntryApply, libraryID: q.scope.libraryID, libraryKey: q.scope.libraryKey})
}

func iaReconcileAcceptRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaReconcileObjectRequest, ref ReceiptReference) (*commandTurn, iaReconcileAcceptOutcome, error) {
	t.Helper()
	turn, budget := iaReconcileAcceptTurn(t, w, r, q)
	out, err := acceptIAReconcileObject(t.Context(), turn, p, t.Context(), budget, ref, q)
	return turn, out, err
}

func iaReconcileNoEffects(t *testing.T, w *executor) {
	t.Helper()
	for _, table := range []string{"scan_command_receipts", "scan_reconcile_members"} {
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
			t.Fatal("refused request wrote", table, got)
		}
	}
	if got := iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaReconcileTestRunID); got != 5 {
		t.Fatal("refused request advanced the run", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT size FROM inventory_objects WHERE id=?`, iaReconcileTestObjectID); got != 42 {
		t.Fatal("refused request mutated the retained object", got)
	}
}

// An unchanged object schedules zero probes: it writes the exact receipt, no
// child row, and one run chronology advance. Replay returns the saved result
// after live source/lease changes without any new effect.
func TestIAReconcileObjectUnchangedZeroProbeAndReplay(t *testing.T) {
	w, reg, p := iaReconcileObjectFixture(t)
	q := iaReconcileAcceptRequest(w)
	ref := iaReconcileAcceptReference(t, q, 2, 2)
	turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.digest == (ResultDigest{}) {
		t.Fatal("fresh unchanged reconcile", out, err)
	}
	if out.result.facts.acceptedRevision != 6 || out.result.facts.appliedMS != w.now().UnixMilli() ||
		out.result.facts.resultCount != 0 || len(out.result.facts.members) != 0 || out.result.facts.analyzed != 0 ||
		out.result.facts.processed != 1 || out.result.facts.phase != "reconciling" ||
		out.result.facts.sourceHealth != "ready" || out.result.facts.beforeKey != iaReconcileTestObjectID || out.result.facts.afterKey != iaReconcileTestObjectID {
		t.Fatal("unchanged facts", out.result.facts)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_reconcile' AND result_count=0 AND ia_processed=1 AND ia_analyzed=0 AND ia_finished_ms=0 AND ia_source_health='ready'`, 1},
		{`SELECT count(*) FROM scan_reconcile_members`, 0},
		{`SELECT count(*) FROM inventory_objects WHERE id='object:reconcile:1' AND relative_path='Reconcile/Movie.mkv' AND revision='object-revision:1' AND size=42`, 1},
		{`SELECT count(*) FROM inventory_runs WHERE job_revision=6 AND last_command_sequence=2 AND acknowledged_command_sequence=1`, 1},
	} {
		if got := iaEpisodeCount(t, w, check.query); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// Live source/lease changes never edit or filter the saved result.
	wc008NativeExec(t, w, `UPDATE library_sources SET generation=9,root='/moved',configured_root='/moved' WHERE id='reconcile-source'`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET job_revision=99,job_lease_expires_ms=1 WHERE job_id='reconcile-run'`)
	turn, replay, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest || replay.result.facts.resultCount != 0 ||
		replay.result.facts.acceptedRevision != out.result.facts.acceptedRevision || replay.result.facts.beforeKey != out.result.facts.beforeKey {
		t.Fatal("saved unchanged replay", replay, err)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 1 {
		t.Fatal("replay minted a receipt", got)
	}
	turn.transaction.rollback(nil)
}

// A changed witness is recorded as one exact disposition, leaves the retained
// object and catalogue untouched, and marks the one analyzed unit so the
// producer can queue a new Basic pass.
func TestIAReconcileObjectChangedQueuesBasic(t *testing.T) {
	w, reg, p := iaReconcileObjectFixture(t)
	q := iaReconcileAcceptRequest(w)
	q.current.size = 43
	q.current.revision = "object-revision:2"
	ref := iaReconcileAcceptReference(t, q, 2, 2)
	turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 1 || out.result.facts.analyzed != 1 ||
		out.result.facts.afterKey != iaReconcileTestObjectID {
		t.Fatal("changed reconcile", out, err)
	}
	m := out.result.facts.members[0]
	if m.disposition != "changed" || m.objectID != iaReconcileTestObjectID || m.priorObjectID != nil ||
		m.beforePath != iaReconcileTestPath || m.afterPath != iaReconcileTestPath ||
		m.beforeRevision != iaReconcileTestObjectRevision || m.afterRevision != "object-revision:2" {
		t.Fatal("changed member", m)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_reconcile_members WHERE command_kind='inventory_reconcile' AND disposition='changed' AND object_id='object:reconcile:1' AND prior_object_id IS NULL AND before_revision='object-revision:1' AND after_revision='object-revision:2'`); got != 1 {
		t.Fatal("changed child", got)
	}
	// No mid-walk canonical mutation: the retained observation keeps its old
	// exact state until the refresh journal owns the change.
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_objects WHERE id='object:reconcile:1' AND revision='object-revision:1' AND size=42 AND object_identity='ino:41'`); got != 1 {
		t.Fatal("changed reconcile mutated the retained object", got)
	}
}

// A different native identity at the same path is a replacement with a distinct
// prepared object id and an exact prior object.
func TestIAReconcileObjectReplacement(t *testing.T) {
	w, reg, p := iaReconcileObjectFixture(t)
	q := iaReconcileAcceptRequest(w)
	q.current.size = 43
	q.current.revision = "object-revision:2"
	q.current.objectIdentity = "ino:42"
	q.current.objectID = "object:reconcile:2"
	q.afterObjectID = "object:reconcile:2"
	ref := iaReconcileAcceptReference(t, q, 2, 2)
	turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 1 || out.result.facts.afterKey != "object:reconcile:2" {
		t.Fatal("replacement reconcile", out, err)
	}
	m := out.result.facts.members[0]
	if m.disposition != "replaced" || m.objectID != "object:reconcile:2" || m.priorObjectID == nil || *m.priorObjectID != iaReconcileTestObjectID {
		t.Fatal("replacement member", m)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_reconcile_members WHERE disposition='replaced' AND object_id='object:reconcile:2' AND prior_object_id='object:reconcile:1'`); got != 1 {
		t.Fatal("replacement child", got)
	}
	// Without the prepared replacement id the same observation must refuse
	// rather than reuse the retained object id.
	w2, reg2, p2 := iaReconcileObjectFixture(t)
	q2 := iaReconcileAcceptRequest(w2)
	q2.current.size = 43
	q2.current.revision = "object-revision:2"
	q2.current.objectIdentity = "ino:42"
	ref2 := iaReconcileAcceptReference(t, q2, 2, 2)
	turn2, out2, err := iaReconcileAcceptRun(t, w2, reg2, p2, q2, ref2)
	if !errors.Is(err, ErrConflict) || out2.decision != 0 || len(out2.result.facts.members) != 0 {
		t.Fatal("replacement without id", out2, err)
	}
	turn2.transaction.rollback(nil)
	iaReconcileNoEffects(t, w2)
}

// A native move keeps the retained object identity and records the exact old and
// new retained paths. An occupied live destination refuses; a retired placement
// does not block.
func TestIAReconcileObjectSafeNativeMove(t *testing.T) {
	t.Run("moved", func(t *testing.T) {
		w, reg, p := iaReconcileObjectFixture(t)
		q := iaReconcileAcceptRequest(w)
		q.current.relativePath = iaReconcileTestMovedPath
		ref := iaReconcileAcceptReference(t, q, 2, 2)
		turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 1 || out.result.facts.afterKey != iaReconcileTestObjectID {
			t.Fatal("native move", out, err)
		}
		m := out.result.facts.members[0]
		if m.disposition != "moved" || m.objectID != iaReconcileTestObjectID || m.priorObjectID != nil ||
			m.beforePath != iaReconcileTestPath || m.afterPath != iaReconcileTestMovedPath || m.beforeRevision != m.afterRevision {
			t.Fatal("moved member", m)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		// Retained placements keep their IDs; the retained row is not rewritten.
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_objects WHERE id='object:reconcile:1' AND relative_path='Reconcile/Movie.mkv'`); got != 1 {
			t.Fatal("move rewrote the retained path", got)
		}
	})
	t.Run("occupied_destination", func(t *testing.T) {
		w, reg, p := iaReconcileObjectFixture(t)
		wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(42,'reconcile-token-2','/media/reconcile/Reconcile/Moved.mkv',42,7,'','','',0,0,0,7)`)
		wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:reconcile:2','reconcile-source','reconcile-token-2','reconcile-incarnation','Reconcile/Moved.mkv','ino:99','object-revision:1','{}',42,7,'reconcile-run','Reconcile','available')`)
		q := iaReconcileAcceptRequest(w)
		q.current.relativePath = iaReconcileTestMovedPath
		ref := iaReconcileAcceptReference(t, q, 2, 2)
		turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("occupied destination", out, err)
		}
		turn.transaction.rollback(nil)
	})
	t.Run("retired_destination", func(t *testing.T) {
		w, reg, p := iaReconcileObjectFixture(t)
		wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(42,'reconcile-token-2','/media/reconcile/Reconcile/Moved.mkv',42,7,'','','',0,0,0,7)`)
		wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state,retired) VALUES('object:reconcile:2','reconcile-source','reconcile-token-2','reconcile-incarnation','Reconcile/Moved.mkv','ino:99','object-revision:1','{}',42,7,'reconcile-run','Reconcile','available',1)`)
		q := iaReconcileAcceptRequest(w)
		q.current.relativePath = iaReconcileTestMovedPath
		ref := iaReconcileAcceptReference(t, q, 2, 2)
		turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh || out.result.facts.members[0].disposition != "moved" {
			t.Fatal("retired destination", out, err)
		}
		turn.transaction.rollback(nil)
	})
}

// Two retained placements with one native identity are ambiguous. An incomplete
// window cannot nominate a move, and a third raw row proves the window is wider
// than the admitted shape.
func TestIAReconcileObjectAmbiguousMoveRefusal(t *testing.T) {
	t.Run("two_matches", func(t *testing.T) {
		w, reg, p := iaReconcileObjectFixture(t)
		wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(42,'reconcile-token-2','/media/reconcile/Reconcile/Link.mkv',42,7,'','','',0,0,0,7)`)
		wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:reconcile:2','reconcile-source','reconcile-token-2','reconcile-incarnation','Reconcile/Link.mkv','ino:41','object-revision:1','{}',42,7,'reconcile-run','Reconcile','available')`)
		q := iaReconcileAcceptRequest(w)
		q.current.relativePath = iaReconcileTestMovedPath
		ref := iaReconcileAcceptReference(t, q, 2, 2)
		turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("ambiguous move", out, err)
		}
		turn.transaction.rollback(nil)
		iaReconcileNoEffects(t, w)
	})
	t.Run("incomplete_window", func(t *testing.T) {
		w, reg, p := iaReconcileObjectFixture(t)
		q := iaReconcileAcceptRequest(w)
		q.current.relativePath = iaReconcileTestMovedPath
		q.moveWindowComplete = false
		ref := iaReconcileAcceptReference(t, q, 2, 2)
		turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrResolveRequired) || out.decision != 0 {
			t.Fatal("incomplete move window", out, err)
		}
		turn.transaction.rollback(nil)
		iaReconcileNoEffects(t, w)
	})
	t.Run("wide_window", func(t *testing.T) {
		w, reg, p := iaReconcileObjectFixture(t)
		for i := int64(2); i <= 4; i++ {
			wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,42,7,'','','',0,0,0,7)`, 40+i, "reconcile-token-"+string(rune('0'+i)), "/media/reconcile/Reconcile/Link"+string(rune('0'+i))+".mkv")
			wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES(?,?,?,?,?,?,?,?,42,7,'reconcile-run','Reconcile','available')`,
				"object:reconcile:"+string(rune('0'+i)), "reconcile-source", "reconcile-token-"+string(rune('0'+i)), "reconcile-incarnation", "Reconcile/Link"+string(rune('0'+i))+".mkv", "ino:41", "object-revision:1", "{}")
		}
		q := iaReconcileAcceptRequest(w)
		q.current.relativePath = iaReconcileTestMovedPath
		ref := iaReconcileAcceptReference(t, q, 2, 2)
		turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrResolveRequired) || out.decision != 0 {
			t.Fatal("wide move window", out, err)
		}
		turn.transaction.rollback(nil)
	})
}

// Every fresh witness is re-proven after the receipt is Fresh. A changed
// library, source, run, lease or retained object refuses the whole unit.
func TestIAReconcileObjectFreshGates(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *executor, *iaReconcileObjectRequest){
		"retired_library": func(t *testing.T, w *executor, _ *iaReconcileObjectRequest) {
			wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=7`)
		},
		"stale_source": func(_ *testing.T, _ *executor, q *iaReconcileObjectRequest) { q.source.generation++ },
		"changed_lease": func(t *testing.T, w *executor, q *iaReconcileObjectRequest) {
			q.run.leaseExpiresMS = w.now().UnixMilli()
		},
		"stale_run_revision": func(_ *testing.T, _ *executor, q *iaReconcileObjectRequest) { q.run.revision++ },
		"stale_object_revision": func(_ *testing.T, _ *executor, q *iaReconcileObjectRequest) {
			q.accepted.revision = "object-revision:old"
			q.current.revision = "object-revision:old"
		},
		"changed_object_size": func(t *testing.T, w *executor, _ *iaReconcileObjectRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_objects SET size=43 WHERE id='object:reconcile:1'`)
		},
		"changed_object_mtime": func(t *testing.T, w *executor, _ *iaReconcileObjectRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_objects SET modified_ns=8 WHERE id='object:reconcile:1'`)
		},
		"changed_object_identity": func(t *testing.T, w *executor, _ *iaReconcileObjectRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_objects SET object_identity='ino:99' WHERE id='object:reconcile:1'`)
		},
		"retired_object": func(t *testing.T, w *executor, _ *iaReconcileObjectRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_objects SET retired=1 WHERE id='object:reconcile:1'`)
		},
		"missing_object": func(t *testing.T, w *executor, _ *iaReconcileObjectRequest) {
			wc008NativeExec(t, w, `DELETE FROM inventory_objects WHERE id='object:reconcile:1'`)
		},
		"after_id_on_changed": func(_ *testing.T, _ *executor, q *iaReconcileObjectRequest) {
			q.current.objectID = "object:reconcile:2"
			q.afterObjectID = "object:reconcile:2"
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaReconcileObjectFixture(t)
			q := iaReconcileAcceptRequest(w)
			mutate(t, w, &q)
			ref := iaReconcileAcceptReference(t, q, 2, 2)
			turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
				t.Fatal("fresh gate", out, err)
			}
			turn.transaction.rollback(nil)
			if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 0 {
				t.Fatal("gate wrote a receipt", got)
			}
			if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_reconcile_members`); got != 0 {
				t.Fatal("gate wrote a child", got)
			}
			if got := iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaReconcileTestRunID); got != 5 {
				t.Fatal("gate advanced the run", got)
			}
		})
	}
}

// Current authority precedes every retained read: a forged issuer, a demoted
// owner and a cancelled caller lifetime all deny with zero public output.
func TestIAReconcileObjectAuthorityCells(t *testing.T) {
	t.Run("forged_issuer", func(t *testing.T) {
		w, reg, p := iaReconcileObjectFixture(t)
		q := iaReconcileAcceptRequest(w)
		ref := iaReconcileAcceptReference(t, q, 2, 2)
		fake := *p
		fake.witness.issuer.family = "forged"
		turn, out, err := iaReconcileAcceptRun(t, w, reg, &fake, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("forged issuer", out, err)
		}
		turn.transaction.rollback(nil)
		iaReconcileNoEffects(t, w)
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
				w, reg, p := iaReconcileObjectFixture(t)
				wc008NativeExec(t, w, mutation)
				q := iaReconcileAcceptRequest(w)
				ref := iaReconcileAcceptReference(t, q, 2, 2)
				turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
				if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
					t.Fatal("stale authority", out, err)
				}
				turn.transaction.rollback(nil)
				iaReconcileNoEffects(t, w)
			})
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, reg, p := iaReconcileObjectFixture(t)
		q := iaReconcileAcceptRequest(w)
		ref := iaReconcileAcceptReference(t, q, 2, 2)
		turn, budget := iaReconcileAcceptTurn(t, w, reg, q)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := acceptIAReconcileObject(cancelled, turn, p, cancelled, budget, ref, q)
		if err == nil || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("cancelled lifetime", out, err)
		}
		turn.transaction.rollback(nil)
		iaReconcileNoEffects(t, w)
	})
}

// Current authority is re-observed after the fresh object gate and again after
// the child write. A same-turn revocation must deny the fresh result.
func TestIAReconcileObjectLateAuthorityRechecks(t *testing.T) {
	t.Run("before_writes", func(t *testing.T) {
		w, reg, p := iaReconcileObjectFixture(t)
		state := installIAEntryQueryRevocation(t, w, "FROM inventory_objects")
		q := iaReconcileAcceptRequest(w)
		ref := iaReconcileAcceptReference(t, q, 2, 2)
		turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
		if !state.fired || state.err != nil {
			t.Fatal("object-close mutation did not run", state.err)
		}
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("pre-write recheck released a stale result", out, err)
		}
		turn.transaction.rollback(nil)
		iaReconcileNoEffects(t, w)
	})
	t.Run("after_writes", func(t *testing.T) {
		w, reg, p := iaReconcileObjectFixture(t)
		wc008NativeExec(t, w, `CREATE TRIGGER ia_reconcile_late_revocation AFTER INSERT ON scan_reconcile_members BEGIN UPDATE identity_security_fences SET denied=1; END`)
		q := iaReconcileAcceptRequest(w)
		q.current.size = 43
		q.current.revision = "object-revision:2"
		ref := iaReconcileAcceptReference(t, q, 2, 2)
		turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("post-write recheck released a stale result", out, err)
		}
		var children int64
		if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_reconcile_members`).Scan(&children); err != nil || children != 1 {
			t.Fatal("child write not reached before the final recheck", children, err)
		}
		turn.transaction.rollback(nil)
		iaReconcileNoEffects(t, w)
	})
}

// A saved cohort must be complete and digest-exact before it is served.
func TestIAReconcileObjectReplayRejectsTamperedCohort(t *testing.T) {
	for name, mutate := range map[string]string{
		"missing_member": `DELETE FROM scan_reconcile_members`,
		"result_digest":  `UPDATE scan_command_receipts SET result_digest=zeroblob(32)`,
		"before_key":     `UPDATE scan_command_receipts SET ia_before_key='object:forged'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaReconcileObjectFixture(t)
			q := iaReconcileAcceptRequest(w)
			q.current.size = 43
			q.current.revision = "object-revision:2"
			ref := iaReconcileAcceptReference(t, q, 2, 2)
			turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
			if err != nil || out.decision != receiptFresh {
				t.Fatal(out, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			wc008NativeExec(t, w, mutate)
			turn, replay, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrResolveRequired) || replay.decision != 0 || replay.digest != (ResultDigest{}) || len(replay.result.facts.members) != 0 {
				t.Fatal("tampered cohort served", replay, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

// A lost COMMIT reply is never success. The committed file is read back through
// a FRESH read-only connection, never the fenced writer pool.
func TestIAReconcileObjectUnknownCommitIsNotSuccess(t *testing.T) {
	w, reg, p := iaReconcileObjectFixture(t)
	path := iaEpisodePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	q := iaReconcileAcceptRequest(w)
	q.current.size = 43
	q.current.revision = "object-revision:2"
	ref := iaReconcileAcceptReference(t, q, 2, 2)
	turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 1 {
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
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_reconcile' AND result_count=1`, 1},
		{`SELECT count(*) FROM scan_reconcile_members WHERE command_kind='inventory_reconcile' AND disposition='changed'`, 1},
		{`SELECT job_revision FROM inventory_runs WHERE job_id='reconcile-run'`, 6},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	if _, err = w.begin(t.Context(), IAReconcileObject, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

// At or below the durable ACK floor the original is Retired: no result, no
// digest and no new effect, even while the physical rows still exist.
func TestIAReconcileObjectRetiredAfterAck(t *testing.T) {
	w, reg, p := iaReconcileObjectFixture(t)
	q := iaReconcileAcceptRequest(w)
	ref := iaReconcileAcceptReference(t, q, 2, 2)
	turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='reconcile-run'`)
	before := map[string]int64{}
	for _, table := range []string{"scan_command_receipts", "scan_reconcile_members"} {
		before[table] = iaEpisodeCount(t, w, `SELECT count(*) FROM `+table)
	}
	before["run"] = iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaReconcileTestRunID)
	turn, out, err = iaReconcileAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptRetired || out.digest != (ResultDigest{}) || out.result.facts.resultCount != 0 || len(out.result.facts.members) != 0 {
		t.Fatal("retired original", out, err)
	}
	turn.transaction.rollback(nil)
	for table, want := range before {
		var got int64
		if table == "run" {
			got = iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaReconcileTestRunID)
		} else {
			got = iaEpisodeCount(t, w, `SELECT count(*) FROM `+table)
		}
		if got != want {
			t.Fatal("retired replay wrote", table, got, want)
		}
	}
}

// A conflicting digest and a non-contiguous sequence are conflicts, never a
// fresh admission or a new receipt.
func TestIAReconcileObjectRejectsConflictingOriginal(t *testing.T) {
	w, reg, p := iaReconcileObjectFixture(t)
	q := iaReconcileAcceptRequest(w)
	ref := iaReconcileAcceptReference(t, q, 2, 2)
	changed := q
	changed.current.size = 43
	turn, out, err := iaReconcileAcceptRun(t, w, reg, p, changed, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("changed request digest", out, err)
	}
	turn.transaction.rollback(nil)
	gap := q
	gap.run.lastSequence, gap.run.acknowledgedSequence = 2, 2
	gapRef := iaReconcileAcceptReference(t, gap, 3, 4)
	turn, out, err = iaReconcileAcceptRun(t, w, reg, p, gap, gapRef)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("sequence gap", out, err)
	}
	turn.transaction.rollback(nil)
	// The current observation must name the one retained object unless an
	// explicit replacement supplies the prepared new id.
	foreign := q
	foreign.current.objectID = "object:other"
	turn, out, err = iaReconcileAcceptRun(t, w, reg, p, foreign, ref)
	if !errors.Is(err, ErrInvalid) || out.decision != 0 {
		t.Fatal("mismatched current object id", out, err)
	}
	turn.transaction.rollback(nil)
	// An over-bound revision is refused before any text is copied or hashed.
	oversize := q
	oversize.accepted.revision = strings.Repeat("r", iaReconcileMaxKeyBytes+1)
	oversize.current.revision = oversize.accepted.revision
	if _, err = newIAReconcileObjectCommand(CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}, oversize); !errors.Is(err, ErrInvalid) {
		t.Fatal("over-bound revision", err)
	}
	iaReconcileNoEffects(t, w)
}

// A second configured source in the same library must never widen the candidate
// window: the move stays scoped to the exact source, and a retained object from
// another source is not a placement of this file.
func TestIAReconcileObjectSourceLibrariesOverlapSafely(t *testing.T) {
	w, reg, p := iaReconcileObjectFixture(t)
	wc008NativeExec(t, w, `INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES('reconcile-source-2','reconcile-library','Reconcile two','/media/reconcile-two','/media/reconcile-two','local','reconcile-root-identity-2',0,0,'',1,'reconcile-incarnation-2',3,'ready',1,'local')`)
	wc008NativeExec(t, w, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(42,'reconcile-token-2','/media/reconcile/Reconcile/Link.mkv',42,7,'','','',0,0,0,7)`)
	wc008NativeExec(t, w, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object:reconcile:2','reconcile-source-2','reconcile-token-2','reconcile-incarnation-2','Reconcile/Link.mkv','ino:41','object-revision:1','{}',42,7,'reconcile-run','Reconcile','available')`)
	q := iaReconcileAcceptRequest(w)
	q.current.relativePath = iaReconcileTestMovedPath
	ref := iaReconcileAcceptReference(t, q, 2, 2)
	turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.members[0].disposition != "moved" {
		t.Fatal("overlapping source widened the move window", out, err)
	}
	turn.transaction.rollback(nil)
	// The retained object id is scoped by source: a foreign id at the same path
	// is not this library's retained object.
	foreign := iaReconcileAcceptRequest(w)
	foreign.accepted.objectID = "object:reconcile:2"
	foreign.current.objectID = "object:reconcile:2"
	foreignRef := iaReconcileAcceptReference(t, foreign, 2, 2)
	turn, out, err = iaReconcileAcceptRun(t, w, reg, p, foreign, foreignRef)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("foreign retained object", out, err)
	}
	turn.transaction.rollback(nil)
	iaReconcileNoEffects(t, w)
}

func TestIAReconcileObjectRollbackLeavesNoEffect(t *testing.T) {
	w, reg, p := iaReconcileObjectFixture(t)
	q := iaReconcileAcceptRequest(w)
	q.current.size = 43
	q.current.revision = "object-revision:2"
	ref := iaReconcileAcceptReference(t, q, 2, 2)
	turn, out, err := iaReconcileAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	iaReconcileNoEffects(t, w)
}
