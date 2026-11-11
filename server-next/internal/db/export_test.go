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

// ShareListForTest shares a list with a profile, to view or to edit, until sharing has its own route.
func (d *DB) ShareListForTest(ctx context.Context, list string, profile int64, canEdit bool) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO list_shares (list_id, profile_id, can_edit, created_at) SELECT id, ?, ?, 0 FROM lists WHERE public_id = ?`,
			profile, canEdit, list)
		return err
	})
}

// ForgetEndedSignIns drops the in-memory note of ended sign-ins at once, as the server does by
// itself once no access token could still carry them, so a test can see what holds after that.
func ForgetEndedSignIns(d *DB) {
	d.marks.mu.Lock()
	clear(d.marks.marks)
	d.marks.mu.Unlock()
}
