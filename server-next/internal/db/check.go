package db

import (
	"context"
	"fmt"
)

// Check runs SQLite's own integrity check and foreign-key check and reports the first problem.
// On a large database it reads everything and takes minutes.
func (d *DB) Check(ctx context.Context) error {
	var result string
	if err := d.read.QueryRowContext(ctx, `PRAGMA integrity_check(1)`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity check: %s", result)
	}
	rows, err := d.read.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowid, index any
		if err := rows.Scan(&table, &rowid, &parent, &index); err != nil {
			return err
		}
		return fmt.Errorf("foreign key check: a row of %s refers to a missing row of %s", table, parent)
	}
	return rows.Err()
}
