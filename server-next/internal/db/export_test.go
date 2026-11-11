package db

import (
	"context"
	"database/sql"
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
