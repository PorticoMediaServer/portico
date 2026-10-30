package ingestion

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func (f *loopFixture) pendingQueue(job string) map[string]bool {
	f.t.Helper()
	rows, err := f.db.Query(`SELECT o.relative_path FROM inventory_analysis_queue q JOIN inventory_objects o ON o.id=q.object_id WHERE q.job_id=? AND q.state='pending'`, job)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			f.t.Fatal(err)
		}
		out[filepath.ToSlash(path)] = true
	}
	return out
}

func (f *loopFixture) activeJob() string {
	f.t.Helper()
	var job string
	if err := f.db.QueryRow(`SELECT job_id FROM inventory_source_active WHERE source_id=?`, f.source).Scan(&job); err != nil {
		f.t.Fatal(err)
	}
	return job
}

// TestReportedFolderIsListedBeforeTheAnalysisBacklogDrains is S4: a file added
// while a run works through its analysis backlog is listed by a targeted run
// once the backlog's run has analysed for the handover floor, not after the
// whole backlog; the floor itself is respected; and the backlog the old run had
// not reached is queued again by the new run rather than dropped.
func TestReportedFolderIsListedBeforeTheAnalysisBacklogDrains(t *testing.T) {
	f := newLoopFixture(t, "movie", "Old/Alpha (2000).mkv", "Old/Beta (2001).mkv", "Old/Gamma (2002).mkv")
	f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='["probe"]' WHERE library_id=?`, f.library)
	old := reportHandoverAfter
	defer func() { reportHandoverAfter = old }()
	first := f.queue()
	f.until(first, "basic_pending")
	backlog := f.pendingQueue(first.ID)
	if len(backlog) != 3 {
		t.Fatalf("backlog before the report: %v", backlog)
	}
	f.write("New/Delta (2003).mkv", "media")
	if _, err := f.s.ReportPaths(context.Background(), []string{filepath.Join(f.root, "New")}, nil); err != nil {
		t.Fatal(err)
	}
	if f.activeJob() != first.ID {
		t.Fatal("a report started a second run beside the active one")
	}
	newFile := `SELECT count(*) FROM inventory_objects WHERE relative_path=? AND retired=0`
	delta := filepath.FromSlash("New/Delta (2003).mkv")

	// Under the floor the run keeps analysing and the report keeps waiting.
	reportHandoverAfter = time.Hour
	f.s.processQuantum(context.Background(), first.ID, f.library)
	if f.activeJob() != first.ID || f.count(`SELECT count(*) FROM inventory_path_notices WHERE source_id=?`, f.source) == 0 {
		t.Fatal("the run handed over before it had analysed for the floor")
	}
	if f.count(newFile, delta) != 0 {
		t.Fatal("the new file was listed without a run for it")
	}
	unreached := f.pendingQueue(first.ID)

	// Past the floor the next analysis quantum hands the source over.
	reportHandoverAfter = 0
	f.s.processQuantum(context.Background(), first.ID, f.library)
	state, err := f.s.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "complete" && state.Status != "complete_with_warnings" {
		t.Fatalf("the handing-over run ended as %+v", state)
	}
	second := f.activeJob()
	if second == first.ID {
		t.Fatal("no run took the source for the waiting report")
	}
	// The reports become a targeted run, or a full run when one is due anyway
	// (startTargetedTx); either lists the reported folder.
	targeted := f.count(`SELECT count(*) FROM inventory_run_targets WHERE job_id=? AND relative_path='New'`, second) == 1
	full := f.count(`SELECT count(*) FROM inventory_run_targets WHERE job_id=?`, second) == 0 && f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path='.'`, second) == 1
	if !targeted && !full {
		t.Fatal("the new run covers neither the reported folder nor the whole source")
	}
	if n := f.count(`SELECT count(*) FROM inventory_runs WHERE job_id=? AND reported=1`, second); n != 1 {
		t.Fatal("the new run was not started for the reports")
	}
	if n := f.count(`SELECT count(*) FROM inventory_path_notices WHERE source_id=?`, f.source); n != 0 {
		t.Fatalf("%d reports still wait after the handover", n)
	}
	next, err := f.s.Get(second)
	if err != nil {
		t.Fatal(err)
	}
	f.until(next, "basic_pending")
	if f.count(newFile, delta) != 1 {
		t.Fatal("the reported file was not listed by the targeted run")
	}
	queued := f.pendingQueue(second)
	if !queued["New/Delta (2003).mkv"] {
		t.Fatalf("the new file is not queued for analysis: %v", queued)
	}
	for path := range unreached {
		if !queued[path] {
			t.Fatalf("the old run's unanalysed %s was dropped by the handover: %v", path, queued)
		}
	}
}

// TestRetryDelayedObjectDoesNotHoldTheAnalysisQueue is the S4 head-of-line
// case: an object whose stage waits for a retry is passed over, later objects
// are analysed meanwhile, and a queue whose every object waits is not finished.
func TestRetryDelayedObjectDoesNotHoldTheAnalysisQueue(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha (2000).mkv", "Beta (2001).mkv", "Gamma (2002).mkv")
	f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='["probe"]' WHERE library_id=?`, f.library)
	job := f.queue()
	f.until(job, "basic_pending")
	ids := []string{}
	rows, err := f.db.Query(`SELECT object_id,revision,policy_revision FROM inventory_analysis_queue WHERE job_id=? AND state='pending' ORDER BY object_id`, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	type queued struct {
		id, revision string
		policy       int64
	}
	all := []queued{}
	for rows.Next() {
		var q queued
		if err = rows.Scan(&q.id, &q.revision, &q.policy); err != nil {
			t.Fatal(err)
		}
		all = append(all, q)
		ids = append(ids, q.id)
	}
	rows.Close()
	if len(all) != 3 {
		t.Fatalf("queue: %v", ids)
	}
	wait := func(q queued) {
		f.exec(`INSERT INTO analysis_stage_runs(job_id,object_id,source_revision,policy_revision,stage,algorithm,state,attempt,next_ms,updated_ms) VALUES(?,?,?,?,'checksum','test','retry_wait',1,?,?)`, job.ID, q.id, q.revision, q.policy, time.Now().Add(time.Hour).UnixMilli(), time.Now().UnixMilli())
	}
	wait(all[0])
	f.s.processQuantum(context.Background(), job.ID, f.library)
	if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=? AND object_id=? AND state='pending'`, job.ID, all[0].id); n != 1 {
		t.Fatal("the delayed object was analysed before it was due")
	}
	if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=? AND state='pending'`, job.ID); n != 2 {
		t.Fatalf("a later object did not advance past the delayed one: %d pending", n)
	}
	f.s.processQuantum(context.Background(), job.ID, f.library)
	if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=? AND state='pending'`, job.ID); n != 1 {
		t.Fatalf("only the delayed object should remain: %d pending", n)
	}
	// Only the delayed object remains: the run waits for it, it does not end.
	f.s.processQuantum(context.Background(), job.ID, f.library)
	if state, err := f.s.Get(job.ID); err != nil || state.Status != "running" {
		t.Fatalf("a queue that only waits for a retry finished the run: %+v %v", state, err)
	}
}
