package db

// WidthsNotKept counts the items whose stored widest version is not what their versions
// say, and the items that have one at all.
func WidthsNotKept(d *DB) (wrong, set int, err error) {
	err = d.read.QueryRow(`SELECT count(*) FILTER (WHERE best_width IS NOT (SELECT max(m.width) FROM media m WHERE m.item_id = items.id)),
		count(best_width) FROM items`).Scan(&wrong, &set)
	return wrong, set, err
}
