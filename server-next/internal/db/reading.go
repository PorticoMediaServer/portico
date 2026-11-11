package db

import (
	"context"
	"database/sql"
	"errors"
)

// Book is what opening a book needs: its versions' files, and whether the reader may save
// the original file.
type Book struct {
	ID       int64
	PublicID string
	Kind     ItemKind
	Title    string
	Versions []BookVersion
	// MaySave says the profile, and its account, may download, which is what saving the
	// book's own file needs.
	MaySave bool
}

// ImageVersion is the name the scanner gives the one version of a comic made of a folder of
// pictures. It has a part for every page, in reading order.
const ImageVersion = "images"

// BookVersion is one file of a book. Path is empty for a file on remote storage. For a comic
// made of pictures, the file is its first page, Pages names every page in reading order, and
// Size and ModifiedNS are the pages' total size and newest change, which its fingerprint is
// made from.
type BookVersion struct {
	Pages      []string
	PublicID   string
	Name       string
	FileID     int64
	FileName   string
	Path       string
	Size       int64
	ModifiedNS int64
}

// Reading reports whether an item of this kind is read rather than played.
func (k ItemKind) Reading() bool { return k == KindBook || k == KindComic || k == KindIssue }

// Book returns a book, comic or issue the viewer may see, with each version's file that is
// not missing. Anything else, including a book the viewer may not see, is ErrNotFound.
func (d *DB) Book(ctx context.Context, v Viewer, publicID string) (Book, error) {
	var out Book
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.public_id, i.kind, i.title,
		(SELECT p.allow_downloads AND u.allow_downloads FROM profiles p JOIN users u ON u.id = p.user_id WHERE p.id = @viewer_profile)
		FROM items i WHERE i.public_id = @id AND `+acc.visible("i"), append(v.args(), sql.Named("id", publicID))...).
		Scan(&out.ID, &out.PublicID, &out.Kind, &out.Title, &out.MaySave)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !out.Kind.Reading() {
		return Book{}, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Versions, err = queryAll(ctx, tx, `SELECT m.public_id, ifnull(m.name, ''), f.id, f.name,
		CASE WHEN lp.remote_source_id IS NULL THEN rtrim(lp.path, '/') || '/' || CASE WHEN dir.rel_path = '' THEN '' ELSE dir.rel_path || '/' END || f.name
			ELSE '' END, f.size, f.modified_ns
		FROM media m JOIN media_parts mp ON mp.media_id = m.id AND mp.position = 0 JOIN files f ON f.id = mp.file_id
		JOIN directories dir ON dir.id = f.directory_id JOIN library_paths lp ON lp.id = dir.library_path_id
		WHERE m.item_id = ? AND f.missing_since_at IS NULL ORDER BY m.position, m.id`, []any{out.ID},
		func(b *BookVersion) []any {
			return []any{&b.PublicID, &b.Name, &b.FileID, &b.FileName, &b.Path, &b.Size, &b.ModifiedNS}
		})
	if err != nil {
		return out, err
	}
	for n := range out.Versions {
		if v := &out.Versions[n]; v.Name == ImageVersion {
			if v.Pages, v.Size, v.ModifiedNS, err = imagePages(ctx, tx, v.PublicID); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// imagePages lists the pages of a comic made of pictures, with their total size and newest change.
func imagePages(ctx context.Context, tx *sql.Tx, version string) (pages []string, size, newest int64, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT f.name, f.size, f.modified_ns FROM media m JOIN media_parts mp ON mp.media_id = m.id
		JOIN files f ON f.id = mp.file_id WHERE m.public_id = ? ORDER BY mp.position`, version)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	pages = []string{}
	for rows.Next() {
		var name string
		var s, m int64
		if err := rows.Scan(&name, &s, &m); err != nil {
			return nil, 0, 0, err
		}
		pages, size, newest = append(pages, name), size+s, max(newest, m)
	}
	return pages, size, newest, rows.Err()
}

// ImagePage is page n (from 0) of the comic made of pictures whose first page is the file
// firstPage, with the whole comic's total size and newest change, which its fingerprint is
// made from. A page that is not there is ErrNotFound.
func (d *DB) ImagePage(ctx context.Context, firstPage int64, n int) (page FileOnDisk, size, newest int64, err error) {
	var mediaID, pageFile int64
	err = d.read.QueryRowContext(ctx, `SELECT m.id, ifnull((SELECT sum(f.size) FROM media_parts p JOIN files f ON f.id = p.file_id WHERE p.media_id = m.id), 0),
		ifnull((SELECT max(f.modified_ns) FROM media_parts p JOIN files f ON f.id = p.file_id WHERE p.media_id = m.id), 0),
		ifnull((SELECT file_id FROM media_parts WHERE media_id = m.id AND position = ?), 0)
		FROM media_parts mp JOIN media m ON m.id = mp.media_id WHERE mp.file_id = ? AND mp.position = 0 AND m.name = ? LIMIT 1`,
		n, firstPage, ImageVersion).Scan(&mediaID, &size, &newest, &pageFile)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (pageFile == 0 || n < 0) {
		return page, 0, 0, ErrNotFound
	}
	if err != nil {
		return page, 0, 0, err
	}
	page, err = d.FileOnDisk(ctx, pageFile)
	return page, size, newest, err
}

// BookAccess reports whether the viewer may still see the book and the file is still one of
// its versions, and whether the viewer may save the file (the profile and its account may
// download). A book's address asks this, a few seconds apart, while it is read through.
func (d *DB) BookAccess(ctx context.Context, v Viewer, itemID, fileID int64) (visible, maySave bool, err error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return false, false, err
	}
	err = tx.QueryRowContext(ctx, `SELECT (SELECT p.allow_downloads AND u.allow_downloads FROM profiles p JOIN users u ON u.id = p.user_id
		WHERE p.id = @viewer_profile) FROM items i WHERE i.id = @item AND `+acc.visible("i")+`
		AND EXISTS (SELECT 1 FROM media m JOIN media_parts mp ON mp.media_id = m.id WHERE m.item_id = i.id AND mp.file_id = @file)`,
		append(v.args(), sql.Named("item", itemID), sql.Named("file", fileID))...).Scan(&maySave)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	return err == nil, maySave, err
}
