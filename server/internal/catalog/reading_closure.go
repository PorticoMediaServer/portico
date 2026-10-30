package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
)

// readingInventoryDirectoryTx registers one physical edge with the page that
// discovered it. Replays cannot duplicate parent debt. Logical Reading keys are
// still computed by ReadingDirectoryKey, never by a SQL dirname expression.
func readingInventoryDirectoryTx(ctx context.Context, tx *sql.Tx, job, relative string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_reading_jobs WHERE job_id=?)`, job).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_reading_jobs(job_id,source_id,incarnation,generation,root_identity,root,coverage_revision)
 SELECT r.job_id,s.id,r.root_incarnation,r.source_generation,r.root_identity,s.root,COALESCE((SELECT sequence FROM inventory_scan_issues WHERE job_id=r.job_id AND protect=1 ORDER BY sequence DESC LIMIT 1),0)
 FROM inventory_runs r JOIN library_sources s ON s.id=r.source_id JOIN libraries l ON l.id=s.library_id
 WHERE r.job_id=? AND r.scan_mode='first-import' AND s.kind='local' AND s.identity_confirmed=1 AND r.root_identity<>'' AND l.kind IN('book','comic')`, job); err != nil {
			return err
		}
	}
	parent, top := filepath.Dir(relative), strings.Split(filepath.ToSlash(relative), "/")[0]
	if relative == "." {
		parent, top = "", ""
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_reading_directories(job_id,relative_path,parent_path,top_level) SELECT job_id,?,?,? FROM inventory_reading_jobs WHERE job_id=?`, relative, parent, top, job)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n > 0 && parent != "" {
		_, err = tx.ExecContext(ctx, `UPDATE inventory_reading_directories SET children=children+1 WHERE job_id=? AND relative_path=?`, job, parent)
	}
	return err
}

// DrainReadingClosuresTx releases private scan proofs in bounded child-first
// chunks before a run/job is removed. No public deletion cascades a subtree.
func DrainReadingClosuresTx(ctx context.Context, tx *sql.Tx, job string, limit int) (bool, error) {
	limit = max(1, min(limit, 128))
	for _, q := range []string{
		`DELETE FROM inventory_reading_seals WHERE id IN(SELECT id FROM inventory_reading_seals WHERE job_id=? ORDER BY top_level,id LIMIT ?)`,
		`DELETE FROM inventory_reading_directories WHERE (job_id,relative_path) IN(SELECT job_id,relative_path FROM inventory_reading_directories WHERE job_id=? ORDER BY relative_path LIMIT ?)`,
	} {
		r, e := tx.ExecContext(ctx, q, job, limit)
		if e != nil {
			return false, e
		}
		if n, _ := r.RowsAffected(); n > 0 {
			return true, nil
		}
	}
	r, e := tx.ExecContext(ctx, `DELETE FROM inventory_reading_jobs WHERE job_id=?`, job)
	if e != nil {
		return false, e
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}
