package writer

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const (
	iaCoverageTestLibraryID      int64 = 7
	iaCoverageTestLibraryKey           = "coverage-library"
	iaCoverageTestSourceID             = "coverage-source"
	iaCoverageTestRunID                = "coverage-run"
	iaCoverageTestRoot                 = "/media/coverage"
	iaCoverageTestDirectory            = "Coverage"
	iaCoverageTestIdentity             = "coverage-directory-identity"
	iaCoverageTestRevision             = "coverage-directory-revision"
	iaCoverageTestCursor               = "42"
	iaCoverageTestEmptyDirectory       = "Coverage/Empty"
	iaCoverageTestEmptyCursor          = "empty-cursor"
)

func TestIACoverageVerifyDescriptor(t *testing.T) {
	got, err := Describe(IACoverageVerify)
	want := Descriptor{ID: 121, Name: "ia_coverage_verify", Durability: Normal}
	if err != nil || got != want {
		t.Fatalf("Describe(121) = %+v, %v; want %+v", got, err, want)
	}
	if kind, err := iaEntryCommandKind(IACoverageVerify); err != nil || kind != "inventory_verify" {
		t.Fatalf("coverage kind = %q, %v", kind, err)
	}
	if class, err := commandClass(IACoverageVerify); err != nil || class != governor.Background {
		t.Fatalf("coverage class = %v, %v", class, err)
	}
}

// One private W frame, the frozen clock and the exact group C draft subset. The
// fixture seeds only a prepared source/run and sealed directories; it is not a
// qualified population, admission profile or product wiring. The skip-veto index
// overlay is test-only: with skippedIndex false the family runs on the same draft
// without the bounded access and must fail closed.
func iaCoverageVerifyFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	return iaCoverageVerifyFixtureOverlay(t, true)
}

func iaCoverageVerifyFixtureOverlay(t *testing.T, skippedIndex bool) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	requireC019Fixture(t)
	var ddlFiles []string
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
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`, nil},
		{`INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES('coverage-library','Coverage','movies','/media/coverage',1,1,1)`, nil},
		{`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'coverage-library','/media/coverage')`, nil},
		{`INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES('coverage-source','coverage-library','Coverage','/media/coverage','/media/coverage','local','coverage-root-identity',0,0,'',1,'coverage-incarnation',3,'ready',1,'local')`, nil},
		{`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('coverage-run','coverage-library','running',0,'')`, nil},
		{`INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES('coverage-run',7,'coverage-library','/media/coverage',0,?,?,'coverage-source',3,'coverage-incarnation','coverage-root-identity',4,'verifying','first-import',11,13,?,5,?,1,1)`,
			[]any{iaEpisodeBytes(3), make([]byte, 32), w.now().Add(time.Hour).UnixMilli(), iaEpisodeBytes(1)}},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('coverage-run','.','coverage-root-directory-identity','coverage-root-directory-revision','1','done',1,0)`, nil},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('coverage-run','Coverage','coverage-directory-identity','coverage-directory-revision','42','done',1,0)`, nil},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('coverage-run','Coverage/Sub','coverage-sub-directory-identity','coverage-sub-directory-revision','7','done',1,0)`, nil},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('coverage-run','Coverage/Empty','coverage-empty-directory-identity','coverage-empty-directory-revision','empty-cursor','done',1,0)`, nil},
	} {
		if _, err := w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}
	registry, err := stmttest.New(t, iaCoverageVerifyDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{registry, keeper}, proof
}

func iaCoverageAcceptRequest(w *executor) iaCoverageVerifyRequest {
	return iaCoverageVerifyRequest{
		scope:           inventoryScope{runID: iaCoverageTestRunID, libraryKey: iaCoverageTestLibraryKey, libraryID: iaCoverageTestLibraryID},
		source:          inventorySource{id: iaCoverageTestSourceID, incarnation: "coverage-incarnation", rootIdentity: "coverage-root-identity", resolvedRoot: []byte(iaCoverageTestRoot), generation: 3},
		run:             inventoryRun{revision: 5, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13, leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(), lastSequence: 1, acknowledgedSequence: 1, phase: "verifying", scanMode: "first-import"},
		directory:       iaCoverageWitness{relativePath: iaCoverageTestDirectory, identity: iaCoverageTestIdentity, revision: iaCoverageTestRevision, cursor: iaCoverageTestCursor, state: "done", pages: 1, skipped: 0},
		raw:             3,
		filtered:        1,
		accepted:        2,
		subtreeComplete: true,
		rawComplete:     true,
		stable:          true,
		pinsResolved:    true,
		phase:           "reconciling",
		authoritative:   0,
	}
}

func iaCoverageAcceptReference(t *testing.T, r iaCoverageVerifyRequest, sequence int64, operation byte) ReceiptReference {
	t.Helper()
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
	command, err := newIACoverageVerifyCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: command.digest}
}

func iaCoverageAcceptTurn(t *testing.T, w *executor, r *iaEntryRegistry, q iaCoverageVerifyRequest) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{purpose: IACoverageVerify, action: iaEntryApply, libraryID: q.scope.libraryID, libraryKey: q.scope.libraryKey})
}

func iaCoverageAcceptRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaCoverageVerifyRequest, ref ReceiptReference) (*commandTurn, iaCoverageAcceptOutcome, error) {
	t.Helper()
	turn, budget := iaCoverageAcceptTurn(t, w, r, q)
	out, err := acceptIACoverageVerify(t.Context(), turn, p, t.Context(), budget, ref, q)
	return turn, out, err
}

// A refused request writes no receipt, does not advance the run and leaves the
// sealed directory row untouched.
func iaCoverageNoEffects(t *testing.T, w *executor) {
	t.Helper()
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 0 {
		t.Fatal("refused request wrote a receipt", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaCoverageTestRunID); got != 5 {
		t.Fatal("refused request advanced the run", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path=? AND state='done' AND identity=? AND revision=?`,
		iaCoverageTestRunID, iaCoverageTestDirectory, iaCoverageTestIdentity, iaCoverageTestRevision); got != 1 {
		t.Fatal("refused request mutated the sealed directory", got)
	}
}

// A complete stable sealed subtree records its exact directory seal and raw
// progress, moves the run from verifying to reconciling, and replays the same
// result after live source/lease changes without any new effect.
func TestIACoverageVerifySealedSubtreeAndReplay(t *testing.T) {
	w, reg, p := iaCoverageVerifyFixture(t)
	q := iaCoverageAcceptRequest(w)
	ref := iaCoverageAcceptReference(t, q, 2, 2)
	turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.digest == (ResultDigest{}) {
		t.Fatal("fresh coverage verify", out, err)
	}
	facts := out.result.facts
	if facts.acceptedRevision != 6 || facts.appliedMS != w.now().UnixMilli() || facts.phase != "reconciling" ||
		facts.sourceHealth != "ready" || facts.directory != iaCoverageTestDirectory ||
		facts.identity != iaCoverageTestIdentity || facts.revision != iaCoverageTestRevision ||
		facts.processed != 3 || facts.analyzed != 2 || facts.warnings != 1 || facts.resultVersion != 1 {
		t.Fatal("coverage facts", facts)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_verify' AND result_count=0 AND ia_verified_directory='Coverage' AND CAST(ia_verified_identity AS TEXT)='coverage-directory-identity' AND CAST(ia_verified_revision AS TEXT)='coverage-directory-revision' AND ia_processed=3 AND ia_analyzed=2 AND ia_warnings=1 AND ia_finished_ms=0 AND ia_phase='reconciling'`, 1},
		{`SELECT count(*) FROM inventory_runs WHERE job_id='coverage-run' AND job_revision=6 AND last_command_sequence=2 AND acknowledged_command_sequence=1 AND phase='reconciling'`, 1},
		{`SELECT count(*) FROM inventory_directories WHERE job_id='coverage-run' AND relative_path='Coverage' AND state='done'`, 1},
	} {
		if got := iaEpisodeCount(t, w, check.query); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// Live source/lease changes never edit or filter the saved result.
	wc008NativeExec(t, w, `UPDATE library_sources SET generation=9,root='/moved',configured_root='/moved' WHERE id='coverage-source'`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET job_revision=99,job_lease_expires_ms=1 WHERE job_id='coverage-run'`)
	turn, replay, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest ||
		replay.result.facts.directory != facts.directory || replay.result.facts.processed != facts.processed ||
		replay.result.facts.acceptedRevision != facts.acceptedRevision {
		t.Fatal("saved coverage replay", replay, err)
	}
	// The replay frame still holds the single writer connection, so the
	// no-new-receipt check reads the open transaction itself.
	var receipts int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("replay minted a receipt", receipts, err)
	}
	turn.transaction.rollback(nil)
}

// A sealed empty directory with an accepted cursor schedules zero raw members and
// still records exact coverage: zero raw progress is not an absent window.
func TestIACoverageVerifyZeroMemberAcceptedCursor(t *testing.T) {
	w, reg, p := iaCoverageVerifyFixture(t)
	q := iaCoverageAcceptRequest(w)
	q.directory = iaCoverageWitness{relativePath: iaCoverageTestEmptyDirectory, identity: "coverage-empty-directory-identity",
		revision: "coverage-empty-directory-revision", cursor: iaCoverageTestEmptyCursor, state: "done", pages: 1}
	q.raw, q.filtered, q.accepted = 0, 0, 0
	ref := iaCoverageAcceptReference(t, q, 2, 2)
	turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.processed != 0 ||
		out.result.facts.analyzed != 0 || out.result.facts.warnings != 0 ||
		out.result.facts.directory != iaCoverageTestEmptyDirectory {
		t.Fatal("zero-member coverage", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_verify' AND ia_verified_directory='Coverage/Empty' AND ia_processed=0 AND ia_analyzed=0 AND ia_warnings=0`); got != 1 {
		t.Fatal("zero-member receipt", got)
	}
}

// A fully filtered raw listing is complete progress: raw entries are recorded and
// accepted evidence stays zero rather than pretending the directory was empty.
func TestIACoverageVerifyAllFilteredRawProgress(t *testing.T) {
	w, reg, p := iaCoverageVerifyFixture(t)
	q := iaCoverageAcceptRequest(w)
	q.raw, q.filtered, q.accepted = 4, 4, 0
	ref := iaCoverageAcceptReference(t, q, 2, 2)
	turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.processed != 4 ||
		out.result.facts.analyzed != 0 || out.result.facts.warnings != 4 {
		t.Fatal("all-filtered coverage", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_verify' AND ia_processed=4 AND ia_analyzed=0 AND ia_warnings=4`); got != 1 {
		t.Fatal("raw progress receipt", got)
	}
}

// An incomplete, truncated or unstable observation never authorizes coverage, and
// a refused entry is not a complete stable window.
func TestIACoverageVerifyIncompleteObservations(t *testing.T) {
	for name, mutate := range map[string]func(*iaCoverageVerifyRequest){
		"partial_subtree": func(q *iaCoverageVerifyRequest) { q.subtreeComplete = false },
		"truncated_raw":   func(q *iaCoverageVerifyRequest) { q.rawComplete = false },
		"unstable":        func(q *iaCoverageVerifyRequest) { q.stable = false },
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaCoverageVerifyFixture(t)
			q := iaCoverageAcceptRequest(w)
			mutate(&q)
			ref := iaCoverageAcceptReference(t, q, 2, 2)
			turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrResolveRequired) || out.decision != 0 || len(out.result.facts.directory) != 0 {
				t.Fatal("incomplete observation accepted", out, err)
			}
			turn.transaction.rollback(nil)
			iaCoverageNoEffects(t, w)
		})
	}
	t.Run("refused_progress", func(t *testing.T) {
		w, reg, p := iaCoverageVerifyFixture(t)
		q := iaCoverageAcceptRequest(w)
		q.raw, q.filtered, q.accepted, q.refused = 4, 1, 2, 1
		ref := iaCoverageAcceptReference(t, q, 2, 2)
		turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 {
			t.Fatal("refused progress accepted", out, err)
		}
		turn.transaction.rollback(nil)
		iaCoverageNoEffects(t, w)
	})
	t.Run("unresolved_mp10_pins", func(t *testing.T) {
		w, reg, p := iaCoverageVerifyFixture(t)
		q := iaCoverageAcceptRequest(w)
		q.pinsResolved = false
		ref := iaCoverageAcceptReference(t, q, 2, 2)
		turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, iaCoveragePinsUnresolved) || out.decision != 0 {
			t.Fatal("unresolved source pins accepted", out, err)
		}
		turn.transaction.rollback(nil)
		iaCoverageNoEffects(t, w)
	})
}

// Every fresh witness is re-proven after the receipt is Fresh. A changed library,
// source, run, lease, directory seal or subtree frontier refuses the whole unit.
func TestIACoverageVerifyFreshGates(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *executor, *iaCoverageVerifyRequest){
		"retired_library": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=7`)
		},
		"stale_source": func(_ *testing.T, _ *executor, q *iaCoverageVerifyRequest) { q.source.generation++ },
		"changed_lease": func(t *testing.T, w *executor, q *iaCoverageVerifyRequest) {
			q.run.leaseExpiresMS = w.now().UnixMilli()
		},
		"stale_run_revision": func(_ *testing.T, _ *executor, q *iaCoverageVerifyRequest) { q.run.revision++ },
		"changed_run_phase": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_runs SET phase='inventory' WHERE job_id='coverage-run'`)
		},
		"finished_run": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_runs SET finished_at='2026-11-08T00:00:00.000000000Z' WHERE job_id='coverage-run'`)
		},
		"partial_subtree": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('coverage-run','Coverage/Partial','partial-identity','partial-revision','1','pending',1,0)`)
		},
		"unreadable_subtree": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('coverage-run','Coverage/Blocked','blocked-identity','blocked-revision','1','blocked',1,0)`)
		},
		"protected_issue": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `INSERT INTO inventory_scan_issues(job_id,relative_path,reason,line,protect,warning) VALUES('coverage-run','Coverage/Secret','unreadable_entry',0,1,1)`)
		},
		"unstable_identity": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET identity='other-identity' WHERE job_id='coverage-run' AND relative_path='Coverage'`)
		},
		"unstable_revision": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET revision='other-revision' WHERE job_id='coverage-run' AND relative_path='Coverage'`)
		},
		"changed_cursor": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET cursor='43' WHERE job_id='coverage-run' AND relative_path='Coverage'`)
		},
		"changed_pages": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET pages=2 WHERE job_id='coverage-run' AND relative_path='Coverage'`)
		},
		"changed_skipped": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET skipped=1 WHERE job_id='coverage-run' AND relative_path='Coverage'`)
		},
		"not_done_directory": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET state='blocked' WHERE job_id='coverage-run' AND relative_path='Coverage'`)
		},
		"missing_directory": func(t *testing.T, w *executor, _ *iaCoverageVerifyRequest) {
			wc008NativeExec(t, w, `DELETE FROM inventory_directories WHERE job_id='coverage-run' AND relative_path='Coverage'`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaCoverageVerifyFixture(t)
			q := iaCoverageAcceptRequest(w)
			mutate(t, w, &q)
			ref := iaCoverageAcceptReference(t, q, 2, 2)
			turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.directory) != 0 {
				t.Fatal("fresh gate", out, err)
			}
			turn.transaction.rollback(nil)
			if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 0 {
				t.Fatal("gate wrote a receipt", got)
			}
			if got := iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaCoverageTestRunID); got != 5 {
				t.Fatal("gate advanced the run", got)
			}
		})
	}
}

// The subtree bound is exact: a pending sibling, a prefix collision and another
// run's frontier never block this directory's coverage.
func TestIACoverageVerifySubtreeScoping(t *testing.T) {
	w, reg, p := iaCoverageVerifyFixture(t)
	wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('coverage-run','Other','other-identity','other-revision','1','pending',1,0)`)
	wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('coverage-run','CoverageTwo','two-identity','two-revision','1','pending',1,0)`)
	wc008NativeExec(t, w, `INSERT INTO inventory_scan_issues(job_id,relative_path,reason,line,protect,warning) VALUES('coverage-run','Other/Secret','unreadable_entry',0,1,1)`)
	q := iaCoverageAcceptRequest(w)
	ref := iaCoverageAcceptReference(t, q, 2, 2)
	turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.directory != iaCoverageTestDirectory {
		t.Fatal("sibling frontier blocked coverage", out, err)
	}
	turn.transaction.rollback(nil)
}

// The skip veto is a native subtree point, not an EOF inference: a skip on the
// selected directory, the root or any descendant refuses coverage, while a skip in
// a sibling prefix, outside the subtree or under another job never blocks it.
func TestIACoverageVerifySkippedSubtreeVeto(t *testing.T) {
	seedDir := func(t *testing.T, w *executor, job, relativePath string, skipped int64) {
		t.Helper()
		wc008NativeExec(t, w, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES(?,?,?,?,?,'done',?,?)`,
			job, relativePath, relativePath+"-identity", relativePath+"-revision", "1", skipped, skipped)
	}
	root := func(skipped int64) iaCoverageWitness {
		return iaCoverageWitness{relativePath: ".", identity: "coverage-root-directory-identity",
			revision: "coverage-root-directory-revision", cursor: "1", state: "done", pages: 1, skipped: skipped}
	}
	ptr := func(w iaCoverageWitness) *iaCoverageWitness { return &w }
	for name, tc := range map[string]struct {
		directory *iaCoverageWitness
		seed      func(*testing.T, *executor)
		fresh     bool
	}{
		"selected_native_skip": {seed: func(t *testing.T, w *executor) {
			wc008NativeExec(t, w, `UPDATE inventory_directories SET skipped=1 WHERE job_id='coverage-run' AND relative_path='Coverage'`)
		}},
		"root_selected_skip": {
			directory: ptr(root(0)),
			seed: func(t *testing.T, w *executor) {
				wc008NativeExec(t, w, `UPDATE inventory_directories SET skipped=1 WHERE job_id='coverage-run' AND relative_path='.'`)
			}},
		"first_descendant_skip": {seed: func(t *testing.T, w *executor) { seedDir(t, w, "coverage-run", "Coverage/000", 1) }},
		"end_descendant_skip":   {seed: func(t *testing.T, w *executor) { seedDir(t, w, "coverage-run", "Coverage/zzz", 1) }},
		"deep_descendant_skip":  {seed: func(t *testing.T, w *executor) { seedDir(t, w, "coverage-run", "Coverage/Sub/Deep/Leaf", 1) }},
		"root_clean_deep_skip": {directory: ptr(root(0)),
			seed: func(t *testing.T, w *executor) { seedDir(t, w, "coverage-run", "Coverage/Sub/Deep", 1) }},
		"sibling_prefix_skip": {fresh: true, seed: func(t *testing.T, w *executor) {
			seedDir(t, w, "coverage-run", "CoverageTwo", 1)
			seedDir(t, w, "coverage-run", "Coverages", 1)
		}},
		"out_of_subtree_skip": {fresh: true, seed: func(t *testing.T, w *executor) { seedDir(t, w, "coverage-run", "Other/Skip", 1) }},
		"other_job_skip": {fresh: true, seed: func(t *testing.T, w *executor) {
			wc008NativeExec(t, w, `INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('coverage-other','coverage-library','running',0,'')`)
			seedDir(t, w, "coverage-other", "Coverage/Skipped", 1)
		}},
		"large_clean_subtree": {fresh: true, seed: func(t *testing.T, w *executor) {
			for i := 0; i < 64; i++ {
				seedDir(t, w, "coverage-run", "Coverage/Bulk/"+strconv.Itoa(i), 0)
			}
		}},
		"zero_skip_empty_subtree": {fresh: true, directory: &iaCoverageWitness{relativePath: iaCoverageTestEmptyDirectory,
			identity: "coverage-empty-directory-identity", revision: "coverage-empty-directory-revision",
			cursor: iaCoverageTestEmptyCursor, state: "done", pages: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaCoverageVerifyFixture(t)
			q := iaCoverageAcceptRequest(w)
			if tc.directory != nil {
				q.directory = *tc.directory
				if q.directory.relativePath == iaCoverageTestEmptyDirectory {
					q.raw, q.filtered, q.accepted = 0, 0, 0
				}
			}
			if tc.seed != nil {
				tc.seed(t, w)
			}
			ref := iaCoverageAcceptReference(t, q, 2, 2)
			turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
			if tc.fresh {
				if err != nil || out.decision != receiptFresh {
					t.Fatal("skip-free subtree refused", out, err)
				}
				turn.transaction.rollback(nil)
				return
			}
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) ||
				len(out.result.facts.directory) != 0 {
				t.Fatal("skip authorized absence", out, err)
			}
			turn.transaction.rollback(nil)
			iaCoverageNoEffects(t, w)
		})
	}
	// A prepared witness that already carries a skip is not a valid absence proof,
	// at the selected directory or at the root.
	for name, directory := range map[string]iaCoverageWitness{
		"prepared_selected_skip": {relativePath: "Coverage", identity: iaCoverageTestIdentity,
			revision: iaCoverageTestRevision, cursor: iaCoverageTestCursor, state: "done", pages: 1, skipped: 1},
		"prepared_root_skip": root(1),
	} {
		t.Run(name, func(t *testing.T) {
			q := iaCoverageVerifyRequest{directory: directory}
			if _, err := newIACoverageVerifyCommand(CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}, q); !errors.Is(err, ErrInvalid) {
				t.Fatal("prepared skip accepted", err)
			}
		})
	}
}

// Absence verification fails closed when the bounded skipped-directory access
// cannot be prepared: the same draft without the test-only index yields a typed
// hold with zero effects, never a successful empty window and never a raw crash.
func TestIACoverageVerifySkippedAccessUnavailableHolds(t *testing.T) {
	w, reg, p := iaCoverageVerifyFixtureOverlay(t, false)
	q := iaCoverageAcceptRequest(w)
	ref := iaCoverageAcceptReference(t, q, 2, 2)
	turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
	if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, iaCoverageSkippedUnavailable) ||
		out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.directory) != 0 {
		t.Fatal("missing bounded access did not hold", out, err)
	}
	turn.transaction.rollback(nil)
	iaCoverageNoEffects(t, w)
}

// Current authority precedes every retained read: a forged issuer, a demoted
// owner and a cancelled caller lifetime all deny with zero public output.
func TestIACoverageVerifyAuthorityCells(t *testing.T) {
	t.Run("forged_issuer", func(t *testing.T) {
		w, reg, p := iaCoverageVerifyFixture(t)
		q := iaCoverageAcceptRequest(w)
		ref := iaCoverageAcceptReference(t, q, 2, 2)
		fake := *p
		fake.witness.issuer.family = "forged"
		turn, out, err := iaCoverageAcceptRun(t, w, reg, &fake, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.directory) != 0 {
			t.Fatal("forged issuer", out, err)
		}
		turn.transaction.rollback(nil)
		iaCoverageNoEffects(t, w)
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
				w, reg, p := iaCoverageVerifyFixture(t)
				wc008NativeExec(t, w, mutation)
				q := iaCoverageAcceptRequest(w)
				ref := iaCoverageAcceptReference(t, q, 2, 2)
				turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
				if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.directory) != 0 {
					t.Fatal("stale authority", out, err)
				}
				turn.transaction.rollback(nil)
				iaCoverageNoEffects(t, w)
			})
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, reg, p := iaCoverageVerifyFixture(t)
		q := iaCoverageAcceptRequest(w)
		ref := iaCoverageAcceptReference(t, q, 2, 2)
		turn, budget := iaCoverageAcceptTurn(t, w, reg, q)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := acceptIACoverageVerify(cancelled, turn, p, cancelled, budget, ref, q)
		if err == nil || out.decision != 0 || len(out.result.facts.directory) != 0 {
			t.Fatal("cancelled lifetime", out, err)
		}
		turn.transaction.rollback(nil)
		iaCoverageNoEffects(t, w)
	})
}

// Current authority is re-observed after the sealed directory/frontier reads and
// again after the receipt write. A same-turn revocation must deny the fresh result.
func TestIACoverageVerifyLateAuthorityRechecks(t *testing.T) {
	t.Run("before_writes", func(t *testing.T) {
		w, reg, p := iaCoverageVerifyFixture(t)
		state := installIAEntryQueryRevocation(t, w, "FROM inventory_directories")
		q := iaCoverageAcceptRequest(w)
		ref := iaCoverageAcceptReference(t, q, 2, 2)
		turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
		if !state.fired || state.err != nil {
			t.Fatal("directory-close mutation did not run", state.err)
		}
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.directory) != 0 {
			t.Fatal("pre-write recheck released a stale result", out, err)
		}
		turn.transaction.rollback(nil)
		iaCoverageNoEffects(t, w)
	})
	t.Run("after_writes", func(t *testing.T) {
		w, reg, p := iaCoverageVerifyFixture(t)
		wc008NativeExec(t, w, `CREATE TRIGGER ia_coverage_late_revocation AFTER INSERT ON scan_command_receipts BEGIN UPDATE identity_security_fences SET denied=1; END`)
		q := iaCoverageAcceptRequest(w)
		ref := iaCoverageAcceptReference(t, q, 2, 2)
		turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.directory) != 0 {
			t.Fatal("post-write recheck released a stale result", out, err)
		}
		var written int64
		if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts`).Scan(&written); err != nil || written != 1 {
			t.Fatal("receipt write not reached before the final recheck", written, err)
		}
		turn.transaction.rollback(nil)
		iaCoverageNoEffects(t, w)
	})
}

// A saved receipt must be exact before it is served: the directory seal, the raw
// progress invariant and the result digest are all checked.
func TestIACoverageVerifyReplayRejectsTamperedReceipt(t *testing.T) {
	for name, mutate := range map[string]string{
		"result_digest":      `UPDATE scan_command_receipts SET result_digest=zeroblob(32)`,
		"verified_directory": `UPDATE scan_command_receipts SET ia_verified_directory='Coverage/Other'`,
		"verified_identity":  `UPDATE scan_command_receipts SET ia_verified_identity=x'00'`,
		"progress_invariant": `UPDATE scan_command_receipts SET ia_processed=9`,
		"before_key":         `UPDATE scan_command_receipts SET ia_before_key='Coverage/Other'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaCoverageVerifyFixture(t)
			q := iaCoverageAcceptRequest(w)
			ref := iaCoverageAcceptReference(t, q, 2, 2)
			turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
			if err != nil || out.decision != receiptFresh {
				t.Fatal(out, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			wc008NativeExec(t, w, mutate)
			turn, replay, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrResolveRequired) || replay.decision != 0 || replay.digest != (ResultDigest{}) || len(replay.result.facts.directory) != 0 {
				t.Fatal("tampered receipt served", replay, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

// A lost COMMIT reply is never success. The committed file is read back through
// a FRESH read-only connection, never the fenced writer pool.
func TestIACoverageVerifyUnknownCommitIsNotSuccess(t *testing.T) {
	w, reg, p := iaCoverageVerifyFixture(t)
	path := iaEpisodePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	q := iaCoverageAcceptRequest(w)
	ref := iaCoverageAcceptReference(t, q, 2, 2)
	turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.processed != 3 {
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
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_verify' AND result_count=0 AND ia_verified_directory='Coverage'`, 1},
		{`SELECT job_revision FROM inventory_runs WHERE job_id='coverage-run'`, 6},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	if _, err = w.begin(t.Context(), IACoverageVerify, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

// At or below the durable ACK floor the original is Retired: no result, no digest
// and no new effect, even while the physical receipt still exists.
func TestIACoverageVerifyRetiredAfterAck(t *testing.T) {
	w, reg, p := iaCoverageVerifyFixture(t)
	q := iaCoverageAcceptRequest(w)
	ref := iaCoverageAcceptReference(t, q, 2, 2)
	turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='coverage-run'`)
	before := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`)
	beforeRevision := iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaCoverageTestRunID)
	turn, out, err = iaCoverageAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptRetired || out.digest != (ResultDigest{}) || len(out.result.facts.directory) != 0 {
		t.Fatal("retired original", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != before {
		t.Fatal("retired replay wrote a receipt", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT job_revision FROM inventory_runs WHERE job_id=?`, iaCoverageTestRunID); got != beforeRevision {
		t.Fatal("retired replay advanced the run", got)
	}
}

// A conflicting digest, a non-contiguous sequence and malformed original bounds
// are conflicts or invalid input, never a fresh admission or a new receipt.
func TestIACoverageVerifyRejectsConflictingOriginal(t *testing.T) {
	w, reg, p := iaCoverageVerifyFixture(t)
	q := iaCoverageAcceptRequest(w)
	ref := iaCoverageAcceptReference(t, q, 2, 2)
	changed := q
	changed.raw++
	changed.accepted++
	turn, out, err := iaCoverageAcceptRun(t, w, reg, p, changed, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("changed request digest", out, err)
	}
	turn.transaction.rollback(nil)
	gap := q
	gap.run.lastSequence, gap.run.acknowledgedSequence = 2, 2
	gapRef := iaCoverageAcceptReference(t, gap, 3, 4)
	turn, out, err = iaCoverageAcceptRun(t, w, reg, p, gap, gapRef)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("sequence gap", out, err)
	}
	turn.transaction.rollback(nil)
	// The directory seal must be an exact done row with a native identity,
	// revision and native relative path.
	for name, corrupt := range map[string]func(*iaCoverageVerifyRequest){
		"over_bound_identity": func(q *iaCoverageVerifyRequest) { q.directory.identity = strings.Repeat("i", iaCoverageMaxKeyBytes+1) },
		"pending_state":       func(q *iaCoverageVerifyRequest) { q.directory.state = "pending" },
		"escaping_path":       func(q *iaCoverageVerifyRequest) { q.directory.relativePath = "../escape" },
		"progress_overflow":   func(q *iaCoverageVerifyRequest) { q.accepted = q.raw + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := q
			corrupt(&bad)
			if _, err = newIACoverageVerifyCommand(CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}, bad); !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed original accepted", err)
			}
		})
	}
	iaCoverageNoEffects(t, w)
}

func TestIACoverageVerifyRollbackLeavesNoEffect(t *testing.T) {
	w, reg, p := iaCoverageVerifyFixture(t)
	q := iaCoverageAcceptRequest(w)
	ref := iaCoverageAcceptReference(t, q, 2, 2)
	turn, out, err := iaCoverageAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	iaCoverageNoEffects(t, w)
}
