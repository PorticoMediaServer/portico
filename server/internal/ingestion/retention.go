package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

const retainedScansPerSource = 20
const retainedChangesPerSource = 10000
const scanRetentionBatch = 256

// One call removes at most one bounded chunk. Active/paused runs are never
// eligible; integrity cadence is kept on the source rather than old work rows.
func (s *Service) PruneSource(ctx context.Context, source string) (bool, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return false, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	cutoff := time.Now().Add(-15 * time.Minute).UTC().Format(time.RFC3339Nano)
	// Proofs are dismantled child-first, before either run/job retention or
	// ordinary directory scratch retirement. Active/paused jobs retain them.
	var closureJob string
	err = tx.QueryRowContext(ctx, `SELECT c.job_id FROM inventory_reading_jobs c JOIN inventory_runs r ON r.job_id=c.job_id JOIN jobs j ON j.id=c.job_id WHERE c.source_id=? AND j.status NOT IN('queued','running','paused') AND COALESCE(nullif(r.finished_at,''),j.created_at)<? ORDER BY c.job_id LIMIT 1`, source, cutoff).Scan(&closureJob)
	if err == nil {
		if _, err = catalog.DrainReadingClosuresTx(ctx, tx, closureJob, 64); err != nil {
			return false, err
		}
		return true, gated.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	for _, table := range []string{"inventory_analysis_queue", "inventory_directories", "inventory_sidecar_changes", "subtitle_inventory_entries"} {
		result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid IN(SELECT w.rowid FROM `+table+` w JOIN inventory_runs r ON r.job_id=w.job_id JOIN jobs j ON j.id=r.job_id WHERE r.source_id=? AND j.status NOT IN('queued','running','paused') AND COALESCE(nullif(r.finished_at,''),j.created_at)<? LIMIT ?)`, source, cutoff, scanRetentionBatch)
		if err != nil {
			return false, err
		}
		if n, _ := result.RowsAffected(); n > 0 {
			return true, gated.Commit()
		}
	}
	// Rows of an earlier incarnation, or of a disabled folder, go. The
	// current incarnation is skipped by key, so a folder with nothing to
	// retire costs two index seeks rather than a read of its whole cache.
	var enabled bool
	var incarnation string
	if err = tx.QueryRowContext(ctx, `SELECT enabled,incarnation FROM library_sources WHERE id=?`, source).Scan(&enabled, &incarnation); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	retired := `c.source_id=?1 AND (c.incarnation<?2 OR c.incarnation>?2)`
	if !enabled {
		retired = `c.source_id=?1`
	}
	for _, table := range []string{"inventory_directory_cache", "inventory_sidecars", "inventory_directory_prune"} {
		result, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid IN(SELECT c.rowid FROM `+table+` c WHERE `+retired+` LIMIT ?3)`, source, incarnation, scanRetentionBatch)
		if e != nil {
			return false, e
		}
		if n, _ := result.RowsAffected(); n > 0 {
			return true, gated.Commit()
		}
	}
	if more, e := s.pruneRemoteListingsTx(ctx, tx, source, cutoff); more || e != nil {
		if e != nil {
			return false, e
		}
		return true, gated.Commit()
	}
	for _, table := range []string{"remote_scan_cursors", "remote_scan_generations"} {
		result, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid IN(SELECT w.rowid FROM `+table+` w JOIN inventory_runs r ON r.job_id=w.job_id JOIN jobs j ON j.id=r.job_id WHERE r.source_id=? AND j.status NOT IN('queued','running','paused') AND COALESCE(nullif(r.finished_at,''),j.created_at)<? LIMIT ?)`, source, cutoff, scanRetentionBatch)
		if e != nil {
			return false, e
		}
		if n, _ := result.RowsAffected(); n > 0 {
			return true, gated.Commit()
		}
	}
	// Change consumers receive an explicit reset marker when their cursor falls
	// behind retained history. Keep the most recent entries or thirty days.
	var boundary int64
	err = tx.QueryRowContext(ctx, `SELECT sequence FROM inventory_changes WHERE source_id=? ORDER BY sequence DESC LIMIT 1 OFFSET ?`, source, retainedChangesPerSource-1).Scan(&boundary)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT sequence FROM inventory_changes WHERE source_id=? AND (sequence<? OR created_at<?) ORDER BY sequence LIMIT ?`, source, boundary, time.Now().Add(-30*24*time.Hour).UTC().Format(time.RFC3339Nano), scanRetentionBatch)
	if err != nil {
		return false, err
	}
	sequences := []int64{}
	for rows.Next() {
		var sequence int64
		if err = rows.Scan(&sequence); err != nil {
			rows.Close()
			return false, err
		}
		sequences = append(sequences, sequence)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(sequences) > 0 {
		last := sequences[len(sequences)-1]
		if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_changes WHERE sequence IN(SELECT sequence FROM inventory_changes WHERE source_id=? AND (sequence<? OR created_at<?) ORDER BY sequence LIMIT ?)`, source, boundary, time.Now().Add(-30*24*time.Hour).UTC().Format(time.RFC3339Nano), scanRetentionBatch); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE library_sources SET changes_pruned_sequence=max(changes_pruned_sequence,?) WHERE id=?`, last, source); err != nil {
			return false, err
		}
		return true, gated.Commit()
	}
	var old string
	err = tx.QueryRowContext(ctx, `SELECT r.job_id FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE r.source_id=? AND j.status NOT IN('queued','running','paused') AND r.job_id NOT IN(SELECT r2.job_id FROM inventory_runs r2 JOIN jobs j2 ON j2.id=r2.job_id WHERE r2.source_id=? AND j2.status NOT IN('queued','running','paused') ORDER BY j2.created_at DESC,j2.id DESC LIMIT ?) ORDER BY j.created_at,j.id LIMIT 1`, source, source, retainedScansPerSource).Scan(&old)
	if errors.Is(err, sql.ErrNoRows) {
		return false, gated.Commit()
	}
	if err != nil {
		return false, err
	}
	if more, e := catalog.DrainReadingClosuresTx(ctx, tx, old, 64); e != nil {
		return false, e
	} else if more {
		return true, gated.Commit()
	}
	// A very large old run is dismantled over many turns before its summary goes.
	for _, table := range []string{"inventory_analysis_queue", "inventory_directories", "inventory_sidecar_changes", "inventory_scan_issues", "subtitle_inventory_entries", "analysis_stage_runs", "scan_queue"} {
		result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid IN(SELECT rowid FROM `+table+` WHERE job_id=? LIMIT ?)`, old, scanRetentionBatch)
		if err != nil {
			return false, err
		}
		if n, _ := result.RowsAffected(); n > 0 {
			return true, gated.Commit()
		}
	}
	for _, table := range []string{"inventory_run_targets", "inventory_operation_jobs", "inventory_runs"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE job_id=?`, old); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM jobs WHERE id=? AND status NOT IN('queued','running','paused')`, old); err != nil {
		return false, err
	}
	return true, gated.Commit()
}
