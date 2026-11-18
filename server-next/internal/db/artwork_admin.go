package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// A title's pictures are images rows, one a role; selectPicture decides which is shown. The
// bytes are in the picture store under file_hash. A provider's picture is recorded with the
// address it came from (source_url), so it is fetched once.

// ImageRoleByName reads a role as the API names it.
var ImageRoleByName = func() map[string]ImageRole {
	out := map[string]ImageRole{}
	for role, name := range imageRoleNames {
		out[name] = role
	}
	return out
}()

// NewPicture is a picture to record for a title.
type NewPicture struct {
	Role      ImageRole
	Hash      string // its name in the picture store; "" for a provider's picture not fetched
	SourceURL string
	Width     int
	Height    int
	Language  string // the language of any words on it; "" for none
	// Source is, for UploadPicture, ImageFromFrame for a picture made from the video at a
	// person's asking; anything else is an upload.
	Source ImageSource
}

// ProviderPictureURLs are the addresses of a title's provider pictures already fetched.
func (d *DB) ProviderPictureURLs(ctx context.Context, itemID int64) (map[string]bool, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT source_url FROM images WHERE item_id = ? AND source = ? AND file_hash IS NOT NULL
		AND source_url IS NOT NULL`, itemID, ImageFromProvider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, err
		}
		out[url] = true
	}
	return out, rows.Err()
}

// AddProviderPictures records a provider's pictures of a title, after the server fetched them,
// and works out again which is shown for each role: a picture beside the media, or one a
// person chose, still comes first. One already recorded by its address is passed over.
func (d *DB) AddProviderPictures(ctx context.Context, p Priority, itemID int64, pictures []NewPicture) error {
	if len(pictures) == 0 {
		return nil
	}
	return d.write(ctx, p, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		var library int64
		if err := b.queryRow(`SELECT library_id FROM items WHERE id = ?`, itemID).Scan(&library); errors.Is(err, sql.ErrNoRows) {
			return nil // gone since it was asked about
		} else if err != nil {
			return err
		}
		roles := map[ImageRole]bool{}
		for _, pic := range pictures {
			if err := checkCode("images.role", imageRoleNames, pic.Role); err != nil {
				return err
			}
			res, err := b.exec(`INSERT INTO images (item_id, role, source, file_hash, source_url, width, height, language, position)
				SELECT ?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, (SELECT ifnull(max(position), -1) + 1 FROM images WHERE item_id = ?1 AND role = ?2)
				WHERE NOT EXISTS (SELECT 1 FROM images WHERE item_id = ?1 AND source = ?3 AND source_url = ?5)`,
				itemID, pic.Role, ImageFromProvider, nz(pic.Hash), pic.SourceURL, nz(pic.Width), nz(pic.Height), nz(pic.Language))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 1 {
				roles[pic.Role] = true
			}
		}
		for role := range roles {
			if err := selectPicture(b, itemID, role); err != nil {
				return err
			}
		}
		if len(roles) == 0 {
			return nil
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicItem, Op: OpUpdated, RefID: itemID, LibraryID: library})
	})
}

// Picture is one picture a title could show for a role.
type Picture struct {
	ID       string
	Role     ImageRole
	Source   ImageSource
	Hash     string
	Width    int
	Height   int
	Language string
	Selected bool
	Locked   bool
}

// Pictures lists the pictures a title has for a role, the one shown first, then in the order
// selectPicture would take them.
func (d *DB) Pictures(ctx context.Context, publicID string, role ImageRole) ([]Picture, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE public_id = ?`, publicID).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return queryAll(ctx, tx, `SELECT id, role, source, file_hash, ifnull(width, 0), ifnull(height, 0), ifnull(language, ''), selected, locked
		FROM images WHERE item_id = ? AND role = ? AND file_hash IS NOT NULL ORDER BY selected DESC, source, position, id`, []any{id, role},
		func(p *Picture) []any {
			return []any{&p.ID, &p.Role, &p.Source, &p.Hash, &p.Width, &p.Height, &p.Language, &p.Selected, &p.Locked}
		})
}

// pictureEdit is one person's change to a title's pictures, in one transaction with its
// History entry. do runs inside it with the title's id.
func (d *DB) pictureEdit(ctx context.Context, publicID string, role ImageRole, do func(b *batchTx, itemID int64) error) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		var id, library int64
		if err := b.queryRow(`SELECT id, library_id FROM items WHERE public_id = ?`, publicID).Scan(&id, &library); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		if err := remember(b, id, HistoryArtwork, now); err != nil {
			return err
		}
		if err := do(b, id); err != nil {
			return err
		}
		if err := selectPicture(b, id, role); err != nil {
			return err
		}
		if err := rememberAfter(b, id); err != nil {
			return err
		}
		if _, err := b.exec(`UPDATE items SET updated_at = ?, revision = revision + 1 WHERE id = ?`, now, id); err != nil {
			return err
		}
		return appendChange(tx, Change{At: now, Topic: TopicItem, Op: OpUpdated, RefID: id, LibraryID: library})
	})
}

// choose makes one picture the role's choice: shown and locked, so no scan or refresh changes it.
func choose(b *batchTx, itemID int64, role ImageRole, imageID int64) error {
	_, err := b.exec(`UPDATE images SET locked = (id = ?1), selected = (id = ?1) WHERE item_id = ?2 AND role = ?3`, imageID, itemID, role)
	return err
}

// ChoosePicture is Use this: the picture is shown for its role and locked.
func (d *DB) ChoosePicture(ctx context.Context, publicID string, role ImageRole, imageID string) error {
	image, err := strconv.ParseInt(imageID, 10, 64)
	if err != nil {
		return ErrNotFound
	}
	return d.pictureEdit(ctx, publicID, role, func(b *batchTx, itemID int64) error {
		var has bool
		if err := b.queryRow(`SELECT EXISTS (SELECT 1 FROM images WHERE id = ? AND item_id = ? AND role = ? AND file_hash IS NOT NULL)`,
			image, itemID, role).Scan(&has); err != nil {
			return err
		}
		if !has {
			return ErrNotFound
		}
		return choose(b, itemID, role, image)
	})
}

// UnlockPicture lets the best picture of a role be shown again, as if no one had chosen.
func (d *DB) UnlockPicture(ctx context.Context, publicID string, role ImageRole) error {
	return d.pictureEdit(ctx, publicID, role, func(b *batchTx, itemID int64) error {
		_, err := b.exec(`UPDATE images SET locked = 0 WHERE item_id = ? AND role = ?`, itemID, role)
		return err
	})
}

// UploadPicture records a picture a person uploaded, or had made from the video (pic.Source),
// already in the picture store, as the role's choice. The same picture again is the same row.
// It returns the picture's id.
func (d *DB) UploadPicture(ctx context.Context, publicID string, pic NewPicture) (string, error) {
	var image int64
	source := ImageFromUpload
	if pic.Source == ImageFromFrame {
		source = ImageFromFrame
	}
	err := d.pictureEdit(ctx, publicID, pic.Role, func(b *batchTx, itemID int64) error {
		err := b.queryRow(`SELECT id FROM images WHERE item_id = ? AND role = ? AND source = ? AND file_hash = ?`, itemID, pic.Role,
			source, pic.Hash).Scan(&image)
		if errors.Is(err, sql.ErrNoRows) {
			res, err := b.exec(`INSERT INTO images (item_id, role, source, file_hash, width, height, position) VALUES (?, ?, ?, ?, ?, ?, 0)`,
				itemID, pic.Role, source, pic.Hash, nz(pic.Width), nz(pic.Height))
			if err != nil {
				return err
			}
			image, _ = res.LastInsertId()
		} else if err != nil {
			return err
		}
		return choose(b, itemID, pic.Role, image)
	})
	return strconv.FormatInt(image, 10), err
}

// RemovePicture removes an uploaded picture; the next best is shown for its role. Only an
// upload can be removed: a picture beside the media goes with its file, and a provider's
// stays to choose from.
func (d *DB) RemovePicture(ctx context.Context, publicID, imageID string) error {
	image, err := strconv.ParseInt(imageID, 10, 64)
	if err != nil {
		return ErrNotFound
	}
	var role ImageRole
	var source ImageSource
	if err := d.read.QueryRowContext(ctx, `SELECT g.role, g.source FROM images g JOIN items i ON i.id = g.item_id WHERE g.id = ? AND i.public_id = ?`,
		image, publicID).Scan(&role, &source); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if source != ImageFromUpload {
		return ErrConflict
	}
	return d.pictureEdit(ctx, publicID, role, func(b *batchTx, itemID int64) error {
		_, err := b.exec(`DELETE FROM images WHERE id = ? AND item_id = ?`, image, itemID)
		return err
	})
}

// pictureNamers are the tables that name a stored picture, each read a page at a time from a
// key: the key and a picture's name, NULL for a row that names none. A new place that names a
// stored picture is added here, or the sweep takes its pictures.
var pictureNamers = []string{
	// Titles' (collections' too), people's and profiles' pictures, and made episode pictures.
	`SELECT id, file_hash FROM images WHERE id > ? ORDER BY id LIMIT ?`,
	// Every channel's logo, live and library channels alike.
	`SELECT id, logo_hash FROM channels WHERE id > ? ORDER BY id LIMIT ?`,
	`SELECT p.media_id, s.file_hash FROM (SELECT media_id FROM scrub_previews WHERE media_id > ? ORDER BY media_id LIMIT ?) p
		LEFT JOIN scrub_preview_sheets s ON s.media_id = p.media_id`,
	// A removed upload that a History entry still names is kept, so Undo can put it back.
	`SELECT h.id, u.value ->> '$.hash' FROM (SELECT id, before FROM metadata_history WHERE id > ? ORDER BY id LIMIT ?) h
		LEFT JOIN json_each(h.before, '$.uploads') u`,
}

// PictureHashes calls fn with the name of every picture the server keeps in its picture store,
// in no order and perhaps more than once. It reads each table by its key a page at a time, so
// no read sorts or holds a whole table, all in one read so a name moving from one table to
// another (an Undo) is not missed. It is for the daily tidy-up, not for a request.
func (d *DB) PictureHashes(ctx context.Context, fn func(hash string) error) error {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range pictureNamers {
		for after, more := int64(0), true; more; {
			more = false
			rows, err := tx.QueryContext(ctx, query, after, 1000)
			if err != nil {
				return err
			}
			for rows.Next() {
				var hash sql.NullString
				if err := rows.Scan(&after, &hash); err != nil {
					rows.Close()
					return err
				}
				more = true
				if hash.Valid {
					if err := fn(hash.String); err != nil {
						rows.Close()
						return err
					}
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// PictureToFetch is a provider's picture a lookup recorded and nobody has fetched yet.
type PictureToFetch struct {
	ID        int64
	ItemID    int64
	Role      ImageRole
	SourceURL string
}

// PicturesToFetch lists a library's provider pictures recorded without their file, after an
// id, a page at a time: what the picture pass fetches. It reads the images_to_fetch index,
// which holds only those, and each one's title by its key.
func (d *DB) PicturesToFetch(ctx context.Context, libraryID, after int64, limit int) ([]PictureToFetch, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryAll(ctx, tx, `SELECT i.id, i.item_id, i.role, i.source_url FROM images i CROSS JOIN items t ON t.id = i.item_id
		WHERE i.file_hash IS NULL AND i.source = 3 AND i.id > ? AND t.library_id = ? AND i.source_url IS NOT NULL
		ORDER BY i.id LIMIT ?`, []any{after, libraryID, limit},
		func(p *PictureToFetch) []any { return []any{&p.ID, &p.ItemID, &p.Role, &p.SourceURL} })
}

// PictureFetched records the file of a picture the pass fetched, and shows it where nothing a
// person chose or a local picture comes first (selectPicture). A row removed since is left.
func (d *DB) PictureFetched(ctx context.Context, id int64, hash string) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		var item, library int64
		var role ImageRole
		err := b.queryRow(`SELECT i.item_id, i.role, t.library_id FROM images i JOIN items t ON t.id = i.item_id WHERE i.id = ?`, id).
			Scan(&item, &role, &library)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := b.exec(`UPDATE images SET file_hash = ? WHERE id = ? AND file_hash IS NULL`, hash, id); err != nil {
			return err
		}
		if err := selectPicture(b, item, role); err != nil {
			return err
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicItem, Op: OpUpdated, RefID: item, LibraryID: library})
	})
}

// PictureGone removes a recorded picture whose address the provider no longer serves, or that
// is not a picture, so the pass does not ask for it again. The next refresh records it anew
// if the provider still names it.
func (d *DB) PictureGone(ctx context.Context, id int64) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM images WHERE id = ? AND file_hash IS NULL AND source = 3`, id)
		return err
	})
}

// ChosenProviderPicture is the provider's picture a title shows for a role, for Re-fetch: its
// id and the address it was fetched from. ErrNotFound when the role shows no provider's picture.
func (d *DB) ChosenProviderPicture(ctx context.Context, publicID string, role ImageRole) (int64, string, error) {
	var id int64
	var address string
	err := d.read.QueryRowContext(ctx, `SELECT g.id, g.source_url FROM images g JOIN items i ON i.id = g.item_id
		WHERE i.public_id = ? AND g.role = ? AND g.selected = 1 AND g.source = ? AND g.source_url IS NOT NULL`,
		publicID, role, ImageFromProvider).Scan(&id, &address)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrNotFound
	}
	return id, address, err
}

// PictureRefetched records the file a provider's picture was downloaded to again (Re-fetch),
// in place: it keeps its choice and its lock.
func (d *DB) PictureRefetched(ctx context.Context, imageID int64, hash string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var item, library int64
		err := tx.QueryRow(`UPDATE images SET file_hash = ? WHERE id = ? AND source = ? RETURNING item_id,
			(SELECT library_id FROM items WHERE id = images.item_id)`, hash, imageID, ImageFromProvider).Scan(&item, &library)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicItem, Op: OpUpdated, RefID: item, LibraryID: library})
	})
}
