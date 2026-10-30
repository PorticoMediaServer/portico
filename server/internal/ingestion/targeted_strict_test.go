package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func strictJob(t *testing.T, f *loopFixture, paths ...string) Job {
	t.Helper()
	out, err := f.s.ReportPathsStrict(context.Background(), paths, nil)
	if err != nil || len(out.Jobs) != 1 || len(out.Paths) != len(paths) {
		t.Fatalf("report: %+v %v", out, err)
	}
	job := out.Jobs[0]
	if job.ScanMode != "incremental" || f.count(`SELECT count(*) FROM inventory_runs WHERE job_id=? AND reported=2 AND scan_mode='incremental'`, job.ID) != 1 {
		t.Fatalf("not a durable strict incremental job: %+v", job)
	}
	if f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path='.'`, job.ID) != 0 || f.count(`SELECT count(*) FROM inventory_run_targets WHERE job_id=? AND relative_path='.'`, job.ID) != 0 || f.count(`SELECT count(*) FROM inventory_path_notices`) != 0 {
		t.Fatal("strict report left a root seed or deferred ordinary notice")
	}
	return job
}

func strictRefused(t *testing.T, f *loopFixture, reason StrictRefusalReason, paths ...string) {
	t.Helper()
	jobs, notices := f.count(`SELECT count(*) FROM jobs`), f.count(`SELECT count(*) FROM inventory_path_notices`)
	out, err := f.s.ReportPathsStrict(context.Background(), paths, nil)
	var refusal *StrictPathRefusal
	if !errors.As(err, &refusal) || refusal.Reason != reason || len(out.Jobs) != 0 || len(out.Paths) != 0 {
		t.Fatalf("wanted %s, got %+v %v", reason, out, err)
	}
	if f.count(`SELECT count(*) FROM jobs`) != jobs || f.count(`SELECT count(*) FROM inventory_path_notices`) != notices {
		t.Fatal("refused request mutated the queue")
	}
}

func TestStrictPathsNewAndExistingStayWithinRequestedFolders(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Old (2000).mkv", "Beta/Keep (2001).mkv")
	first := f.queue()
	f.finish(first)
	f.write("Alpha/New (2002).mkv", "new")
	f.write("New Folder/Leaf/New (2003).mkv", "new")
	job := strictJob(t, f, filepath.Join(f.root, "Alpha", "New (2002).mkv"), filepath.Join(f.root, "Alpha"), filepath.Join(f.root, "New Folder"))
	if f.count(`SELECT count(*) FROM inventory_run_targets WHERE job_id=?`, job.ID) != 2 {
		t.Fatal("duplicate folder did not coalesce")
	}
	// Repeated reports coalesce while the strict job has never started.
	if repeated := strictJob(t, f, filepath.Join(f.root, "Alpha")); repeated.ID != job.ID {
		t.Fatal("duplicate queued strict report created another job")
	}
	f.finish(job)
	if f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path NOT IN('Alpha','New Folder',?)`, job.ID, filepath.Join("New Folder", "Leaf")) != 0 {
		t.Fatal("discovery left the requested subtrees")
	}
	if f.count(`SELECT count(*) FROM inventory_objects WHERE source_id=? AND directory_path='Beta' AND seen_job=?`, f.source, first.ID) != 1 || f.count(`SELECT count(*) FROM inventory_objects WHERE source_id=? AND seen_job=?`, f.source, job.ID) != 3 {
		t.Fatal("new media was missed or an unreported sibling was visited")
	}
}

func TestStrictPathsDeletionUsesOnlyImmediateSurvivingParent(t *testing.T) {
	f := newLoopFixture(t, "movie", "Bundle/Deleted/Film (2000).mkv", "Bundle/Keep (2001).mkv", "Other/Keep (2002).mkv")
	f.finish(f.queue())
	deleted := filepath.Join(f.root, "Bundle", "Deleted")
	if err := os.RemoveAll(deleted); err != nil {
		t.Fatal(err)
	}
	job := strictJob(t, f, deleted)
	if f.count(`SELECT count(*) FROM inventory_run_targets WHERE job_id=? AND relative_path='Bundle'`, job.ID) != 1 {
		t.Fatal("wrong deletion parent")
	}
	f.finish(job)
	if f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path=? AND state='missing'`, filepath.Join("Bundle", "Deleted", "Film (2000).mkv")) != 1 {
		t.Fatal("deleted folder's media was not reconciled")
	}
	if f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path='Other'`, job.ID) != 0 {
		t.Fatal("visited unrelated folder")
	}
	// Known removed file: same immediate-parent rule.
	file := filepath.Join(f.root, "Bundle", "Keep (2001).mkv")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	job = strictJob(t, f, file)
	f.finish(job)
	if f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path=? AND state='missing'`, filepath.Join("Bundle", "Keep (2001).mkv")) != 1 {
		t.Fatal("removed file not reconciled")
	}
}

func TestStrictPathsRefuseUnknownOutsideRootsAndAtomicMixedBatch(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv", "Loose (2001).mkv")
	for _, tc := range []struct {
		path   string
		reason StrictRefusalReason
	}{
		{filepath.Join(f.root, "Alpha", "Unknown.mkv"), StrictUnknownPath},
		{filepath.Join(filepath.Dir(f.root), "outside", "film.mkv"), StrictOutsideLibraries},
		{f.root, StrictRootRefused},
		{f.root + string(filepath.Separator) + "Alpha" + string(filepath.Separator) + "..", StrictRootRefused},
		{filepath.Join(f.root, "Loose (2001).mkv"), StrictRootRefused},
		{"relative/file.mkv", StrictInvalidPath},
	} {
		t.Run(string(tc.reason)+tc.path, func(t *testing.T) { strictRefused(t, f, tc.reason, tc.path) })
	}
	strictRefused(t, f, StrictUnknownPath, filepath.Join(f.root, "Alpha"), filepath.Join(f.root, "Alpha", "Unknown.mkv"))
}

func TestStrictPathsRefuseDeletionWhoseParentWouldBeRootOrAlsoMissing(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Leaf/Film (2000).mkv", "Other/Film (2001).mkv")
	f.finish(f.queue())
	if err := os.RemoveAll(filepath.Join(f.root, "Alpha")); err != nil {
		t.Fatal(err)
	}
	strictRefused(t, f, StrictRootRefused, filepath.Join(f.root, "Alpha"))
	strictRefused(t, f, StrictProofUnavailable, filepath.Join(f.root, "Alpha", "Leaf"))
}

func TestStrictPathsNeverEscalateIncompleteOrOverdueSource(t *testing.T) {
	old := time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	for _, tc := range []struct{ name, complete, integrity, ordinaryMode string }{
		{"incomplete", "", "", "first-import"},
		{"overdue", old, old, "integrity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLoopFixture(t, "movie", "New/Film (2000).mkv", "Untouched/Film (2001).mkv")
			f.exec(`UPDATE library_sources SET last_complete_at=?,last_integrity_at=? WHERE id=?`, tc.complete, tc.integrity, f.source)
			job := strictJob(t, f, filepath.Join(f.root, "New"))
			f.finish(job)
			var complete, integrity string
			if err := f.db.QueryRow(`SELECT last_complete_at,last_integrity_at FROM library_sources WHERE id=?`, f.source).Scan(&complete, &integrity); err != nil {
				t.Fatal(err)
			}
			if complete != tc.complete || integrity != tc.integrity || f.count(`SELECT count(*) FROM inventory_objects WHERE directory_path='Untouched'`) != 0 {
				t.Fatal("partial scan claimed whole-source completion or visited sibling")
			}
			ordinary, err := f.s.QueueMode(context.Background(), f.library, f.source, "incremental", nil)
			if err != nil || ordinary.ScanMode != tc.ordinaryMode {
				t.Fatalf("ordinary behavior changed: %+v %v", ordinary, err)
			}
		})
	}
}

func TestStrictPathsDeferOrdinaryBusyPendingAndOverload(t *testing.T) {
	for _, count := range []int{0, 1, maxPendingNotices} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv")
			if count == 0 {
				f.queue()
			} else {
				f.exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO inventory_path_notices(source_id,relative_path,created_ms) SELECT ?,'Pending/'||x,1 FROM n`, count, f.source)
			}
			jobs := f.count(`SELECT count(*) FROM jobs`)
			out, err := f.s.ReportPathsStrict(context.Background(), []string{filepath.Join(f.root, "Alpha")}, nil)
			if err != nil || !out.Deferred || len(out.Jobs) != 0 || len(out.Paths) != 1 || out.Paths[0].Status != "deferred" {
				t.Fatalf("not accepted deferred work: %+v %v", out, err)
			}
			if f.count(`SELECT count(*) FROM jobs`) != jobs || f.count(`SELECT count(*) FROM inventory_path_notices`) != count || f.count(`SELECT count(*) FROM inventory_strict_intents`) != 1 {
				t.Fatal("deferred work fabricated a job, altered ordinary notices, or lost intent")
			}
			expected := "strict"
			if out.Paths[0].ScanScope != "" {
				t.Fatal("an ordinary backlog widened strict intent")
			}
			if f.count(`SELECT count(*) FROM inventory_strict_intents WHERE mode=?`, expected) != 1 {
				t.Fatal("wrong maintained intent scope")
			}
		})
	}
	f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv", "Next/Film (2001).mkv")
	job := strictJob(t, f, filepath.Join(f.root, "Alpha"))
	f.exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO inventory_run_targets(job_id,relative_path) SELECT ?,'Queued/'||x FROM n`, maxPendingNotices-1, job.ID)
	out, err := f.s.ReportPathsStrict(context.Background(), []string{filepath.Join(f.root, "Next")}, nil)
	if err != nil || !out.Deferred || len(out.Jobs) != 0 || out.Paths[0].ScanScope != "" {
		t.Fatalf("busy strict queue did not retain the exact pending folder: %+v %v", out, err)
	}
	if f.count(`SELECT count(*) FROM inventory_run_targets WHERE job_id=?`, job.ID) != maxPendingNotices || f.count(`SELECT count(*) FROM inventory_strict_intents WHERE mode='strict' AND relative_path='Next'`) != 1 {
		t.Fatal("deferral changed the existing strict job or lost the named folder")
	}
}

func TestStrictPathsRuntimeLossNeverWidensOrLeavesOrdinaryRetry(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Leaf/Film (2000).mkv", "Other/Film (2001).mkv")
	f.finish(f.queue())
	job := strictJob(t, f, filepath.Join(f.root, "Alpha", "Leaf"))
	if err := os.RemoveAll(filepath.Join(f.root, "Alpha", "Leaf")); err != nil {
		t.Fatal(err)
	}
	// Restart service state: strictness comes from the job, not request context.
	f.s = New(f.db, f.s.catalog, f.s.probe)
	if widened, err := f.s.widenTarget(context.Background(), job.ID, filepath.Join("Alpha", "Leaf")); err != nil || widened {
		t.Fatalf("strict target widened: %v %v", widened, err)
	}
	state := f.finish(job)
	if state.Status != "complete_with_warnings" {
		t.Fatalf("lost target needs an explicit warning: %+v", state)
	}
	if f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path IN('.','Alpha','Other')`, job.ID) != 0 {
		t.Fatal("runtime widening escaped original target")
	}
	if f.count(`SELECT count(*) FROM inventory_objects WHERE directory_path=? AND state='available'`, filepath.Join("Alpha", "Leaf")) != 1 {
		t.Fatal("unreadable target judged absent")
	}
	// Both exclusion/revision-change and unstable-page paths call the same
	// job-aware notice writer. It must never leave a deferred ordinary scan.
	if err := f.s.excludeDirectory(context.Background(), job.ID, filepath.Join("Alpha", "Leaf"), "directory_changed"); err == nil {
		t.Fatal("completed job should be fenced from new exclusion")
	}
	if f.count(`SELECT count(*) FROM inventory_path_notices`) != 0 {
		t.Fatal("strict failure produced widening ordinary work")
	}
}

func TestStrictPathsSuppressRunRetryAndDoubleCheckSuccessor(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv")
	f.exec(`INSERT INTO remote_sources(id,kind,name,root) VALUES('strict-settings','webdav','Fixture','fixture:strict')`)
	f.exec(`INSERT INTO remote_source_settings(source_id,revision,scan_interval_seconds,watch_for_changes,scan_on_start,full_recheck_days,seek_previews,chapter_images,waveforms,loudness,checksums,double_check_folders) VALUES('strict-settings',1,0,0,0,0,0,0,0,0,0,1)`)
	f.exec(`UPDATE library_sources SET remote_source_id='strict-settings' WHERE id=?`, f.source)
	job := strictJob(t, f, filepath.Join(f.root, "Alpha"))
	f.exec(`UPDATE jobs SET status='running' WHERE id=?`, job.ID)
	if err := f.s.excludeDirectory(context.Background(), job.ID, "Alpha", "directory_changed"); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT count(*) FROM inventory_scan_issues WHERE job_id=? AND reason='directory_changed' AND protect=1`, job.ID) != 1 || f.count(`SELECT count(*) FROM inventory_path_notices`) != 0 {
		t.Fatal("strict instability lost protection or made a broad retry")
	}
	if err := dbwork.WithWriteTxContext(context.Background(), f.db, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
		return finishScanTx(ctx, tx, job.ID, f.source)
	}); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT count(*) FROM jobs`) != 1 || f.count(`SELECT count(*) FROM inventory_source_active`) != 0 {
		t.Fatal("strict completion initiated a broad successor")
	}
}

func TestStrictPathsAuthorizationAndSourceRecheckAreAtomic(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv")
			denied, calls := errors.New("denied"), 0
			_, err := f.s.ReportPathsStrict(context.Background(), []string{filepath.Join(f.root, "Alpha")}, func(*sql.Tx, string) error {
				calls++
				if calls == failAt {
					return denied
				}
				return nil
			})
			if !errors.Is(err, denied) || calls != failAt || f.count(`SELECT count(*) FROM jobs`) != 0 || f.count(`SELECT count(*) FROM inventory_path_notices`) != 0 {
				t.Fatalf("authorization: %d %v", calls, err)
			}
		})
	}
	f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv")
	calls := 0
	_, err := f.s.ReportPathsStrict(context.Background(), []string{filepath.Join(f.root, "Alpha")}, func(tx *sql.Tx, _ string) error {
		calls++
		if calls == 2 {
			_, e := tx.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, f.source)
			return e
		}
		return nil
	})
	var refused *StrictPathRefusal
	if !errors.As(err, &refused) || refused.Reason != StrictSourceChanged || f.count(`SELECT count(*) FROM jobs`) != 0 {
		t.Fatalf("source replacement: %v", err)
	}
}

func TestStrictPathsAnalysisDoesNotQueueUnreportedSibling(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv", "Beta/Film (2001).mkv")
	f.finish(f.queue())
	// Stale work in Beta must not be imported into Alpha's strict analysis.
	f.exec(`UPDATE inventory_objects SET analysis_state='pending',analysis_revision='' WHERE source_id=?`, f.source)
	f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='[]' WHERE library_id=?`, f.library)
	job := strictJob(t, f, filepath.Join(f.root, "Alpha"))
	f.until(job, "analysis_enqueue")
	if err := f.s.enqueueAnalysisPage(context.Background(), job.ID, f.librarySource()); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT count(*) FROM inventory_analysis_queue q JOIN inventory_objects o ON o.id=q.object_id WHERE q.job_id=? AND o.directory_path='Beta'`, job.ID) != 0 {
		t.Fatal("strict job queued sibling analysis")
	}
	if f.count(`SELECT count(*) FROM inventory_analysis_queue q JOIN inventory_objects o ON o.id=q.object_id WHERE q.job_id=? AND o.directory_path='Alpha'`, job.ID) == 0 {
		t.Fatal("test did not exercise strict analysis selection")
	}
}

func TestStrictPathsRefuseSourcesThatFollowDirectoryLinks(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv")
	f.exec(`UPDATE library_sources SET follow_symlinks=1 WHERE id=?`, f.source)
	// Even a currently safe directory might contain a root/sibling link by
	// the time its worker runs. Refuse the source policy, not just one link.
	strictRefused(t, f, StrictSymlinksUnsupported, filepath.Join(f.root, "Alpha"))
}

func TestStrictPathsAuthorizeActualMatchedLibrary(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv")
	seen := []string{}
	denied := errors.New("library denied")
	_, err := f.s.ReportPathsStrict(context.Background(), []string{filepath.Join(f.root, "Alpha")}, func(_ *sql.Tx, library string) error {
		seen = append(seen, library)
		if library == f.library {
			return denied
		}
		return nil
	})
	if !errors.Is(err, denied) || len(seen) != 2 || seen[0] != "" || seen[1] != f.library || f.count(`SELECT count(*) FROM jobs`) != 0 {
		t.Fatalf("actual library was not authorized atomically: %v %v", seen, err)
	}
}

func TestStrictPathRefusalRetryClasses(t *testing.T) {
	for _, reason := range []StrictRefusalReason{StrictSourceBusy, StrictPendingReports, StrictOverloaded, StrictProofUnavailable, StrictSourceChanged} {
		if !(&StrictPathRefusal{Reason: reason}).Retryable() {
			t.Fatalf("transient reason is permanent: %s", reason)
		}
	}
	for _, reason := range []StrictRefusalReason{StrictInvalidPath, StrictOutsideLibraries, StrictRootRefused, StrictAmbiguousSource, StrictUnverifiedSource, StrictUnsupportedSource, StrictSymlinksUnsupported, StrictUnknownPath} {
		if (&StrictPathRefusal{Reason: reason}).Retryable() {
			t.Fatalf("scope refusal invites repeated retries: %s", reason)
		}
	}
}
