package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strings"
)

// A full scan may span many worker quanta and server restarts. Its adapter
// generation is fixed at first contact, independent of each directory cursor.
func (s *Service) CheckScan(ctx context.Context, job, root string) error {
	b, _, ok := s.find(root)
	if !ok || b.Removed {
		return storage.ErrRemoteOffline
	}
	generation, err := s.generation(ctx, b)
	if err != nil {
		return err
	}
	if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `INSERT OR IGNORE INTO remote_scan_generations VALUES(?,?,?)`, job, b.ID, generation); err != nil {
		return err
	}
	var expected string
	if err = s.db.QueryRowContext(ctx, `SELECT generation FROM remote_scan_generations WHERE job_id=? AND source_id=?`, job, b.ID).Scan(&expected); err != nil {
		return err
	}
	if expected != generation {
		return storage.ErrRemoteChanged
	}
	return nil
}
func (s *Service) ValidateScan(ctx context.Context, tx *sql.Tx, job, root string) error {
	b, _, ok := s.find(root)
	if !ok {
		return storage.ErrRemoteConfig
	}
	var expected, current string
	var removed bool
	err := tx.QueryRowContext(ctx, `SELECT g.generation,CASE WHEN r.kind IN('rclone','sftp','smb') THEN CAST(m.generation AS TEXT) ELSE CAST(r.generation AS TEXT) END,r.removed FROM remote_scan_generations g JOIN remote_sources r ON r.id=g.source_id LEFT JOIN mount_backend_configs m ON m.mount_id=r.mount_id WHERE g.job_id=? AND r.id=?`, job, b.ID).Scan(&expected, &current, &removed)
	if err != nil || removed || expected != current {
		return storage.ErrRemoteChanged
	}
	return nil
}
func (s *Service) RecordReference(ctx context.Context, tx *sql.Tx, path, revision string) error {
	b, _, ok := s.find(path)
	if !ok || revision == "" {
		return storage.ErrRemoteConfig
	}
	var generation string
	var removed bool
	err := tx.QueryRowContext(ctx, `SELECT CASE WHEN r.kind IN('rclone','sftp','smb') THEN CAST(m.generation AS TEXT) ELSE CAST(r.generation AS TEXT) END,r.removed FROM remote_sources r LEFT JOIN mount_backend_configs m ON m.mount_id=r.mount_id WHERE r.id=?`, b.ID).Scan(&generation, &removed)
	if err != nil || removed || !strings.HasPrefix(revision, generation+":") {
		return storage.ErrRemoteChanged
	}
	var old string
	err = tx.QueryRowContext(ctx, `SELECT revision FROM remote_object_refs WHERE path=?`, path).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO remote_object_refs VALUES(?,?) ON CONFLICT(path) DO UPDATE SET revision=excluded.revision`, path, revision)
	return err
}
