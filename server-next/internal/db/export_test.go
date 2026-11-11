package db

import "context"

// SetRareTag lets a test send tag-filtered lists down the path for common tags or the path
// for rare ones, whatever the size of its library.
func SetRareTag(uses int) { rareTag = uses }

// SetChangesKeepRows lets a test prune the changes table without writing 200,000 rows first.
func SetChangesKeepRows(rows int) { changesKeepRows = rows }

// HistoryChildren says, newest entry first, how many seasons and episodes each History entry
// of a title keeps.
func HistoryChildren(d *DB, publicID string) ([]int, error) {
	rows, err := d.read.Query(`SELECT ifnull(json_array_length(h.before, '$.children'), 0) FROM metadata_history h
		JOIN items i ON i.id = h.item_id WHERE i.public_id = ? ORDER BY h.id DESC`, publicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ChangeMatchOf is ChangeMatch for a title named by its public id.
func ChangeMatchOf(d *DB, publicID string, details ItemDetails) error {
	var id int64
	if err := d.read.QueryRow(`SELECT id FROM items WHERE public_id = ?`, publicID).Scan(&id); err != nil {
		return err
	}
	_, err := d.ChangeMatch(context.Background(), User, id, details)
	return err
}
