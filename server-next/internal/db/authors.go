package db

import (
	"context"
	"database/sql"
)

// loadAuthors gives each book and audiobook of a set of cards its first credited author, read
// by the credits' primary key for those titles alone.
func loadAuthors(ctx context.Context, tx *sql.Tx, items []Item) error {
	at := map[int64][]int{}
	var ids []int64
	for n, it := range items {
		if it.Kind == KindBook || it.Kind == KindAudiobook {
			if at[it.ID] == nil {
				ids = append(ids, it.ID)
			}
			at[it.ID] = append(at[it.ID], n)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.item_id, p.name FROM credits c JOIN people p ON p.id = c.person_id
		WHERE c.item_id IN `+intList(ids)+` AND c.role = ? ORDER BY c.item_id, c.position`, RoleAuthor)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		for _, n := range at[id] {
			if items[n].Author == "" {
				items[n].Author = name
			}
		}
	}
	return rows.Err()
}
