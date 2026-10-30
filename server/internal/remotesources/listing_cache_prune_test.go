package remotesources

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func pruneListing(t *testing.T, s *Service, id, job, directory string, createdAt int64) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO remote_listing_sessions(id,job_id,source_id,generation,directory,created_at) VALUES(?,?,'remote','1',?,?)`, id, job, directory, createdAt); err != nil {
		t.Fatal(err)
	}
}

func pruneEntries(t *testing.T, s *Service, listing string, count int) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	statement, err := tx.Prepare(`INSERT INTO remote_listing_entries(listing_id,name,snapshot) VALUES(?,?,'{}')`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for i := 0; i < count; i++ {
		if _, err = statement.Exec(listing, fmt.Sprintf("entry-%05d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func pruneCount(t *testing.T, s *Service, query string, args ...any) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// An expired session leaves in pruneEntryBatch-entry batches, and only the batch that
// exhausts its entries also removes the session, its check and its cursor.
func TestListingCachePruneExpiredListingLeavesBoundedBatches(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	old := time.Now().Add(-30 * 24 * time.Hour).Unix()
	pruneListing(t, f.s, "old", "past-job", "/past", old)
	pruneEntries(t, f.s, "old", 3000)
	if _, err := f.s.db.Exec(`INSERT INTO remote_inventory_checks(job_id,directory,listing_id,check_job,verified_ms) VALUES('past-job','/past','old','past-job',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`INSERT INTO remote_scan_cursors(job_id,path,listing_id,cursor) VALUES('past-job','/past','old','')`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	for calls < 3000/pruneEntryBatch+2 && pruneCount(t, f.s, `SELECT count(*) FROM remote_listing_sessions WHERE id='old'`) == 1 {
		before := pruneCount(t, f.s, `SELECT count(*) FROM remote_listing_entries WHERE listing_id='old'`)
		f.s.pruneListingCache(ctx)
		after := pruneCount(t, f.s, `SELECT count(*) FROM remote_listing_entries WHERE listing_id='old'`)
		if before-after > pruneEntryBatch {
			t.Fatalf("one call deleted %d entries", before-after)
		}
		calls++
	}
	if calls < 3000/pruneEntryBatch {
		t.Fatalf("3000 entries left in %d calls", calls)
	}
	for _, query := range []string{
		`SELECT count(*) FROM remote_listing_sessions WHERE id='old'`,
		`SELECT count(*) FROM remote_listing_entries WHERE listing_id='old'`,
		`SELECT count(*) FROM remote_inventory_checks WHERE listing_id='old'`,
		`SELECT count(*) FROM remote_scan_cursors WHERE listing_id='old'`,
	} {
		if left := pruneCount(t, f.s, query); left != 0 {
			t.Fatalf("%s left %d rows", query, left)
		}
	}
}

// A pending job keeps its expired session; the expired session after it is
// still reached in the same window.
func TestListingCachePruneKeepsExpiredListingOfPendingJob(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	old := time.Now().Add(-30 * 24 * time.Hour).Unix()
	if _, err := f.s.db.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('pending-job',?,'running','2026-10-23T00:00:00Z')`, f.library); err != nil {
		t.Fatal(err)
	}
	pruneListing(t, f.s, "kept", "pending-job", "/kept", old)
	pruneEntries(t, f.s, "kept", 5)
	pruneListing(t, f.s, "gone", "past-job", "/gone", old+1)
	pruneEntries(t, f.s, "gone", 5)
	f.s.pruneListingCache(ctx)
	if kept := pruneCount(t, f.s, `SELECT count(*) FROM remote_listing_sessions WHERE id='kept'`); kept != 1 {
		t.Fatal("expired session of a pending job was pruned")
	}
	if kept := pruneCount(t, f.s, `SELECT count(*) FROM remote_listing_entries WHERE listing_id='kept'`); kept != 5 {
		t.Fatalf("pending session kept %d entries", kept)
	}
	if gone := pruneCount(t, f.s, `SELECT count(*) FROM remote_listing_sessions WHERE id='gone'`); gone != 0 {
		t.Fatal("expired inactive session was not pruned")
	}
}

func TestListingCachePruneKeepsFreshListing(t *testing.T) {
	f := newObservationFixture(t)
	pruneListing(t, f.s, "fresh", "past-job", "/fresh", time.Now().Add(-time.Hour).Unix())
	pruneEntries(t, f.s, "fresh", 3)
	f.s.pruneListingCache(context.Background())
	if kept := pruneCount(t, f.s, `SELECT count(*) FROM remote_listing_sessions WHERE id='fresh'`); kept != 1 {
		t.Fatal("listing younger than 24h was pruned")
	}
	if kept := pruneCount(t, f.s, `SELECT count(*) FROM remote_listing_entries WHERE listing_id='fresh'`); kept != 3 {
		t.Fatalf("fresh listing kept %d entries", kept)
	}
}

func TestListingCachePruneRemovesOnlyOrphans(t *testing.T) {
	f := newObservationFixture(t)
	pruneListing(t, f.s, "live", "live-job", "/live", time.Now().Unix())
	for _, row := range []struct{ job, directory, listing string }{
		{"orphan-job", "/orphan", "missing"},
		{"live-job", "/live", "live"},
	} {
		if _, err := f.s.db.Exec(`INSERT INTO remote_inventory_checks(job_id,directory,listing_id,check_job,verified_ms) VALUES(?,?,?,?,0)`, row.job, row.directory, row.listing, row.job); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.db.Exec(`INSERT INTO remote_scan_cursors(job_id,path,listing_id,cursor) VALUES(?,?,?,'')`, row.job, row.directory, row.listing); err != nil {
			t.Fatal(err)
		}
	}
	f.s.pruneListingCache(context.Background())
	if left := pruneCount(t, f.s, `SELECT count(*) FROM remote_inventory_checks WHERE job_id='orphan-job'`); left != 0 {
		t.Fatal("orphan check was kept")
	}
	if left := pruneCount(t, f.s, `SELECT count(*) FROM remote_scan_cursors WHERE job_id='orphan-job'`); left != 0 {
		t.Fatal("orphan scan cursor was kept")
	}
	if kept := pruneCount(t, f.s, `SELECT count(*) FROM remote_inventory_checks WHERE job_id='live-job'`); kept != 1 {
		t.Fatal("check of a live listing was pruned")
	}
	if kept := pruneCount(t, f.s, `SELECT count(*) FROM remote_scan_cursors WHERE job_id='live-job'`); kept != 1 {
		t.Fatal("scan cursor of a live listing was pruned")
	}
}

// The prune window is bounded, so one call's work does not grow with the table.
func TestListingCachePruneCostDoesNotGrowWithExpiredRows(t *testing.T) {
	steps := func(count int) int64 {
		f := newObservationFixture(t)
		old := time.Now().Add(-30 * 24 * time.Hour).Unix()
		tx, err := f.s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		statement, err := tx.Prepare(`INSERT INTO remote_listing_sessions(id,job_id,source_id,generation,directory,created_at) VALUES(?, 'scan','remote','1',?,?)`)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		for i := 0; i < count; i++ {
			if _, err = statement.Exec(fmt.Sprintf("listing-%05d", i), fmt.Sprintf("/dir/%05d", i), old); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		ctx, cost := dbwork.Measure(context.Background())
		if !f.s.pruneListingCache(ctx) {
			t.Fatal("a full window of expired sessions reported no further work")
		}
		return cost().VMSteps
	}
	small, large := steps(200), steps(2000)
	t.Logf("prune steps: %d at 200 expired rows, %d at 2000", small, large)
	if small <= 0 || large >= 2*small {
		t.Fatalf("prune steps %d at 200 rows, %d at 2000 rows", small, large)
	}
}
