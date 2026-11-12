package db

import (
	"context"
	"database/sql"
	"path"
)

// ServerSkippedFile is one file of the server's Files skipped list (12-console rule 72), with
// the library it is in. Directory and File are its place in the list's order, for the cursor.
type ServerSkippedFile struct {
	SkippedFile
	LibraryID, LibraryName string
	Directory, File        int64
}

// SkippedFilesFor lists the files the last scan could not use in this viewer's libraries,
// after the cursor (a folder's id and a file's id; zero for the start). Library access is
// checked before paging; gone files are not listed.
func (d *DB) SkippedFilesFor(ctx context.Context, viewer Viewer, afterDirectory, afterFile int64, limit int) ([]ServerSkippedFile, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	access, err := loadAccess(ctx, tx, viewer)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT f.directory_id, f.id, p.path, d.rel_path, f.name, f.problem, l.public_id, l.name
		FROM files f JOIN directories d ON d.id = f.directory_id JOIN library_paths p ON p.id = d.library_path_id
		JOIN libraries l ON l.id = p.library_id
		WHERE `+access.libraryVisible("l.id")+` AND f.problem IS NOT NULL AND f.missing_since_at IS NULL AND (f.directory_id, f.id) > (?, ?)
		ORDER BY f.directory_id, f.id LIMIT ?`, afterDirectory, afterFile, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerSkippedFile
	for rows.Next() {
		var root, dir, name string
		var s ServerSkippedFile
		if err := rows.Scan(&s.Directory, &s.File, &root, &dir, &name, &s.Problem, &s.LibraryID, &s.LibraryName); err != nil {
			return nil, err
		}
		s.Path = path.Join(root, dir, name)
		out = append(out, s)
	}
	return out, rows.Err()
}
