package db

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
)

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

// CoveringLabels compares the labels stored on every item with the labels of the item, of
// what it sits in and of what that sits in, worked out here from the tag rows. It returns a
// line for each item that is wrong, and what every item has stored.
func CoveringLabels(d *DB) (wrong []string, stored map[int64]string, err error) {
	stored = map[int64]string{}
	own := map[int64][]int64{}
	rows, err := d.read.Query(`SELECT it.item_id, it.tag_id FROM item_tags it JOIN tags t ON t.id = it.tag_id WHERE t.kind = 'label'`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var item, tag int64
		if err := rows.Scan(&item, &tag); err != nil {
			rows.Close()
			return nil, nil, err
		}
		own[item] = append(own[item], tag)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	rows, err = d.read.Query(`SELECT id, ifnull(parent_id, 0), ifnull(root_id, 0), ifnull(labels, '') FROM items`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, root int64
		var labels string
		if err := rows.Scan(&id, &parent, &root, &labels); err != nil {
			return nil, nil, err
		}
		stored[id] = labels
		tags := slices.Concat(own[id], own[parent], own[root])
		slices.Sort(tags)
		want := ""
		for _, tag := range slices.Compact(tags) {
			want += fmt.Sprint(",", tag)
		}
		if want != "" {
			want += ","
		}
		if labels != want {
			wrong = append(wrong, fmt.Sprintf("item %d has %q stored, and its own and its ancestors' labels are %q", id, labels, want))
		}
	}
	return wrong, stored, rows.Err()
}
