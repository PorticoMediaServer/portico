package db

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
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
		if err := b.ratingChanged(id); err != nil {
			return err
		}
		return settleMarksNow(b)
	})
}

// ForgetEndedSignIns drops the in-memory note of ended sign-ins at once, as the server does by
// itself once no access token could still carry them, so a test can see what holds after that.
func ForgetEndedSignIns(d *DB) {
	d.marks.mu.Lock()
	clear(d.marks.marks)
	d.marks.mu.Unlock()
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

// PutLabel gives a title a label the way the label editor will have to: the tag row, then
// labelsChanged for the title, in one transaction. off takes it away again.
func PutLabel(d *DB, publicID, name string, off bool) error {
	return d.write(context.Background(), User, func(tx *sql.Tx) error {
		im := &importer{batchTx: newBatchTx(tx), tags: map[TagName]int64{}}
		defer im.close()
		var id int64
		if err := im.queryRow(`SELECT id FROM items WHERE public_id = ?`, publicID).Scan(&id); err != nil {
			return err
		}
		tag, err := im.tagID(TagName{Kind: TagLabel, Name: name})
		if err != nil {
			return err
		}
		if off {
			if _, err := im.exec(`DELETE FROM item_tags WHERE item_id = ? AND tag_id = ?`, id, tag); err != nil {
				return err
			}
		} else if err := im.tagItem(id, tag); err != nil {
			return err
		}
		return im.labelsChanged(id)
	})
}

// Raw runs a read and returns the first column of each row as text.
func Raw(d *DB, query string, args ...any) ([]string, error) {
	rows, err := d.read.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s sql.NullString
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s.String)
	}
	return out, rows.Err()
}

// SplitSecondVersions splits one version off every title that has more than one and sits
// under a parent, and returns how many it split.
func SplitSecondVersions(d *DB, onlyCovered bool) (int, error) {
	q := `SELECT m.public_id FROM media m JOIN items i ON i.id = m.item_id WHERE i.parent_id IS NOT NULL AND m.position > 0`
	if onlyCovered {
		q += ` AND i.labels IS NOT NULL`
	}
	versions, err := Raw(d, q)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, version := range versions {
		if _, err := d.SplitVersion(context.Background(), version); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// HolderFlags works out from every item's rating and stored labels which holders some
// playable title under them differs from, and compares that with each holder's stored flag.
// missing names each holder that should be flagged and is not: one a profile could be left
// with nothing under and still meet. extra counts flags nothing causes any more, which
// ordinary writes are allowed to leave. stored is every item's flag.
func HolderFlags(d *DB) (missing []string, extra int, stored map[int64]bool, err error) {
	type row struct {
		kind           ItemKind
		parent, root   int64
		rating, labels sql.NullString
		flag           bool
	}
	items := map[int64]row{}
	rows, err := d.read.Query(`SELECT id, kind, ifnull(parent_id, 0), ifnull(root_id, 0), rating_age, labels, may_empty FROM items`)
	if err != nil {
		return nil, 0, nil, err
	}
	for rows.Next() {
		var id int64
		var r row
		if err := rows.Scan(&id, &r.kind, &r.parent, &r.root, &r.rating, &r.labels, &r.flag); err != nil {
			rows.Close()
			return nil, 0, nil, err
		}
		items[id] = r
	}
	if err := rows.Close(); err != nil {
		return nil, 0, nil, err
	}
	want := map[int64]bool{}
	for _, leaf := range items {
		if !leaf.kind.Playable() {
			continue
		}
		for _, id := range []int64{leaf.parent, leaf.root} {
			if holder, ok := items[id]; ok && !holder.kind.Playable() && (holder.rating != leaf.rating || holder.labels != leaf.labels) {
				want[id] = true
			}
		}
	}
	stored = map[int64]bool{}
	for id, r := range items {
		stored[id] = r.flag
		switch {
		case want[id] && !r.flag:
			missing = append(missing, fmt.Sprintf("%s %d is not flagged, though a title under it is rated or covered differently", r.kind, id))
		case r.flag && !want[id]:
			extra++
		}
	}
	return missing, extra, stored, nil
}

// MatchTitle gives a title the provider match key, as the metadata service does: found by the
// server, or chosen by a person, which merges it into another title of the library that has
// that match already.
func MatchTitle(d *DB, publicID, key string, chosen bool) error {
	var id int64
	if err := d.read.QueryRow(`SELECT id FROM items WHERE public_id = ?`, publicID).Scan(&id); err != nil {
		return err
	}
	if !chosen {
		return d.ApplyDetails(context.Background(), Background, id, ItemDetails{Match: MatchFound, ProviderKey: key})
	}
	_, err := d.ChangeMatch(context.Background(), User, id, ItemDetails{Match: MatchChosen, ProviderKey: key})
	return err
}
