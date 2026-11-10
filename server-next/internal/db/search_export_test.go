package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// SetSearchFolding stands in for a server built with other Unicode tables or word rules.
func SetSearchFolding(stamp string) (restore func()) {
	old := searchFolding
	searchFolding = stamp
	return func() { searchFolding = old }
}

// SetSearchCheckCap lets a small library reach the cap on how many matches one request checks.
func SetSearchCheckCap(matches int) (restore func()) {
	old := searchCheckCap
	searchCheckCap = matches
	return func() { searchCheckCap = old }
}

// RenameItem and DeleteItem stand in for the metadata editor and the library's delete, which
// are not built yet. Each writes what those will: the row, and its change.
func (d *DB) RenameItem(ctx context.Context, publicID, title string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var id, library int64
		if err := tx.QueryRow(`UPDATE items SET title = ?, sort_title = ? WHERE public_id = ? RETURNING id, library_id`,
			title, sorttext.Key(title, "", "en"), publicID).Scan(&id, &library); err != nil {
			return err
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicItem, Op: OpUpdated, RefID: id, LibraryID: library})
	})
}

func (d *DB) DeleteItem(ctx context.Context, publicID string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var id, library int64
		if err := tx.QueryRow(`DELETE FROM items WHERE public_id = ? RETURNING id, library_id`, publicID).Scan(&id, &library); err != nil {
			return err
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicItem, Op: OpDeleted, RefID: id, LibraryID: library})
	})
}

// LabelItem stands in for the label editor, which is not built yet. It writes what that will:
// the tag on the title, and the title's change.
func (d *DB) LabelItem(ctx context.Context, publicID, tagPublicID string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var id, library int64
		if err := tx.QueryRow(`INSERT INTO item_tags (item_id, tag_id) SELECT i.id, t.id FROM items i, tags t
			WHERE i.public_id = ? AND t.public_id = ? RETURNING item_id, (SELECT library_id FROM items WHERE id = item_id)`,
			publicID, tagPublicID).Scan(&id, &library); err != nil {
			return err
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicItem, Op: OpUpdated, RefID: id, LibraryID: library})
	})
}

// RateShow does what a match, a refresh or the metadata editor does to a show's rating: the
// show and everything under it with no rating of its own, and one change, for the show.
func (d *DB) RateShow(ctx context.Context, publicID string, age int) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var id, library int64
		if err := tx.QueryRow(`UPDATE items SET rating_age = ? WHERE public_id = ? RETURNING id, library_id`, age, publicID).Scan(&id, &library); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET rating_age = ? WHERE root_id = ? AND content_rating IS NULL`, age, id); err != nil {
			return err
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicItem, Op: OpUpdated, RefID: id, LibraryID: library})
	})
}
