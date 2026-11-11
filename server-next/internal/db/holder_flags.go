package db

import (
	"context"
	"database/sql"
)

// Which holders a profile could be left with nothing under.
//
// A show, season, artist or album is hidden from a profile that may see it but none of the
// playable titles under it (see access.filled). Every title under a holder is in its library,
// and one with no rating of its own carries its holder's, so a title is hidden while its
// holder is not only when it is rated differently or covered by a label its holder is not.
// items.may_empty marks the holders where some title is, and only those are ever checked:
// for every other holder the answer is the holder's own.
//
// flagHolders is the one place the column is written. labelsChanged and ratingChanged call it,
// and so does an import for each title it describes, in the same transaction as the change.
// It only ever sets the flag. A holder left flagged after what caused it has gone costs one
// check; a holder left unflagged would show a profile an empty holder, so nothing clears it
// but RebuildHolderFlags, which works every row out again from nothing.

// differsBelow is the condition that some playable title under the items row in scope is
// rated or covered differently from it. Titles nest at most three deep, so a holder is the
// root or the parent of its titles.
const differsBelow = `(EXISTS (SELECT 1 FROM items l WHERE l.root_id = items.id AND l.kind IN ` + playableKindsSQL + `
	AND (l.rating_age IS NOT items.rating_age OR l.labels IS NOT items.labels))
	OR EXISTS (SELECT 1 FROM items l WHERE l.parent_id = items.id AND l.kind IN ` + playableKindsSQL + `
	AND (l.rating_age IS NOT items.rating_age OR l.labels IS NOT items.labels)))`

// flagHolders flags the holders these items' changes may have made able to be left empty:
// what holds each item, checked against the titles at or under the item only (one row for an
// episode, so an import of a long show stays cheap), and each item that is a holder and the
// holders under it, checked against everything under each. Call it after the item's rating,
// labels or place have been written.
func (b *batchTx) flagHolders(items ...int64) error {
	for _, id := range items {
		if id == 0 {
			continue
		}
		if _, err := b.exec(`UPDATE items SET may_empty = 1 WHERE may_empty = 0 AND kind NOT IN `+playableKindsSQL+`
			AND id IN (SELECT parent_id FROM items WHERE id = ?1 UNION SELECT root_id FROM items WHERE id = ?1)
			AND EXISTS (SELECT 1 FROM items l WHERE (l.id = ?1 OR l.parent_id = ?1 OR l.root_id = ?1) AND l.kind IN `+playableKindsSQL+`
				AND (l.rating_age IS NOT items.rating_age OR l.labels IS NOT items.labels))`, id); err != nil {
			return err
		}
		if _, err := b.exec(`UPDATE items SET may_empty = 1 WHERE may_empty = 0 AND kind NOT IN `+playableKindsSQL+`
			AND (id = ?1 OR parent_id = ?1 OR root_id = ?1) AND `+differsBelow, id); err != nil {
			return err
		}
	}
	return nil
}

// RebuildHolderFlags works out every holder's flag again from nothing, a few thousand items to
// a transaction, and clears the ones nothing causes any more. Nothing needs it in ordinary
// running; it is there to prove the writes keep the flag, and to fill it after an upgrade.
func (d *DB) RebuildHolderFlags(ctx context.Context) error {
	const page = 2000
	flag := `(kind NOT IN ` + playableKindsSQL + ` AND ` + differsBelow + `)`
	for from := int64(0); ; from += page {
		var more bool
		err := d.write(ctx, Background, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET may_empty = `+flag+` WHERE id > ? AND id <= ? AND may_empty IS NOT `+flag,
				from, from+page); err != nil {
				return err
			}
			return tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM items WHERE id > ?)`, from+page).Scan(&more)
		})
		if err != nil || !more {
			return err
		}
	}
}
