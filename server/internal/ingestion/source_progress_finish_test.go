package ingestion

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// finishFixture installs one running inventory job for a real loop fixture.
func finishFixture(t *testing.T, job string, warnings int) *loopFixture {
	t.Helper()
	f := newLoopFixture(t, "movie")
	at := time.Now().UTC().Format(time.RFC3339Nano)
	f.exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running',?)`, job, f.library, at)
	f.exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,warnings) SELECT ?,id,generation,incarnation,root_identity,1,'inventory',? FROM library_sources WHERE id=?`, job, warnings, f.source)
	f.exec(`INSERT INTO inventory_source_active(source_id,job_id) VALUES(?,?) ON CONFLICT(source_id) DO UPDATE SET job_id=excluded.job_id`, f.source, job)
	return f
}

// finishTx runs the real finish and reports whether it queued a successor
// (second-pass) job alongside the finishing one.
func finishTx(t *testing.T, f *loopFixture, job string) (bool, error) {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		return finishScanTx(context.Background(), tx, job, f.source)
	})
	if err != nil {
		return false, err
	}
	var successors int
	err = f.db.QueryRow(`SELECT count(*) FROM jobs WHERE library_id=? AND id<>?`, f.library, job).Scan(&successors)
	return successors > 0, err
}

// TestFinishScanSharesCapturedTimestampAndKeepsHealthAuthority checks the split
// finish: the health statement still runs on library_sources (so the authority
// generation moves) while the progress value is the one captured at entry and
// is shared with the run and scan_completed rows.
func TestFinishScanSharesCapturedTimestampAndKeepsHealthAuthority(t *testing.T) {
	for _, test := range []struct {
		name       string
		warnings   int
		wantStatus string
		wantHealth string
	}{
		{"clean", 0, "complete", "healthy"},
		{"warnings", 1, "complete_with_warnings", "degraded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := finishFixture(t, "finish-"+test.name, test.warnings)
			before := dbwork.AuthorityGeneration()
			followUp, err := finishTx(t, f, "finish-"+test.name)
			if err != nil {
				t.Fatal(err)
			}
			if followUp {
				t.Fatal("a local source without a second-pass request asked for a follow-up")
			}
			var status, finishedAt, progress, scanCompleted, health string
			if err = f.db.QueryRow(`SELECT status FROM jobs WHERE id=?`, "finish-"+test.name).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if err = f.db.QueryRow(`SELECT finished_at FROM inventory_runs WHERE job_id=?`, "finish-"+test.name).Scan(&finishedAt); err != nil {
				t.Fatal(err)
			}
			if err = f.db.QueryRow(`SELECT last_progress_at FROM library_source_progress WHERE source_id=?`, f.source).Scan(&progress); err != nil {
				t.Fatal(err)
			}
			if err = f.db.QueryRow(`SELECT created_at FROM inventory_changes WHERE job_id=? AND kind='scan_completed'`, "finish-"+test.name).Scan(&scanCompleted); err != nil {
				t.Fatal(err)
			}
			source, err := f.s.catalog.LibrarySource(context.Background(), f.source)
			if err != nil {
				t.Fatal(err)
			}
			health = source.Health
			if status != test.wantStatus || health != test.wantHealth {
				t.Fatalf("status/health: %q/%q want %q/%q", status, health, test.wantStatus, test.wantHealth)
			}
			if progress == "" || finishedAt != progress || scanCompleted != progress || source.LastProgressAt != progress {
				t.Fatalf("finish timestamp split: progress=%q finished=%q scan_completed=%q projection=%q", progress, finishedAt, scanCompleted, source.LastProgressAt)
			}
			if _, err = time.Parse(time.RFC3339Nano, progress); err != nil {
				t.Fatalf("finish progress is not the original formatted value: %q %v", progress, err)
			}
			if got := dbwork.AuthorityGeneration(); got <= before {
				t.Fatal("the real source health UPDATE no longer moves AuthorityGeneration")
			}
			var active int
			if err = f.db.QueryRow(`SELECT count(*) FROM inventory_source_active WHERE job_id=?`, "finish-"+test.name).Scan(&active); err != nil || active != 0 {
				t.Fatalf("active source row: %d %v", active, err)
			}
		})
	}
}

// TestFinishScanClearsPendingFollowUpWithoutLosingProgress keeps the existing
// second-pass bookkeeping: the pending marker is cleared, no successor job is
// queued, and the split progress/health still commit together.
func TestFinishScanClearsPendingFollowUpWithoutLosingProgress(t *testing.T) {
	f := finishFixture(t, "finish-follow-up", 0)
	f.exec(`UPDATE library_sources SET double_check_job='prior-pass' WHERE id=?`, f.source)
	followUp, err := finishTx(t, f, "finish-follow-up")
	if err != nil {
		t.Fatal(err)
	}
	if followUp {
		t.Fatal("clearing a pending marker asked for another follow-up")
	}
	var marker, progress string
	if err = f.db.QueryRow(`SELECT double_check_job FROM library_sources WHERE id=?`, f.source).Scan(&marker); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT last_progress_at FROM library_source_progress WHERE source_id=?`, f.source).Scan(&progress); err != nil {
		t.Fatal(err)
	}
	if marker != "" {
		t.Fatalf("follow-up marker not cleared: %q", marker)
	}
	if progress == "" {
		t.Fatal("follow-up completion lost progress")
	}
}

// TestScanCompletionProgressMatchesRunTimestamp exercises the production scan
// loop: the final page/finish progress is the run's finished_at and the
// scan_completed row's value, and the source health authority write remains.
func TestScanCompletionProgressMatchesRunTimestamp(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	before := dbwork.AuthorityGeneration()
	job := f.queue()
	if state := f.finish(job); state.Status != "complete" {
		t.Fatalf("scan status: %+v", state)
	}
	source, err := f.s.catalog.LibrarySource(context.Background(), f.source)
	if err != nil {
		t.Fatal(err)
	}
	var finishedAt, scanCompleted string
	if err = f.db.QueryRow(`SELECT finished_at FROM inventory_runs WHERE job_id=?`, job.ID).Scan(&finishedAt); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT created_at FROM inventory_changes WHERE job_id=? AND kind='scan_completed'`, job.ID).Scan(&scanCompleted); err != nil {
		t.Fatal(err)
	}
	if source.LastProgressAt == "" || source.LastProgressAt != finishedAt || source.LastProgressAt != scanCompleted {
		t.Fatalf("completion timestamps: progress=%q finished=%q scan_completed=%q", source.LastProgressAt, finishedAt, scanCompleted)
	}
	if source.Health != "healthy" {
		t.Fatalf("healthy completion left source health %q", source.Health)
	}
	if got := dbwork.AuthorityGeneration(); got <= before {
		t.Fatal("a real scan completion no longer moves AuthorityGeneration")
	}
}
