package db

import (
	"context"
	"database/sql"
	"time"
)

// The changes table is pruned to these bounds. Its two readers are the search indexer, which
// follows within seconds, and open apps. A reader that falls further behind than this rereads
// everything, which is what an app does when it is opened after a week anyway.
var (
	changesKeepRows = 200_000
	changesKeepAge  = 7 * 24 * time.Hour
)

// Change is one row of the numbered list of what changed. It says which thing to read again
// and never carries the data. LibraryID and ProfileID are 0 when they do not apply; they let
// the feed decide who may hear about the row without looking the thing up.
type Change struct {
	ID        int64
	At        int64
	Topic     ChangeTopic
	Op        ChangeOp
	RefID     int64
	LibraryID int64
	ProfileID int64
}

// appendChange is the one insert into changes. It runs inside the transaction that made the
// change, so a change that committed is always announced.
func appendChange(tx *sql.Tx, c Change) error {
	if err := checkCode("changes.topic", changeTopicNames, c.Topic); err != nil {
		return err
	}
	if err := checkCode("changes.op", changeOpNames, c.Op); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO changes (at, topic, op, ref_id, library_id, profile_id) VALUES (?, ?, ?, ?, nullif(?, 0), nullif(?, 0))`,
		c.At, c.Topic, c.Op, c.RefID, c.LibraryID, c.ProfileID)
	return err
}

// ChangesHead is the cursor of the newest change: where a new reader starts.
func (d *DB) ChangesHead(ctx context.Context) (int64, error) {
	var head int64
	err := d.read.QueryRowContext(ctx, `SELECT ifnull((SELECT seq FROM sqlite_sequence WHERE name = 'changes'), 0)`).Scan(&head)
	return head, err
}

// Changes returns up to limit changes after the cursor, oldest first. reread is true when rows
// after the cursor have already been pruned: the reader has missed something it can no longer
// be told about and must read everything again, then carry on from head.
func (d *DB) Changes(ctx context.Context, after int64, limit int) (changes []Change, reread bool, head int64, err error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, 0, err
	}
	defer tx.Rollback()
	var oldest int64
	if err = tx.QueryRowContext(ctx, `SELECT ifnull((SELECT seq FROM sqlite_sequence WHERE name = 'changes'), 0),
		ifnull((SELECT min(id) FROM changes), 0)`).Scan(&head, &oldest); err != nil {
		return nil, false, 0, err
	}
	// Ids are never reused, so with nothing kept the next id to expect is head+1.
	if oldest == 0 {
		oldest = head + 1
	}
	if after < oldest-1 || after > head {
		return nil, true, head, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, at, topic, op, ref_id, ifnull(library_id, 0), ifnull(profile_id, 0)
		FROM changes WHERE id > ? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, false, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Change
		if err = rows.Scan(&c.ID, &c.At, &c.Topic, &c.Op, &c.RefID, &c.LibraryID, &c.ProfileID); err != nil {
			return nil, false, 0, err
		}
		changes = append(changes, c)
	}
	return changes, false, head, rows.Err()
}

// PruneChanges deletes changes beyond the kept row count or older than the kept age, a few
// thousand at a time so a person's write never waits long behind it. The server calls it
// every few minutes.
func (d *DB) PruneChanges(ctx context.Context) (deleted int64, err error) {
	const batch = 2000
	for {
		var n int64
		err = d.write(ctx, Background, func(tx *sql.Tx) error {
			res, err := tx.Exec(`DELETE FROM changes WHERE id IN (SELECT id FROM changes ORDER BY id LIMIT ?)
				AND (id <= (SELECT max(id) FROM changes) - ? OR at < ?)`,
				batch, changesKeepRows, time.Now().Add(-changesKeepAge).UnixMilli())
			if err != nil {
				return err
			}
			n, _ = res.RowsAffected()
			return nil
		})
		deleted += n
		if err != nil || n < batch {
			return deleted, err
		}
	}
}
