package db

// SetRareTag lets a test send tag-filtered lists down the path for common tags or the path
// for rare ones, whatever the size of its library.
func SetRareTag(uses int) { rareTag = uses }

// SetChangesKeepRows lets a test prune the changes table without writing 200,000 rows first.
func SetChangesKeepRows(rows int) { changesKeepRows = rows }

// WidthsNotKept counts the items whose stored widest version is not what their versions
// say, and the items that have one at all.
func WidthsNotKept(d *DB) (wrong, set int, err error) {
	err = d.read.QueryRow(`SELECT count(*) FILTER (WHERE best_width IS NOT (SELECT max(m.width) FROM media m WHERE m.item_id = items.id)),
		count(best_width) FROM items`).Scan(&wrong, &set)
	return wrong, set, err
}
