package db

import (
	"context"
	"database/sql"
)

// loadPersonCredits gives each item of a list of one person's titles that person's credits on
// it. person is 0 for any other list.
func loadPersonCredits(ctx context.Context, tx *sql.Tx, person int64, items []Item) error {
	if person == 0 || len(items) == 0 {
		return nil
	}
	at := make(map[int64]int, len(items))
	ids := make([]int64, len(items))
	for n, it := range items {
		at[it.ID], ids[n] = n, it.ID
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.item_id, p.public_id, p.name, c.role, ifnull(c.character, '') FROM credits c
		JOIN people p ON p.id = c.person_id WHERE c.person_id = ? AND c.item_id IN `+intList(ids)+` ORDER BY c.item_id, c.position`, person)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item int64
		var c Credit
		if err := rows.Scan(&item, &c.PersonID, &c.Name, &c.Role, &c.Character); err != nil {
			return err
		}
		items[at[item]].Credits = append(items[at[item]].Credits, c)
	}
	return rows.Err()
}
