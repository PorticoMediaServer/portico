package ingestion

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestFinishingScanDurablyQueuesExactlyOneFollowUp(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("movies", "Movies", "movie", t.TempDir())
	c.Exec(`INSERT INTO remote_sources(id,kind,name,root) VALUES('remote','webdav','Fixture','fixture:remote')`)
	c.Exec(`INSERT INTO remote_source_settings(source_id,revision,scan_interval_seconds,watch_for_changes,scan_on_start,full_recheck_days,seek_previews,chapter_images,waveforms,loudness,checksums,double_check_folders) VALUES('remote',1,0,0,0,0,0,0,0,0,0,1)`)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	c.Exec(`UPDATE library_sources SET remote_source_id='remote',last_complete_at=?,last_integrity_at=? WHERE id='movies'`, now, now)
	var first Job
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		first, err = queueSourceTx(ctx, tx, "movies", false)
		return err
	})
	c.Exec(`UPDATE jobs SET status='running' WHERE id=?`, first.ID)
	c.Exec(`INSERT INTO inventory_changes(source_id,job_id,kind,created_at) VALUES('movies',?,'observed',?)`, first.ID, now)
	finish := func(job string) error {
		return dbwork.WithWriteTxContext(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
			return finishScanTx(ctx, tx, job, "movies")
		})
	}
	// Failure to persist the successor must roll back completion as well.
	c.Exec(`CREATE TRIGGER refuse_follow_up BEFORE INSERT ON jobs BEGIN SELECT RAISE(ABORT,'injected successor failure'); END`)
	if err := finish(first.ID); err == nil {
		t.Fatal("successor failure was ignored")
	}
	var status, marker, active string
	if err := c.DB.QueryRow(`SELECT j.status,s.double_check_job,a.job_id FROM jobs j JOIN library_sources s ON s.id='movies' JOIN inventory_source_active a ON a.source_id=s.id WHERE j.id=?`, first.ID).Scan(&status, &marker, &active); err != nil {
		t.Fatal(err)
	}
	if status != "running" || marker != "" || active != first.ID {
		t.Fatalf("partial completion: status=%s marker=%s active=%s", status, marker, active)
	}
	c.Exec(`DROP TRIGGER refuse_follow_up`)
	if err := finish(first.ID); err != nil {
		t.Fatal(err)
	}
	// No post-commit QueueMode call is made: a fresh worker can read its work
	// immediately from the database, including with a zero scan interval.
	var next, mode string
	if err := c.DB.QueryRow(`SELECT j.id,j.status,r.scan_mode,s.double_check_job FROM inventory_source_active a JOIN jobs j ON j.id=a.job_id JOIN inventory_runs r ON r.job_id=j.id JOIN library_sources s ON s.id=a.source_id WHERE a.source_id='movies'`).Scan(&next, &status, &mode, &marker); err != nil {
		t.Fatal(err)
	}
	if next == first.ID || status != "queued" || mode != "incremental" || marker != first.ID {
		t.Fatalf("successor is not durable: id=%s status=%s mode=%s marker=%s", next, status, mode, marker)
	}
	c.Exec(`UPDATE jobs SET status='running' WHERE id=?`, next)
	c.Exec(`INSERT INTO inventory_changes(source_id,job_id,kind,created_at) VALUES('movies',?,'observed',?)`, next, now)
	if err := finish(next); err != nil {
		t.Fatal(err)
	}
	var jobs, activeJobs int
	if err := c.DB.QueryRow(`SELECT count(*) FROM jobs WHERE library_id='movies'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT count(*) FROM inventory_source_active WHERE source_id='movies'`).Scan(&activeJobs); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT double_check_job FROM library_sources WHERE id='movies'`).Scan(&marker); err != nil {
		t.Fatal(err)
	}
	if jobs != 2 || activeJobs != 0 || marker != "" {
		t.Fatalf("follow-up scheduled another pass: jobs=%d active=%d marker=%s", jobs, activeJobs, marker)
	}
}
