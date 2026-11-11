package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
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
	// Next is the next volume of the book's series that the viewer may see, offered when this
	// one is finished; nil when it is the last, or not in a series.
	Next *ItemRef
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
	if err != nil {
		return out, err
	}
	out.Next, err = nextVolume(ctx, tx, v, acc, out.ID)
	return out, err
}

// BookAccess reports whether the sign-in that was given a book's address is still live and on
// the viewer's profile, the viewer may still see the book and the file is still one of its
// versions, and whether the viewer may save the file (the profile and its account may
// download). A book's address asks this, a few seconds apart, while it is read through. The
// sign-in is read here, and not only from the in-memory marks, because those are dropped once
// no access token could still carry them (16 minutes), while an address lives for hours.
func (d *DB) BookAccess(ctx context.Context, v Viewer, sessionID, itemID, fileID int64, now time.Time) (visible, maySave bool, err error) {
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
		AND EXISTS (SELECT 1 FROM media m JOIN media_parts mp ON mp.media_id = m.id WHERE m.item_id = i.id AND mp.file_id = @file)
		AND EXISTS (SELECT 1 FROM sessions s WHERE s.id = @session AND s.profile_id = @viewer_profile AND s.revoked_at IS NULL
			AND s.expires_at > @now)`,
		append(v.args(), sql.Named("item", itemID), sql.Named("file", fileID), sql.Named("session", sessionID),
			sql.Named("now", now.UnixMilli()))...).Scan(&maySave)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	return err == nil, maySave, err
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

// volumeOrder is the order of a series' volumes: by number, then those with none by title.
const volumeOrder = `i.index_number IS NULL, i.index_number, i.sort_title, i.id`

// nextVolume is the volume after this one in its series, in volumeOrder: a book in a series
// of books, or an issue of a comic series.
func nextVolume(ctx context.Context, tx *sql.Tx, v Viewer, acc access, id int64) (*ItemRef, error) {
	// The volumes beside this one, as (holder, relation) of item_relations or as the children
	// of its parent.
	siblings := `SELECT i.* FROM item_relations r JOIN items i ON i.id = r.related_item_id
		WHERE r.item_id = (SELECT item_id FROM item_relations WHERE related_item_id = @me AND kind = @series LIMIT 1) AND r.kind = @series`
	var parent sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT parent_id FROM items WHERE id = ? AND kind = ?`, id, KindIssue).Scan(&parent); err == nil && parent.Valid {
		siblings = `SELECT * FROM items WHERE parent_id = @parent AND kind = @issue`
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	ref := &ItemRef{}
	err := tx.QueryRowContext(ctx, `WITH me AS (SELECT index_number IS NULL AS u, index_number AS n, sort_title AS t, id FROM items WHERE id = @me)
		SELECT i.public_id, i.kind, i.title, i.index_number FROM (`+siblings+`) i, me
		WHERE (i.index_number IS NULL, ifnull(i.index_number, 0), i.sort_title, i.id) > (me.u, ifnull(me.n, 0), me.t, me.id)
		AND `+acc.visible("i")+` ORDER BY `+volumeOrder+` LIMIT 1`,
		append(v.args(), sql.Named("me", id), sql.Named("series", RelationSeriesMember), sql.Named("parent", parent.Int64),
			sql.Named("issue", KindIssue))...).Scan(&ref.PublicID, &ref.Kind, &ref.Title, &ref.Index)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return ref, err
}
