package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strings"
)

func InventorySidecar(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".nfo", ".opf", ".jpg", ".jpeg", ".png", ".webp", ".json", ".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx", ".sup", ".smi", ".sami", ".ttml", ".dfxp", ".lrc", ".txt":
		return true
	}
	return false
}

func observeSidecarTx(ctx context.Context, tx *sql.Tx, job string, source LibrarySource, relative string, v storage.Snapshot) error {
	if !InventorySidecar(relative) {
		return nil
	}
	revision, _, err := ObservationRevision(source, v)
	if err != nil {
		return err
	}
	var old string
	err = tx.QueryRowContext(ctx, `SELECT revision FROM inventory_sidecars WHERE source_id=? AND incarnation=? AND relative_path=?`, source.ID, source.Incarnation, relative).Scan(&old)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if old != revision {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_sidecar_changes(job_id,relative_path) VALUES(?,?)`, job, filepath.Dir(relative)); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO inventory_sidecars(source_id,incarnation,relative_path,directory_path,revision,seen_job) VALUES(?,?,?,?,?,?) ON CONFLICT(source_id,incarnation,relative_path) DO UPDATE SET revision=excluded.revision,seen_job=excluded.seen_job`, source.ID, source.Incarnation, relative, filepath.Dir(relative), revision, job)
	return err
}
