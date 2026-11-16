package db

import "context"

// STRMEnabled is checked at each playback acquisition, including before the next
// scan has removed disabled pointers from the library's available versions.
func (d *DB) STRMEnabled(ctx context.Context, fileID int64) (bool, error) {
	var enabled bool
	err := d.read.QueryRowContext(ctx, `SELECT ifnull(json_extract(l.options,'$.strm'),0) = 1
 FROM files f JOIN directories dir ON dir.id = f.directory_id
 JOIN library_paths p ON p.id = dir.library_path_id JOIN libraries l ON l.id = p.library_id
 WHERE f.id = ? AND f.missing_since_at IS NULL`, fileID).Scan(&enabled)
	return enabled, err
}
