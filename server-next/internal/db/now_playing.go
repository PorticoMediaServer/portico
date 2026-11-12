package db

import (
	"context"
	"database/sql"
)

// IndexNumbers answers the index number (an episode's number in its season, a song's on its
// disc) of each of these items that has one, for Now playing.
func (d *DB) IndexNumbers(ctx context.Context, itemIDs []int64) (map[int64]int, error) {
	out := map[int64]int{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	rows, err := d.read.QueryContext(ctx, `SELECT id, index_number FROM items WHERE id IN `+intList(itemIDs)+` AND index_number IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n sql.NullInt64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = int(n.Int64)
	}
	return out, rows.Err()
}
