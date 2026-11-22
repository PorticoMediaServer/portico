package db

import (
	"context"
	"database/sql"
)

// loadListExtras adds what some lists carry beyond the card: on a list of one person's titles,
// that person's credits on each; on a list of one holder's children, each episode's summary.
func loadListExtras(ctx context.Context, tx *sql.Tx, person int64, children bool, items []Item) error {
	if err := loadPersonCredits(ctx, tx, person, items); err != nil || !children {
		return err
	}
	return loadEpisodeSummaries(ctx, tx, items)
}

// loadEpisodeSummaries gives each episode of a page its summary. Only a list of one holder's
// children asks for it (a season's episodes, at most a page of them), read by primary key.
func loadEpisodeSummaries(ctx context.Context, tx *sql.Tx, items []Item) error {
	at := make(map[int64]int, len(items))
	var ids []int64
	for n, it := range items {
		if it.Kind == KindEpisode {
			at[it.ID] = n
			ids = append(ids, it.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, summary FROM items WHERE id IN `+intList(ids)+` AND summary IS NOT NULL AND summary != ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var summary string
		if err := rows.Scan(&id, &summary); err != nil {
			return err
		}
		items[at[id]].Summary = summary
	}
	return rows.Err()
}
