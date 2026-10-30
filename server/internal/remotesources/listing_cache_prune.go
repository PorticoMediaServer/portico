package remotesources

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Listing cache is disposable adapter state. An expired session, its entries,
// its verification check and its scan cursor go together, but a large cascade
// must never be one writer hold: each wake advances a bounded keyset window.
const (
	pruneListingAge    = 24 * time.Hour
	pruneListingWindow = 32
	// pruneEntryBatch is how many entries one transaction deletes across
	// all the listings of its window, so a window of 32 small listings stays
	// inside a background write plan as well as one large listing does.
	pruneEntryBatch   = 96
	pruneOrphanWindow = 64
)

// listingPruneCursor is the last (created_at,rowid) the window reached; a
// cursor that has not started reads from the first retained row.
type listingPruneCursor struct {
	started   bool
	createdAt int64
	rowid     int64
}

func (c listingPruneCursor) keys() (int64, int64) {
	if !c.started {
		return math.MinInt64, 0
	}
	return c.createdAt, c.rowid
}

// orphanPruneCursor is the last primary key of an orphan window.
type orphanPruneCursor struct {
	started bool
	job     string
	key     string
}

func (c orphanPruneCursor) keys() (string, string) {
	if !c.started {
		return "", ""
	}
	return c.job, c.key
}

// pruneListingCache takes one bounded step for each disposable listing table
// and reports whether any window still has work; the caller wakes promptly.
func (s *Service) pruneListingCache(ctx context.Context) bool {
	more := s.pruneExpiredListings(ctx)
	if s.pruneOrphanChecks(ctx) {
		more = true
	}
	if s.pruneOrphanScanCursors(ctx) {
		more = true
	}
	return more
}

func (s *Service) pruneExpiredListings(ctx context.Context) bool {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return false
	}
	defer gated.Rollback()
	cutoff := time.Now().Add(-pruneListingAge).Unix()
	from, rowid := s.pruneListings.keys()
	rows, err := gated.Tx().QueryContext(ctx, `SELECT rowid,created_at,id,job_id,directory FROM remote_listing_sessions INDEXED BY remote_listing_retention WHERE created_at<? AND (created_at,rowid)>(?,?) ORDER BY created_at,rowid LIMIT 32`, cutoff, from, rowid)
	if err != nil {
		return false
	}
	type listing struct {
		rowid, createdAt int64
		id, job, dir     string
	}
	page := make([]listing, 0, pruneListingWindow)
	for rows.Next() {
		var l listing
		if err = rows.Scan(&l.rowid, &l.createdAt, &l.id, &l.job, &l.dir); err != nil {
			rows.Close()
			return false
		}
		page = append(page, l)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return false
	}
	entryBudget := int64(pruneEntryBatch)
	for _, l := range page {
		// Same activity predicate as the unbounded sweep: a pending session's
		// snapshots are kept regardless of wall-clock age.
		var active bool
		if err = gated.Tx().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs j WHERE (j.id=?1 OR EXISTS(SELECT 1 FROM remote_inventory_checks c WHERE c.check_job=?1 AND c.job_id=j.id)) AND j.status IN ('queued','running','paused'))`, l.job).Scan(&active); err != nil {
			return false
		}
		if !active && strings.HasPrefix(l.job, verificationKeyPrefix) {
			source, epoch, ok := parseVerificationKey(l.job)
			if ok {
				if err = gated.Tx().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_source_verification WHERE source_id=? AND health_epoch=? AND pending=1)`, source, epoch).Scan(&active); err != nil {
					return false
				}
			}
		}
		if active {
			s.pruneListings = listingPruneCursor{started: true, createdAt: l.createdAt, rowid: l.rowid}
			continue
		}
		result, execErr := gated.Tx().ExecContext(ctx, `DELETE FROM remote_listing_entries WHERE rowid IN(SELECT rowid FROM remote_listing_entries WHERE listing_id=? LIMIT ?)`, l.id, entryBudget)
		if execErr != nil {
			return false
		}
		deleted, _ := result.RowsAffected()
		if entryBudget -= deleted; entryBudget == 0 {
			// Entries remain: resume just before this listing next wake.
			s.pruneListings = listingPruneCursor{started: true, createdAt: l.createdAt, rowid: l.rowid - 1}
			return gated.Commit() == nil
		}
		if _, err = gated.Tx().ExecContext(ctx, `DELETE FROM remote_inventory_checks WHERE job_id=? AND directory=? AND listing_id=?`, l.job, l.dir, l.id); err != nil {
			return false
		}
		if _, err = gated.Tx().ExecContext(ctx, `DELETE FROM remote_scan_cursors WHERE job_id=? AND path=? AND listing_id=?`, l.job, l.dir, l.id); err != nil {
			return false
		}
		if _, err = gated.Tx().ExecContext(ctx, `DELETE FROM remote_listing_sessions WHERE id=?`, l.id); err != nil {
			return false
		}
		if strings.HasPrefix(l.job, verificationKeyPrefix) {
			if _, err = gated.Tx().ExecContext(ctx, `DELETE FROM remote_scan_generations WHERE job_id=? AND NOT EXISTS(SELECT 1 FROM remote_listing_sessions WHERE job_id=?)`, l.job, l.job); err != nil {
				return false
			}
		}
		s.pruneListings = listingPruneCursor{started: true, createdAt: l.createdAt, rowid: l.rowid}
		return gated.Commit() == nil
	}
	if len(page) < pruneListingWindow {
		// The pass reached the end of the expired range; the next wake starts over.
		s.pruneListings = listingPruneCursor{}
		return false
	}
	// A full window of active sessions; nothing was written, so the deferred
	// rollback ends the pass quietly.
	return true
}

func (s *Service) pruneOrphanChecks(ctx context.Context) bool {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return false
	}
	defer gated.Rollback()
	fromJob, fromDir := s.pruneChecks.keys()
	rows, err := gated.Tx().QueryContext(ctx, `SELECT job_id,directory,listing_id FROM remote_inventory_checks WHERE (job_id,directory)>(?,?) ORDER BY job_id,directory LIMIT 64`, fromJob, fromDir)
	if err != nil {
		return false
	}
	type orphan struct{ job, dir, listing string }
	page := make([]orphan, 0, pruneOrphanWindow)
	for rows.Next() {
		var o orphan
		if err = rows.Scan(&o.job, &o.dir, &o.listing); err != nil {
			rows.Close()
			return false
		}
		page = append(page, o)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return false
	}
	wrote := false
	for _, o := range page {
		var kept bool
		if err = gated.Tx().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_listing_sessions WHERE id=?)`, o.listing).Scan(&kept); err != nil {
			return false
		}
		if kept {
			continue
		}
		if _, err = gated.Tx().ExecContext(ctx, `DELETE FROM remote_inventory_checks WHERE job_id=? AND directory=? AND listing_id=?`, o.job, o.dir, o.listing); err != nil {
			return false
		}
		wrote = true
	}
	if len(page) < pruneOrphanWindow {
		s.pruneChecks = orphanPruneCursor{}
	} else {
		s.pruneChecks = orphanPruneCursor{started: true, job: page[len(page)-1].job, key: page[len(page)-1].dir}
	}
	if wrote {
		if err = gated.Commit(); err != nil {
			return false
		}
	}
	return len(page) == pruneOrphanWindow
}

func (s *Service) pruneOrphanScanCursors(ctx context.Context) bool {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return false
	}
	defer gated.Rollback()
	fromJob, fromPath := s.pruneScanCursors.keys()
	rows, err := gated.Tx().QueryContext(ctx, `SELECT job_id,path,listing_id FROM remote_scan_cursors WHERE (job_id,path)>(?,?) ORDER BY job_id,path LIMIT 64`, fromJob, fromPath)
	if err != nil {
		return false
	}
	type orphan struct{ job, path, listing string }
	page := make([]orphan, 0, pruneOrphanWindow)
	for rows.Next() {
		var o orphan
		if err = rows.Scan(&o.job, &o.path, &o.listing); err != nil {
			rows.Close()
			return false
		}
		page = append(page, o)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return false
	}
	wrote := false
	for _, o := range page {
		var kept bool
		if err = gated.Tx().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_listing_sessions WHERE id=?)`, o.listing).Scan(&kept); err != nil {
			return false
		}
		if kept {
			continue
		}
		if _, err = gated.Tx().ExecContext(ctx, `DELETE FROM remote_scan_cursors WHERE job_id=? AND path=? AND listing_id=?`, o.job, o.path, o.listing); err != nil {
			return false
		}
		wrote = true
	}
	if len(page) < pruneOrphanWindow {
		s.pruneScanCursors = orphanPruneCursor{}
	} else {
		s.pruneScanCursors = orphanPruneCursor{started: true, job: page[len(page)-1].job, key: page[len(page)-1].path}
	}
	if wrote {
		if err = gated.Commit(); err != nil {
			return false
		}
	}
	return len(page) == pruneOrphanWindow
}
