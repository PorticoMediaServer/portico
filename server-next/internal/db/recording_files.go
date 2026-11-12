package db

import (
	"context"
	"path/filepath"
)

// RecordingFilePaths locates a recording's file in its title, including a folder used
// before the owner changed the recording destination. It never guesses from today's setting.
func (d *DB) RecordingFilePaths(ctx context.Context, itemID int64, directory, name string) ([]string, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT DISTINCT lp.path, d.rel_path, f.name FROM media m
  JOIN media_parts mp ON mp.media_id=m.id JOIN files f ON f.id=mp.file_id
  JOIN directories d ON d.id=f.directory_id JOIN library_paths lp ON lp.id=d.library_path_id
  WHERE m.item_id=? AND d.rel_path=? AND f.name=? AND lp.remote_source_id IS NULL`, itemID, directory, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var root, dir, file string
		if err := rows.Scan(&root, &dir, &file); err != nil {
			return nil, err
		}
		paths = append(paths, filepath.Join(root, filepath.FromSlash(dir), file))
	}
	return paths, rows.Err()
}
