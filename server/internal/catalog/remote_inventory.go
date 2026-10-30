package catalog

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func (s *Service) sourceKind(path string) string {
	if s.storage.IsRemote(path) {
		return "remote"
	}
	return "local"
}
func (s *Service) sourceClassification(path string) string {
	if s.storage.IsRemote(path) {
		return "network"
	}
	return "local"
}

// The adapter's generation joins the source/run fence in the same transaction.
// This does not perform remote I/O or depend on process-local registry freshness.
func remoteInventoryFenceTx(ctx context.Context, tx *sql.Tx, job string) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_scan_generations g JOIN remote_sources r ON r.id=g.source_id LEFT JOIN mount_backend_configs m ON m.mount_id=r.mount_id WHERE g.job_id=? AND r.removed=0 AND g.generation=CASE WHEN r.kind='rclone' THEN CAST(m.generation AS TEXT) ELSE CAST(r.generation AS TEXT) END)`, job).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return storage.ErrRemoteChanged
	}
	return nil
}
