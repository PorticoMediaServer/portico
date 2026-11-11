package db

import (
	"context"
	"database/sql"
	"time"
)

// SetRareTag lets a test send tag-filtered lists down the path for common tags or the path
// for rare ones, whatever the size of its library.
func SetRareTag(uses int) { rareTag = uses }

// SetChangesKeepRows lets a test prune the changes table without writing 200,000 rows first.
func SetChangesKeepRows(rows int) { changesKeepRows = rows }

// WidthsNotKept counts the items whose stored widest version is not what their versions
// say, and the items that have one at all.
func WidthsNotKept(d *DB) (wrong, set int, err error) {
	err = d.read.QueryRow(`SELECT count(*) FILTER (WHERE best_width IS NOT (SELECT max(m.width) FROM media m WHERE m.item_id = items.id
		AND m.id NOT IN (SELECT p.media_id FROM media_parts p JOIN files f ON f.id = p.file_id WHERE f.missing_since_at IS NOT NULL))),
		count(best_width) FROM items`).Scan(&wrong, &set)
	return wrong, set, err
}

// VersionGone marks the files of one version missing, as a scan that no longer finds them
// does, and returns the width now stored on its title.
func VersionGone(d *DB, versionID string) (width int, err error) {
	err = d.write(context.Background(), Background, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		now := time.Now().UnixMilli()
		var item, library int64
		if err := b.queryRow(`SELECT m.item_id, i.library_id FROM media m JOIN items i ON i.id = m.item_id WHERE m.public_id = ?`, versionID).Scan(&item, &library); err != nil {
			return err
		}
		if _, err := b.exec(`UPDATE files SET missing_since_at = ? WHERE id IN (SELECT p.file_id FROM media_parts p
			JOIN media m ON m.id = p.media_id WHERE m.public_id = ?)`, now, versionID); err != nil {
			return err
		}
		if err := settleItem(b, library, item, now); err != nil {
			return err
		}
		return b.queryRow(`SELECT ifnull(best_width, 0) FROM items WHERE id = ?`, item).Scan(&width)
	})
	return width, err
}
