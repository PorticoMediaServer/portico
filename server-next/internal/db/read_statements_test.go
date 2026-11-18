package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

// A read connection runs a statement it keeps again while the first run's rows are still
// being read, and reads those rows to the end after the statement has left its statements.
func TestReadPoolKeptStatements(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true}) // one connection throughout
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	const upTo = `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?) SELECT i FROM n`
	read := func(rows *sql.Rows) []int {
		t.Helper()
		var got []int
		for rows.Next() {
			var i int
			if err := rows.Scan(&i); err != nil {
				t.Fatal(err)
			}
			got = append(got, i)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return got
	}
	query := func(text string, args ...any) *sql.Rows {
		t.Helper()
		rows, err := tx.QueryContext(ctx, text, args...)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	first := query(upTo, 3)
	if !first.Next() {
		t.Fatal("no first row")
	}
	if got := read(query(upTo, 2)); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("the same text while its first rows are open: %v", got)
	}
	for n := range keptStatements + 1 {
		var got int
		if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT %d", n)).Scan(&got); err != nil || got != n {
			t.Fatalf("SELECT %d: %d, %v", n, got, err)
		}
	}
	if got := read(first); !slices.Equal(got, []int{2, 3}) {
		t.Fatalf("the first rows, after their statement left: %v", got)
	}
	if got := read(query(upTo, 4)); !slices.Equal(got, []int{1, 2, 3, 4}) {
		t.Fatalf("the text again: %v", got)
	}
}
