package db

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"path/filepath"
)

// VersionFiles is one version of an item with the files it is made of, as the scanner
// recorded them.
type VersionFiles struct {
	PublicID string
	Name     string
	Files    []RecordedFile
}

// RecordedFile is one file of a version: where it is on the server and what reading it found.
// A file that has gone missing keeps its place, so the owner can see where it was.
type RecordedFile struct {
	Path          string
	Size          int64
	Container     string
	DurationMS    int64
	MissingSince  int64 // when it went missing in milliseconds, 0 while it is there
	StartMS       *int64
	EndMS         *int64
	Streams       []Stream
	fileID        int64
	root, dir, nm string
}

// ItemFiles returns every version of an item with the place and facts of each of its files,
// including versions whose files are missing. It is for the owner and administrators; the
// caller checks the role. An item the viewing profile may not see is not found.
func (d *DB) ItemFiles(ctx context.Context, v Viewer, publicID string) ([]VersionFiles, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	var itemID int64
	err = tx.QueryRowContext(ctx, `SELECT i.id FROM items i WHERE i.public_id = ? AND `+acc.visible("i"), publicID).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	type versionRow struct {
		VersionFiles
		id int64
	}
	versions, err := queryAll(ctx, tx, `SELECT id, public_id, ifnull(name, '') FROM media WHERE item_id = ? ORDER BY position, id`,
		[]any{itemID}, func(m *versionRow) []any { return []any{&m.id, &m.PublicID, &m.Name} })
	if err != nil {
		return nil, err
	}
	out := make([]VersionFiles, len(versions))
	for n, m := range versions {
		files, err := queryAll(ctx, tx, `SELECT f.id, lp.path, d.rel_path, f.name, f.size, ifnull(f.container, ''), ifnull(f.duration_ms, 0),
			ifnull(f.missing_since_at, 0), p.start_ms, p.end_ms FROM media_parts p JOIN files f ON f.id = p.file_id
			JOIN directories d ON d.id = f.directory_id JOIN library_paths lp ON lp.id = d.library_path_id
			WHERE p.media_id = ? ORDER BY p.position`, []any{m.id},
			func(f *RecordedFile) []any {
				return []any{&f.fileID, &f.root, &f.dir, &f.nm, &f.Size, &f.Container, &f.DurationMS, &f.MissingSince, &f.StartMS, &f.EndMS}
			})
		if err != nil {
			return nil, err
		}
		for k := range files {
			f := &files[k]
			f.Path = filepath.Join(f.root, filepath.FromSlash(path.Join(f.dir, f.nm)))
			if f.Streams, err = streams(ctx, tx, f.fileID); err != nil {
				return nil, err
			}
		}
		out[n] = m.VersionFiles
		out[n].Files = files
	}
	return out, nil
}
