package db

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"time"
)

// StoredFile is what the last scan knew about one file of a folder.
type StoredFile struct {
	ID         int64
	Role       FileRole
	Size       int64
	ModifiedNS int64
	Missing    bool
	Problem    FileProblem
}

// StoredFolder is what the last scan knew about a folder: its files by name and the folders
// directly inside it. Known is false for a folder no scan has stored.
type StoredFolder struct {
	Known   bool
	Files   map[string]StoredFile
	Folders []string // paths relative to the library path
	// Disk is the number of the disk the folder was on when it was stored, and Disks the same
	// for the folders inside it; 0 where it was not known.
	Disk  int64
	Disks map[string]int64
}

// Folder reads what is stored about one folder, for a scan to compare with what is there now.
func (d *DB) Folder(ctx context.Context, pathID int64, rel string) (StoredFolder, error) {
	out := StoredFolder{Files: map[string]StoredFile{}, Disks: map[string]int64{}}
	var id int64
	err := d.read.QueryRowContext(ctx, `SELECT id, ifnull(device, 0) FROM directories WHERE library_path_id = ? AND rel_path = ?`, pathID, rel).
		Scan(&id, &out.Disk)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Known = true
	rows, err := d.read.QueryContext(ctx, `SELECT name, id, role, size, modified_ns, missing_since_at IS NOT NULL, ifnull(problem, 0)
		FROM files WHERE directory_id = ?`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var name string
		var f StoredFile
		if err := rows.Scan(&name, &f.ID, &f.Role, &f.Size, &f.ModifiedNS, &f.Missing, &f.Problem); err != nil {
			rows.Close()
			return out, err
		}
		out.Files[name] = f
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	rows, err = d.read.QueryContext(ctx, `SELECT rel_path, ifnull(device, 0) FROM directories WHERE parent_id = ?`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var folder string
		var disk int64
		if err := rows.Scan(&folder, &disk); err != nil {
			return out, err
		}
		out.Folders = append(out.Folders, folder)
		out.Disks[folder] = disk
	}
	return out, rows.Err()
}

// FilePlace is a stored file and where it was last seen.
type FilePlace struct {
	ID      int64
	PathID  int64
	Root    string // the library path
	Rel     string // the file's path under it
	Missing bool
}

// SameFiles returns the stored media files of a library with exactly this size and modified
// time: the candidates for "this new file is that one, moved". It returns at most three; more
// than one that could have moved is too many to tell apart.
func (d *DB) SameFiles(ctx context.Context, libraryID, size, modifiedNS int64) ([]FilePlace, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT f.id, p.id, p.path, d.rel_path, f.name, f.missing_since_at IS NOT NULL
		FROM files f JOIN directories d ON d.id = f.directory_id JOIN library_paths p ON p.id = d.library_path_id
		WHERE f.size = ? AND f.modified_ns = ? AND f.role = 1 AND p.library_id = ? LIMIT 3`, size, modifiedNS, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FilePlace
	for rows.Next() {
		var f FilePlace
		var dir, name string
		if err := rows.Scan(&f.ID, &f.PathID, &f.Root, &dir, &name, &f.Missing); err != nil {
			return nil, err
		}
		f.Rel = path.Join(dir, name)
		out = append(out, f)
	}
	return out, rows.Err()
}

// FilesUnder counts the files stored under a library path that are not missing. A path that
// had files and now lists as empty is treated as not mounted, and nothing is marked missing.
func (d *DB) FilesUnder(ctx context.Context, pathID int64) (int, error) {
	var n int
	err := d.read.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM files f JOIN directories d ON d.id = f.directory_id
		WHERE d.library_path_id = ? AND f.missing_since_at IS NULL LIMIT 1)`, pathID).Scan(&n)
	return n, err
}

// MarkFolderGone marks every file under a folder missing: a complete listing of the folder's
// parent no longer has the folder. Files that play nothing are forgotten, and so are the
// folder rows left empty. It works a few hundred files at a time.
func (d *DB) MarkFolderGone(ctx context.Context, libraryID, pathID int64, rel string) error {
	const under = `library_path_id = ? AND (rel_path = ? OR rel_path > ? AND rel_path < ?)`
	args := []any{pathID, rel, rel + "/", rel + "0"}
	for {
		done := false
		err := d.write(ctx, Background, func(tx *sql.Tx) error {
			b := newBatchTx(tx)
			defer b.close()
			now := time.Now().UnixMilli()
			var changed []int64
			if err := goneFiles(b, now, &changed, `id IN (SELECT f.id FROM files f JOIN directories d ON d.id = f.directory_id
				WHERE f.missing_since_at IS NULL AND d.`+under+` LIMIT 300)`, args...); err != nil {
				return err
			}
			items := map[int64]bool{}
			for _, file := range changed {
				ids, err := queryInts(tx, `SELECT DISTINCT m.item_id FROM media_parts p JOIN media m ON m.id = p.media_id WHERE p.file_id = ?`, file)
				if err != nil {
					return err
				}
				for _, id := range ids {
					items[id] = true
				}
			}
			for id := range items {
				if err := settleItem(b, libraryID, id, now); err != nil {
					return err
				}
			}
			var left int
			if err := tx.QueryRow(`SELECT count(*) FROM (SELECT 1 FROM files f JOIN directories d ON d.id = f.directory_id
				WHERE f.missing_since_at IS NULL AND d.`+under+` LIMIT 1)`, args...).Scan(&left); err != nil {
				return err
			}
			if done = left == 0; !done {
				return nil
			}
			// Folder rows that hold nothing any more go, deepest first.
			for {
				res, err := tx.Exec(`DELETE FROM directories WHERE `+under+` AND NOT EXISTS (SELECT 1 FROM files WHERE directory_id = directories.id)
					AND NOT EXISTS (SELECT 1 FROM directories c WHERE c.parent_id = directories.id)`, args...)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					return nil
				}
			}
		})
		if err != nil || done {
			return err
		}
	}
}

// BeginScan numbers a new scan of a library and notes when it started.
func (d *DB) BeginScan(ctx context.Context, libraryID int64) (int64, error) {
	var scan int64
	err := d.write(ctx, Background, func(tx *sql.Tx) error {
		return tx.QueryRow(`UPDATE libraries SET scan_number = scan_number + 1, last_scan_started_at = ? WHERE id = ? RETURNING scan_number`,
			time.Now().UnixMilli(), libraryID).Scan(&scan)
	})
	return scan, err
}

// EndScan notes when a library's scan finished and how each of its folders was found.
func (d *DB) EndScan(ctx context.Context, libraryID int64, paths map[int64]error) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		for id, problem := range paths {
			state, text := "ok", any(nil)
			if problem != nil {
				state, text = "unavailable", problem.Error()
			}
			if _, err := tx.Exec(`UPDATE library_paths SET state = ?, last_error = ?, last_scan_at = ? WHERE id = ?`, state, text, now, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`UPDATE libraries SET last_scan_finished_at = ? WHERE id = ?`, now, libraryID)
		return err
	})
}

// SkippedFile is one entry of a library's Files skipped list.
type SkippedFile struct {
	Path    string // the folder of the library and the file's path under it
	Problem FileProblem
}

// SkippedFiles lists the files of a library the last scan could not use, by path.
func (d *DB) SkippedFiles(ctx context.Context, libraryID int64, offset, limit int) ([]SkippedFile, int, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	const from = ` FROM files f JOIN directories d ON d.id = f.directory_id JOIN library_paths p ON p.id = d.library_path_id
		WHERE f.problem IS NOT NULL AND f.missing_since_at IS NULL AND p.library_id = ?`
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT count(*)`+from, libraryID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.path, d.rel_path, f.name, f.problem`+from+` ORDER BY p.id, d.rel_path, f.name LIMIT ? OFFSET ?`,
		libraryID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []SkippedFile
	for rows.Next() {
		var root, dir, name string
		var s SkippedFile
		if err := rows.Scan(&root, &dir, &name, &s.Problem); err != nil {
			return nil, 0, err
		}
		s.Path = path.Join(root, dir, name)
		out = append(out, s)
	}
	return out, total, rows.Err()
}
