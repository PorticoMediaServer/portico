package db

import (
	"context"
	"strings"
)

// PlanForTest is SQLite's plan for a read, one line per step, for a test that holds a read to an index.
func PlanForTest(d *DB, query string, args ...any) (string, error) {
	rows, err := d.read.QueryContext(context.Background(), `EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			return "", err
		}
		lines = append(lines, detail)
	}
	return strings.Join(lines, "; "), rows.Err()
}
