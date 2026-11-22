package db

import (
	"context"
	"database/sql"
)

// BookTextFile is the file of one version of a book, as the book-text index reads it.
type BookTextFile struct {
	FileID     int64
	FileName   string
	Size       int64
	ModifiedNS int64
}

// BookTextFiles lists the file of every version of every book that is not missing. Which of
// them can be read for words (EPUB, and what the reader rebuilds as one) is the reader's call.
func (d *DB) BookTextFiles(ctx context.Context) ([]BookTextFile, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryAll(ctx, tx, `SELECT DISTINCT f.id, f.name, f.size, f.modified_ns
		FROM items i JOIN media m ON m.item_id = i.id JOIN media_parts mp ON mp.media_id = m.id AND mp.position = 0
		JOIN files f ON f.id = mp.file_id
		WHERE i.kind = ? AND ifnull(m.name, '') <> ? AND f.missing_since_at IS NULL ORDER BY f.id`, []any{KindBook, ImageVersion},
		func(b *BookTextFile) []any { return []any{&b.FileID, &b.FileName, &b.Size, &b.ModifiedNS} })
}

// BookTextOwner is the book a file of the book-text index belongs to, as one viewer sees it.
type BookTextOwner struct {
	LibraryID  string
	ItemID     string
	VersionID  string
	Title      string
	CoverHash  string
	Size       int64
	ModifiedNS int64
}

// BookTextOwners maps each of the files named to the book it is a version of, leaving out a
// file that is missing or belongs to no book the viewer may see, and, when libraryID is not
// empty, one in any other library.
func (d *DB) BookTextOwners(ctx context.Context, v Viewer, fileIDs []int64, libraryID string) (map[int64]BookTextOwner, error) {
	out := map[int64]BookTextOwner{}
	if len(fileIDs) == 0 {
		return out, nil
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT f.id, l.public_id, i.public_id, m.public_id, i.title,
		ifnull((SELECT im.file_hash FROM images im WHERE im.item_id = i.id AND im.selected = 1 AND im.role IN (@cover, @poster)
			AND im.file_hash IS NOT NULL ORDER BY im.role = @cover DESC LIMIT 1), ''), f.size, f.modified_ns
		FROM files f JOIN media_parts mp ON mp.file_id = f.id AND mp.position = 0 JOIN media m ON m.id = mp.media_id
		JOIN items i ON i.id = m.item_id JOIN libraries l ON l.id = i.library_id
		WHERE f.id IN `+intList(fileIDs)+` AND f.missing_since_at IS NULL AND i.kind = @kind AND (@library = '' OR l.public_id = @library)
		AND `+acc.visible("i")+` ORDER BY f.id, i.id`,
		append(v.args(), sql.Named("cover", ImageCover), sql.Named("poster", ImagePoster), sql.Named("kind", KindBook), sql.Named("library", libraryID))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var fileID int64
		var o BookTextOwner
		if err := rows.Scan(&fileID, &o.LibraryID, &o.ItemID, &o.VersionID, &o.Title, &o.CoverHash, &o.Size, &o.ModifiedNS); err != nil {
			return nil, err
		}
		if _, seen := out[fileID]; !seen {
			out[fileID] = o
		}
	}
	return out, rows.Err()
}

// VisibleBookFiles lists the files of the books the viewer may see that are not missing, in
// one library when libraryID is not empty, so a search reads only text the viewer may find.
func (d *DB) VisibleBookFiles(ctx context.Context, v Viewer, libraryID string) ([]int64, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT mp.file_id FROM items i JOIN libraries l ON l.id = i.library_id
		JOIN media m ON m.item_id = i.id JOIN media_parts mp ON mp.media_id = m.id AND mp.position = 0 JOIN files f ON f.id = mp.file_id
		WHERE i.kind = @kind AND f.missing_since_at IS NULL AND (@library = '' OR l.public_id = @library) AND `+acc.visible("i"),
		append(v.args(), sql.Named("kind", KindBook), sql.Named("library", libraryID))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
