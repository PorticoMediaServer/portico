package db

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"slices"
	"strings"
	"time"
)

// LibraryFolder is one folder of a library as the owner sees it.
type LibraryFolder struct {
	ID         int64
	Path       string
	State      string // "ok", or "unavailable" when the last scan could not list it
	Error      string
	LastScanAt int64
}

// AdminLibrary is a library as its owner manages it.
type AdminLibrary struct {
	Library
	Revision       int64
	ScanStartedAt  int64
	ScanFinishedAt int64
	Folders        []LibraryFolder
}

// AdminLibraries lists every library with its folders and how the last scan found them.
// With a public id it returns that library alone.
func (d *DB) AdminLibraries(ctx context.Context, publicID string) ([]AdminLibrary, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT l.id, l.public_id, l.name, l.kind, ifnull(l.language, ''), l.position, l.created_at, l.updated_at,
		l.options, l.revision, ifnull(l.last_scan_started_at, 0), ifnull(l.last_scan_finished_at, 0), p.id, p.path, p.state,
		ifnull(p.last_error, ''), ifnull(p.last_scan_at, 0) FROM libraries l LEFT JOIN library_paths p ON p.library_id = l.id
		WHERE ? = '' OR l.public_id = ? ORDER BY l.position, l.id, p.id`, publicID, publicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminLibrary
	for rows.Next() {
		var l AdminLibrary
		var options, folderPath, state, problem sql.NullString
		var folderID, scanned sql.NullInt64
		if err := rows.Scan(&l.ID, &l.PublicID, &l.Name, &l.Kind, &l.Language, &l.Position, &l.CreatedAt, &l.UpdatedAt, &options, &l.Revision,
			&l.ScanStartedAt, &l.ScanFinishedAt, &folderID, &folderPath, &state, &problem, &scanned); err != nil {
			return nil, err
		}
		l.Options = parseOptions(options)
		if len(out) == 0 || out[len(out)-1].ID != l.ID {
			out = append(out, l)
		}
		if folderID.Valid {
			last := &out[len(out)-1]
			last.Paths = append(last.Paths, LibraryPath{ID: folderID.Int64, Path: folderPath.String})
			last.Folders = append(last.Folders, LibraryFolder{ID: folderID.Int64, Path: folderPath.String, State: state.String,
				Error: problem.String, LastScanAt: scanned.Int64})
		}
	}
	return out, rows.Err()
}

// LibraryEdit changes a library. Fields left nil stay as they are. Folders, when given, is the
// whole list: a folder no longer in it leaves the library with its titles.
type LibraryEdit struct {
	Name     *string
	Language *string
	Options  *LibraryOptions
	Folders  *[]string
}

// EditLibrary applies an edit. It reports whether a folder was added, which calls for a scan.
// The titles of a removed folder are taken out separately, by RemoveFolderTitles.
func (d *DB) EditLibrary(ctx context.Context, publicID string, edit LibraryEdit) (added bool, removed []int64, err error) {
	if edit.Options != nil {
		if err := edit.Options.check(); err != nil {
			return false, nil, err
		}
	}
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		added, removed = false, nil
		var id int64
		if err := tx.QueryRow(`SELECT id FROM libraries WHERE public_id = ?`, publicID).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		now := time.Now().UnixMilli()
		if edit.Name != nil {
			name := strings.TrimSpace(*edit.Name)
			if name == "" || len(name) > 200 {
				return invalid("a library name has 1 to 200 characters")
			}
			if _, err := tx.Exec(`UPDATE libraries SET name = ? WHERE id = ?`, name, id); err != nil {
				return err
			}
		}
		if edit.Language != nil {
			if _, err := tx.Exec(`UPDATE libraries SET language = nullif(?, '') WHERE id = ?`, *edit.Language, id); err != nil {
				return err
			}
		}
		if edit.Options != nil {
			if _, err := tx.Exec(`UPDATE libraries SET options = ? WHERE id = ?`, edit.Options.json(), id); err != nil {
				return err
			}
		}
		if edit.Folders != nil {
			if len(*edit.Folders) == 0 {
				return invalid("a library needs at least one folder")
			}
			have := map[string]int64{}
			rows, err := tx.Query(`SELECT id, path FROM library_paths WHERE library_id = ? AND remote_source_id IS NULL`, id)
			if err != nil {
				return err
			}
			for rows.Next() {
				var pathID int64
				var folder string
				if err := rows.Scan(&pathID, &folder); err != nil {
					rows.Close()
					return err
				}
				have[folder] = pathID
			}
			if err := rows.Close(); err != nil {
				return err
			}
			for _, folder := range *edit.Folders {
				if _, ok := have[folder]; ok {
					delete(have, folder)
					continue
				}
				if _, err := tx.Exec(`INSERT INTO library_paths (library_id, path) VALUES (?, ?)`, id, folder); err != nil {
					if isUnique(err) {
						continue
					}
					return err
				}
				added = true
			}
			for _, pathID := range have {
				removed = append(removed, pathID)
			}
		}
		if _, err := tx.Exec(`UPDATE libraries SET revision = revision + 1, updated_at = ? WHERE id = ?`, now, id); err != nil {
			return err
		}
		return appendChange(tx, Change{At: now, Topic: TopicLibrary, Op: OpUpdated, RefID: id, LibraryID: id})
	})
	return added, removed, err
}

func (o LibraryOptions) check() error {
	switch o.Depth {
	case "", DepthFiles, DepthBasic, DepthComplete:
	default:
		return invalid("unknown scan depth %q", o.Depth)
	}
	switch o.Trash.Mode {
	case "", TrashAfterDays, TrashAfterScan, TrashManual:
	default:
		return invalid("unknown trash rule %q", o.Trash.Mode)
	}
	if o.Trash.Days < 0 || o.Trash.Days > 3650 {
		return invalid("missing titles are removed after 1 to 3,650 days")
	}
	return nil
}

// RemoveFolder takes a folder out of a library: its titles leave with what each person had on
// them kept, in case they are added again, and then its file list goes. Files on disk are not
// touched. It works a few titles at a time.
func (d *DB) RemoveFolder(ctx context.Context, libraryID, pathID int64) error {
	return d.removeTitles(ctx, libraryID, `SELECT DISTINCT m.item_id FROM directories d JOIN files f ON f.directory_id = d.id
		JOIN media_parts p ON p.file_id = f.id JOIN media m ON m.id = p.media_id WHERE d.library_path_id = ? LIMIT 50`, []any{pathID},
		`DELETE FROM library_paths WHERE id = ?`, pathID)
}

// DeleteLibrary removes a library: its titles a few at a time, then the library itself. Files
// on disk are not touched.
func (d *DB) DeleteLibrary(ctx context.Context, libraryID int64) error {
	err := d.removeTitles(ctx, libraryID, `SELECT id FROM items WHERE library_id = ? AND parent_id IS NULL LIMIT 20`, []any{libraryID},
		`DELETE FROM library_paths WHERE library_id = ?`, libraryID)
	if err != nil {
		return err
	}
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM libraries WHERE id = ?`, libraryID); err != nil {
			return err
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicLibrary, Op: OpDeleted, RefID: libraryID, LibraryID: libraryID})
	})
}

// removeTitles removes the items a query picks, a batch for each transaction, until it picks
// none, and then runs the last statement.
func (d *DB) removeTitles(ctx context.Context, libraryID int64, pick string, args []any, last string, lastArg int64) error {
	for {
		done := false
		err := d.write(ctx, Background, func(tx *sql.Tx) error {
			picked, err := queryInts(tx, pick, args...)
			if err != nil {
				return err
			}
			if done = len(picked) == 0; done {
				_, err := tx.Exec(last, lastArg)
				return err
			}
			b := newBatchTx(tx)
			defer b.close()
			now := time.Now().UnixMilli()
			for _, id := range picked {
				var parent sql.NullInt64
				err := b.queryRow(`SELECT parent_id FROM items WHERE id = ?`, id).Scan(&parent)
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return err
				}
				if err := removeItem(b, libraryID, id, now); err != nil {
					return err
				}
				if err := settleItem(b, libraryID, parent.Int64, now); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil || done {
			return err
		}
	}
}

// DiskFile is a stored file and where it is on disk.
type DiskFile struct {
	ID         int64
	Root       string // the library folder it is under
	Path       string
	Size       int64
	ModifiedNS int64
	Remote     bool
}

// Deletion is what deleting a title or a version would take: Delete shows it first.
type Deletion struct {
	LibraryID   int64
	ItemID      int64
	MediaID     int64 // set when one version is being deleted
	Title       string
	AllowDelete bool
	Titles      int // the title and everything under it
	Files       []DiskFile
	Bytes       int64
	ListEntries int
	OtherStates int // other profiles' saved places and marks on these titles
	// Others are versions of other titles that are played from the same files: the second
	// episode of a file holding two, the other tracks of an album in one file. Deleting the
	// files removes them too.
	Others []SharedTitle
}

// SharedTitle is a version of another title played from a file that is being deleted.
type SharedTitle struct {
	ItemID   int64
	MediaID  int64
	PublicID string
	Title    string
}

const subtreeSQL = `WITH RECURSIVE t(id) AS (SELECT ? UNION ALL SELECT i.id FROM items i JOIN t ON i.parent_id = t.id) `

// ItemDeletion says what deleting a title would take, for the viewer who asks.
func (d *DB) ItemDeletion(ctx context.Context, v Viewer, publicID string) (Deletion, error) {
	return d.deletion(ctx, v, `i.public_id = ?`, publicID, false)
}

// VersionDeletion says what deleting one version of a title would take.
func (d *DB) VersionDeletion(ctx context.Context, v Viewer, versionID string) (Deletion, error) {
	return d.deletion(ctx, v, `i.id = (SELECT item_id FROM media WHERE public_id = ?)`, versionID, true)
}

func (d *DB) deletion(ctx context.Context, v Viewer, where, id string, version bool) (Deletion, error) {
	var out Deletion
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var options sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.library_id, i.title, l.options FROM items i JOIN libraries l ON l.id = i.library_id WHERE `+where, id).
		Scan(&out.ItemID, &out.LibraryID, &out.Title, &options)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.AllowDelete = parseOptions(options).AllowDelete
	medias := subtreeSQL + `SELECT m.id FROM media m JOIN t ON t.id = m.item_id`
	args := []any{out.ItemID}
	if version {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM media WHERE public_id = ?`, id).Scan(&out.MediaID); err != nil {
			return out, err
		}
		medias, args = `SELECT ?`, []any{out.MediaID}
		out.Titles = 0
	} else {
		if err := tx.QueryRowContext(ctx, subtreeSQL+`SELECT count(*), (SELECT count(*) FROM list_entries e JOIN t ON t.id = e.item_id),
			(SELECT count(*) FROM user_item_state s JOIN t ON t.id = s.item_id WHERE s.profile_id <> ?) FROM t`, out.ItemID, v.ProfileID).
			Scan(&out.Titles, &out.ListEntries, &out.OtherStates); err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT f.id, lp.path, d.rel_path, f.name, f.size, f.modified_ns, lp.remote_source_id IS NOT NULL
		FROM files f JOIN directories d ON d.id = f.directory_id JOIN library_paths lp ON lp.id = d.library_path_id
		WHERE f.missing_since_at IS NULL AND (f.id IN (SELECT p.file_id FROM media_parts p WHERE p.media_id IN (`+medias+`))
		OR f.media_id IN (`+medias+`))`, append(args, args...)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var f DiskFile
		var root, dir, name string
		if err := rows.Scan(&f.ID, &root, &dir, &name, &f.Size, &f.ModifiedNS, &f.Remote); err != nil {
			return out, err
		}
		f.Root, f.Path = root, path.Join(root, dir, name)
		out.Files = append(out.Files, f)
		out.Bytes += f.Size
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	for _, f := range out.Files {
		others, err := tx.QueryContext(ctx, `SELECT DISTINCT i.id, m.id, i.public_id, i.title FROM media_parts p JOIN media m ON m.id = p.media_id
			JOIN items i ON i.id = m.item_id WHERE p.file_id = ? AND m.id NOT IN (`+medias+`) ORDER BY i.id`, append([]any{f.ID}, args...)...)
		if err != nil {
			return out, err
		}
		for others.Next() {
			var o SharedTitle
			if err := others.Scan(&o.ItemID, &o.MediaID, &o.PublicID, &o.Title); err != nil {
				others.Close()
				return out, err
			}
			if !slices.ContainsFunc(out.Others, func(have SharedTitle) bool { return have.MediaID == o.MediaID }) {
				out.Others = append(out.Others, o)
			}
		}
		if err := others.Close(); err != nil {
			return out, err
		}
	}
	return out, nil
}

// Deleted records that files were deleted from disk. With every file of the deletion gone
// the title (or the one version) is removed at once, never Missing; what each person had on a
// removed title is kept in case it returns. Files that were kept leave their title in place,
// and the deleted ones are marked missing.
func (d *DB) Deleted(ctx context.Context, del Deletion, deleted []int64) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		now := time.Now().UnixMilli()
		var parent sql.NullInt64
		if err := b.queryRow(`SELECT parent_id FROM items WHERE id = ?`, del.ItemID).Scan(&parent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		switch {
		case len(deleted) < len(del.Files):
			var changed []int64
			for _, id := range deleted {
				if err := goneFiles(b, now, &changed, `id = ?`, id); err != nil {
					return err
				}
			}
			items, err := queryInts(tx, subtreeSQL+`SELECT id FROM t`, del.ItemID)
			if err != nil {
				return err
			}
			for _, other := range del.Others {
				items = append(items, other.ItemID)
			}
			for _, id := range items {
				if err := settleItem(b, del.LibraryID, id, now); err != nil {
					return err
				}
			}
			return nil
		}
		// The other titles played from these files go with them: a version whose file is
		// deleted is gone, and a title with no version left is removed, never Missing.
		for _, other := range del.Others {
			if _, err := b.exec(`DELETE FROM media WHERE id = ?`, other.MediaID); err != nil {
				return err
			}
		}
		others := func() error {
			for _, other := range del.Others {
				var left int
				var up sql.NullInt64
				err := b.queryRow(`SELECT (SELECT count(*) FROM media WHERE item_id = i.id), i.parent_id FROM items i WHERE i.id = ?`, other.ItemID).Scan(&left, &up)
				if errors.Is(err, sql.ErrNoRows) {
					continue // already removed with the first of its versions
				}
				if err != nil {
					return err
				}
				if left > 0 {
					err = settleItem(b, del.LibraryID, other.ItemID, now)
				} else if err = removeItem(b, del.LibraryID, other.ItemID, now); err == nil {
					err = settleItem(b, del.LibraryID, up.Int64, now)
				}
				if err != nil {
					return err
				}
			}
			return nil
		}
		switch {
		case del.MediaID != 0:
			for _, id := range deleted {
				if _, err := b.exec(`DELETE FROM files WHERE id = ?`, id); err != nil {
					return err
				}
			}
			if _, err := b.exec(`DELETE FROM media WHERE id = ?`, del.MediaID); err != nil {
				return err
			}
			if err := appendChange(tx, Change{At: now, Topic: TopicItem, Op: OpUpdated, RefID: del.ItemID, LibraryID: del.LibraryID}); err != nil {
				return err
			}
			if err := settleItem(b, del.LibraryID, del.ItemID, now); err != nil {
				return err
			}
			return others()
		}
		if err := removeItem(b, del.LibraryID, del.ItemID, now); err != nil {
			return err
		}
		if err := settleItem(b, del.LibraryID, parent.Int64, now); err != nil {
			return err
		}
		return others()
	})
}
