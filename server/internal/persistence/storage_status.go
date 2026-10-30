package persistence

import (
	"context"
	"database/sql"
	"time"
)

type StorageStatus struct {
	DatabasePath       string `json:"databasePath"`
	CacheDirectory     string `json:"cacheDirectory"`
	BackupDirectory    string `json:"backupDirectory"`
	ReclaimableBytes   int64  `json:"reclaimableBytes"`
	LastReclaimedAt    string `json:"lastReclaimedAt,omitempty"`
	LastReclaimedBytes int64  `json:"lastReclaimedBytes"`
}

func databasePath(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		var name, path string
		if err = rows.Scan(&n, &name, &path); err != nil {
			return "", err
		}
		if name == "main" {
			return path, nil
		}
	}
	return "", rows.Err()
}
func ReadStorageStatus(ctx context.Context, db *sql.DB) (StorageStatus, error) {
	out := StorageStatus{}
	var err error
	out.DatabasePath, err = databasePath(ctx, db)
	if err != nil {
		return out, err
	}
	var pages, size, last int64
	if err = db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&pages); err != nil {
		return out, err
	}
	if err = db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size); err != nil {
		return out, err
	}
	out.ReclaimableBytes = pages * size
	if err = db.QueryRowContext(ctx, `SELECT last_ran_ms,last_reclaimed_bytes FROM database_maintenance_status WHERE singleton=1`).Scan(&last, &out.LastReclaimedBytes); err != nil {
		return out, err
	}
	if last > 0 {
		out.LastReclaimedAt = time.UnixMilli(last).UTC().Format(time.RFC3339)
	}
	return out, nil
}
