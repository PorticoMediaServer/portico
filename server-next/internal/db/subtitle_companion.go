package db

import (
	"context"
	"database/sql"
	"errors"
)

// SubtitleCompanion names the inventoried packet file of a VobSub index.
// Its actual stored name is used, including the case of the extension.
func (d *DB) SubtitleCompanion(ctx context.Context, indexFile int64) (int64, error) {
	var id int64
	err := d.read.QueryRowContext(ctx, `SELECT c.id FROM files f JOIN files c ON c.directory_id = f.directory_id
		WHERE f.id = ? AND f.role = ? AND f.missing_since_at IS NULL AND lower(substr(f.name, -4)) = '.idx'
		AND c.role = f.role AND c.missing_since_at IS NULL
		AND lower(c.name) = lower(substr(f.name, 1, length(f.name)-4) || '.sub') ORDER BY c.name LIMIT 1`, indexFile, FileSubtitle).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return id, err
}
