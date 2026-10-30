package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// An unreadable or changing path is never evidence of absence. Other folders
// in the same run can still finish and publish their files and analysis.
func InventoryIssueTx(ctx context.Context, tx *sql.Tx, job string, issue storage.InventoryIssue) error {
	if !filepath.IsLocal(issue.Path) || len(issue.Path) > 16384 || issue.Reason == "" || len(issue.Reason) > 128 {
		return ErrAdminQuery
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_scan_issues(job_id,relative_path,reason,line,protect,warning) VALUES(?,?,?,?,?,?)`, job, issue.Path, issue.Reason, issue.Line, issue.Protect, issue.Warning)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n > 0 && issue.Protect {
		if err = compactcatalog.ReadingQueuePathCoverageTx(ctx, tx, job); err != nil {
			return err
		}
	}
	if n > 0 && issue.Warning {
		_, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET warnings=warnings+1 WHERE job_id=?`, job)
	}
	return err
}
