package db

import (
	"context"
	"database/sql"
	"errors"
	"path"
)

// LocalMetadataFile is a title's existing file, never a locator supplied by a caller.
type LocalMetadataFile struct {
	PathID int64
	Rel    string
}

// LocalMetadataFiles finds only this title's present files, in version/part order.
func (d *DB) LocalMetadataFiles(ctx context.Context, publicID string) (Library, []LocalMetadataFile, error) {
	var library string
	err := d.read.QueryRowContext(ctx, `SELECT l.public_id FROM items i JOIN libraries l ON l.id = i.library_id WHERE i.public_id = ?`, publicID).Scan(&library)
	if errors.Is(err, sql.ErrNoRows) {
		return Library{}, nil, ErrNotFound
	}
	if err != nil {
		return Library{}, nil, err
	}
	libraries, err := d.AdminLibraries(ctx, library)
	if err != nil {
		return Library{}, nil, err
	}
	if len(libraries) != 1 {
		return Library{}, nil, ErrNotFound
	}
	rows, err := d.read.QueryContext(ctx, `SELECT dir.library_path_id,dir.rel_path,f.name FROM items i
 JOIN media m ON m.item_id = i.id JOIN media_parts p ON p.media_id = m.id
 JOIN files f ON f.id = p.file_id JOIN directories dir ON dir.id = f.directory_id
 WHERE i.public_id = ? AND f.missing_since_at IS NULL ORDER BY m.position,m.id,p.position`, publicID)
	if err != nil {
		return Library{}, nil, err
	}
	defer rows.Close()
	var files []LocalMetadataFile
	for rows.Next() {
		var f LocalMetadataFile
		var dir, name string
		if err := rows.Scan(&f.PathID, &dir, &name); err != nil {
			return Library{}, nil, err
		}
		f.Rel = path.Join(dir, name)
		files = append(files, f)
	}
	return libraries[0].Library, files, rows.Err()
}
