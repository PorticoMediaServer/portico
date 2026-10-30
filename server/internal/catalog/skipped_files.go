package catalog

import (
	"context"
	"database/sql"
)

// SkippedIssuePredicate admits only a known unplayable file reason, never a
// navigation, source-policy, metadata-matching or sidecar diagnostic.
const SkippedIssuePredicate = `reason IN('probe_failed','file_truncated','damaged_media','unsupported_disc_layout','unsupported_disc_image','unsupported_remote_disc','unsupported_container','unsupported_codec','not_media','unfinished_download','partial_download','sample_file')`

func LastSkippedCount(ctx context.Context, tx *sql.Tx, library string) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM inventory_scan_issues WHERE job_id=(SELECT j.id FROM jobs j JOIN inventory_runs r ON r.job_id=j.id WHERE j.library_id=? AND j.status IN('complete','complete_with_warnings') ORDER BY j.created_at DESC,j.id DESC LIMIT 1) AND `+SkippedIssuePredicate, library).Scan(&n)
	return n, err
}
