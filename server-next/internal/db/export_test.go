package db

import (
	"context"
	"database/sql"
)

// SetRareTag lets a test send tag-filtered lists down the path for common tags or the path
// for rare ones, whatever the size of its library.
func SetRareTag(uses int) { rareTag = uses }

// SetChangesKeepRows lets a test prune the changes table without writing 200,000 rows first.
func SetChangesKeepRows(rows int) { changesKeepRows = rows }

// SetRatingAge changes the rating of a title and of what is under it that has none of its
// own, the way a metadata refresh does, for a test of what is counted afterwards.
func SetRatingAge(d *DB, publicID string, age int) error {
	return d.write(context.Background(), Background, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		var id int64
		if err := b.queryRow(`SELECT id FROM items WHERE public_id = ?`, publicID).Scan(&id); err != nil {
			return err
		}
		if _, err := b.exec(`UPDATE items SET rating_age = ?1 WHERE id = ?2 OR root_id = ?2`, age, id); err != nil {
			return err
		}
		return b.ratingChanged(id)
	})
}

// WidthsNotKept counts the items whose stored widest version is not what their versions
// say, and the items that have one at all.
func WidthsNotKept(d *DB) (wrong, set int, err error) {
	err = d.read.QueryRow(`SELECT count(*) FILTER (WHERE best_width IS NOT (SELECT max(m.width) FROM media m WHERE m.item_id = items.id)),
		count(best_width) FROM items`).Scan(&wrong, &set)
	return wrong, set, err
}
