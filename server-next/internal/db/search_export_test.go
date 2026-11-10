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
