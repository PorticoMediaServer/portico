package db

import (
	"context"
	"database/sql"
	"encoding/json"
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
	// Place is the viewer's place in the book as the reader sent it, or "".
	Place string
}

// BookVersion is one file of a book. Path is empty for a file on remote storage.
type BookVersion struct {
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
		(SELECT p.allow_downloads AND u.allow_downloads FROM profiles p JOIN users u ON u.id = p.user_id WHERE p.id = @viewer_profile),
		ifnull(s.position_text, '')
		FROM items i LEFT JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = i.id
		WHERE i.public_id = @id AND `+acc.visible("i"), append(v.args(), sql.Named("id", publicID))...).
		Scan(&out.ID, &out.PublicID, &out.Kind, &out.Title, &out.MaySave, &out.Place)
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
	return out, err
}

// Place is what the server reads of a place in a book. The rest of it (the exact spot, in the
// reader's own notation) is the reader's, and is kept as it was sent.
type Place struct {
	// Progression is how far through the book, 0 to 1: what places it in another version or
	// in a replaced file.
	Progression float64 `json:"progression"`
	// Fingerprint is the file the place was made in (see reading.Fingerprint).
	Fingerprint string `json:"fingerprint"`
	// AtEnd says the reader reached the end of the main text, or the last page.
	AtEnd bool `json:"atEnd"`
	valid bool
}

// maxPlace bounds a place in a book.
const maxPlace = 4096

func readPlace(raw string) Place {
	var p struct {
		Place
		Progression *float64 `json:"progression"`
	}
	if len(raw) > maxPlace || json.Unmarshal([]byte(raw), &p) != nil || p.Progression == nil || *p.Progression < 0 || *p.Progression > 1 {
		return Place{}
	}
	p.Place.Progression, p.Place.valid = *p.Progression, true
	return p.Place
}

// ReadPlace reads a stored place; a place that is not one reads as the start.
func ReadPlace(raw string) Place { return readPlace(raw) }
