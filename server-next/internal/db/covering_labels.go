package db

import (
	"context"
	"database/sql"
	"strconv"
)

// The labels that cover an item.
//
// A profile can have labels blocked, and a blocked label hides the title that carries it and
// everything under that title. So whether a profile may see an item depends on the labels of
// the item, of what it sits in (its season or album) and of what that sits in (its show or
// artist). items.labels holds exactly those, as the tags' ids in ascending order between
// commas (",4,11,"), or NULL when there are none, which is nearly every item. With it the
// visibility condition is one look at the row in hand, a list of what a label hides is read
// from a small index of the items that have any, and no read looks a label up again.
//
// labelsChanged is the one place the column is written. Whatever puts a label on a title or
// takes one off, puts an item under another title, or makes an item, calls it in the same
// transaction. RebuildCoveringLabels works every row out again from nothing.

// coveringLabels is the value of items.labels for the items row in scope, from its tags.
const coveringLabels = `(SELECT ',' || group_concat(tag_id, ',') || ',' FROM (SELECT DISTINCT it.tag_id FROM item_tags it
	JOIN tags t ON t.id = it.tag_id AND t.kind = 'label'
	WHERE it.item_id IN (items.id, items.parent_id, items.root_id) ORDER BY it.tag_id))`

// labelsChanged writes the covering labels of each of these items and of everything under
// them. Each statement finds its rows through a key or an index and leaves alone the rows
// that are already right.
func (b *batchTx) labelsChanged(items ...int64) error {
	for _, id := range items {
		if id == 0 {
			continue
		}
		for _, which := range []string{"id", "parent_id", "root_id"} {
			if _, err := b.exec(`UPDATE items SET labels = `+coveringLabels+` WHERE `+which+` = ? AND labels IS NOT `+coveringLabels, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// labelsExclude is the condition that the covering labels in the SQL expression labels include none
// of these tags.
func labelsExclude(labels string, tags []int64) string {
	out := "(" + labels + " IS NULL OR ("
	for n, tag := range tags {
		if n > 0 {
			out += " AND "
		}
		out += "instr(" + labels + ", '," + strconv.FormatInt(tag, 10) + ",') = 0"
	}
	return out + "))"
}

// RebuildCoveringLabels works out every item's covering labels again from its tags and those
// of what it sits under, a few thousand items to a transaction. Nothing needs it in ordinary
// running: every change writes its own. It is there to prove that, and to put right a
// database whose column was ever written wrongly.
func (d *DB) RebuildCoveringLabels(ctx context.Context) error {
	const page = 2000
	for from := int64(0); ; from += page {
		var more bool
		err := d.write(ctx, Background, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET labels = `+coveringLabels+` WHERE id > ? AND id <= ?
				AND labels IS NOT `+coveringLabels, from, from+page); err != nil {
				return err
			}
			return tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM items WHERE id > ?)`, from+page).Scan(&more)
		})
		if err != nil || !more {
			return err
		}
	}
}
