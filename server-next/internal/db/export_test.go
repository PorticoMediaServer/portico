package db

import (
	"context"
	"database/sql"
	"time"
)

// ChooseMatch marks a title's match as chosen by a person, which only the metadata editor's
// own writes do.
func ChooseMatch(d *DB, publicID string) error {
	_, err := d.writeConn.Exec(`UPDATE items SET match_state = ? WHERE public_id = ?`, MatchChosen, publicID)
	return err
}

// SetRareTag lets a test send tag-filtered lists down the path for common tags or the path
// for rare ones, whatever the size of its library.
func SetRareTag(uses int) { rareTag = uses }

// ExecForTest runs one statement, for a test to put the database in a state that no code on
// this branch can yet (what the metadata service will set).
func (d *DB) ExecForTest(ctx context.Context, query string, args ...any) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(query, args...)
		return err
	})
}

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
