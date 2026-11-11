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

// ForgetEndedSignIns drops the in-memory note of ended sign-ins at once, as the server does by
// itself once no access token could still carry them, so a test can see what holds after that.
func ForgetEndedSignIns(d *DB) {
	d.marks.mu.Lock()
	clear(d.marks.marks)
	d.marks.mu.Unlock()
}

// SetWALLimits lets a test reach the WAL policy's sizes with a small database, and its
// longest hold without waiting seconds. It returns a function that puts the real ones back.
func SetWALLimits(drain, insist int64, hold time.Duration) (restore func()) {
	oldDrain, oldInsist, oldHold := walDrainBytes, walInsistBytes, walHoldMax
	walDrainBytes, walInsistBytes, walHoldMax = drain, insist, hold
	return func() { walDrainBytes, walInsistBytes, walHoldMax = oldDrain, oldInsist, oldHold }

}

// HoldWriter runs a background write that does nothing until release is closed, so a test
// can have a person's write wait behind background work.
func HoldWriter(d *DB, release <-chan struct{}) error {
	return d.write(context.Background(), Background, func(*sql.Tx) error {
		<-release
		return nil
	})
}

// SetBusyFor lets a test see import batches return to normal without waiting seconds.
func SetBusyFor(busy time.Duration) (restore func()) {
	old := busyFor
	busyFor = busy
	return func() { busyFor = old }
}
