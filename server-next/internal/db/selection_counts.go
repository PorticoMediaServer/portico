package db

import (
	"context"
	"database/sql"
)

// KindCount is how many titles of one kind a list holds for the viewer, and how many of them
// are available (none of their files missing), which is what a title's page offers Play by.
type KindCount struct {
	Kind      ItemKind
	Count     int
	Available int
}

// SelectionCounts counts the whole list q describes (its sort, cursor and limit are ignored)
// by kind, under the same condition that decides which titles its pages hold: the viewer's
// libraries, rating limit, blocked labels and every filter. It is what "select all" stands
// for. Like Facets, it reads every row of the list, so a client asks for it when someone
// selects, not with every page.
func (d *DB) SelectionCounts(ctx context.Context, v Viewer, q ItemQuery) ([]KindCount, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	w := newQuery(v)
	if found, err := q.filters(ctx, tx, w, acc); err != nil || !found {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.kind, count(*), sum(i.available) FROM `+w.countFrom+` WHERE `+w.countWhere(true)+
		` GROUP BY i.kind ORDER BY i.kind`, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KindCount
	for rows.Next() {
		var c KindCount
		if err := rows.Scan(&c.Kind, &c.Count, &c.Available); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
