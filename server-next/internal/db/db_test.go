package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// A fresh database takes the whole schema, passes SQLite's own checks, and opens again.
func TestOpenCreatesAndReopens(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	for range 2 {
		d, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		var ok string
		if err := d.read.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&ok); err != nil || ok != "ok" {
			t.Fatalf("integrity_check = %q, %v", ok, err)
		}
		rows, err := d.read.QueryContext(ctx, `PRAGMA foreign_key_check`)
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() {
			t.Fatal("foreign_key_check reported a violation")
		}
		rows.Close()
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// Every foreign key column has an index that starts with it, so deleting a parent row never
// scans a child table.
func TestEveryForeignKeyIsIndexed(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rows, err := d.read.QueryContext(ctx, `
		SELECT m.name, fk."from"
		FROM sqlite_master m, pragma_foreign_key_list(m.name) fk
		WHERE m.type = 'table' AND NOT EXISTS (
			SELECT 1 FROM pragma_index_list(m.name) il, pragma_index_info(il.name) ii
			WHERE ii.seqno = 0 AND ii.name = fk."from")
		AND NOT EXISTS (
			SELECT 1 FROM pragma_table_info(m.name) ti WHERE ti.name = fk."from" AND ti.pk = 1)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		t.Errorf("%s.%s is a foreign key with no index starting with it", table, column)
	}
}

// Writes commit, a failed write leaves nothing behind, and background work still gets a turn
// while user writes keep arriving.
func TestWriteCommitsRollsBackAndSharesTheWriter(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	set := func(p Priority, key string, fail bool) error {
		return d.write(ctx, p, func(tx *sql.Tx) error {
			if _, err := tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, '1', 0)`, key); err != nil {
				return err
			}
			if fail {
				return errors.New("stop")
			}
			return nil
		})
	}
	if err := set(User, "kept", false); err != nil {
		t.Fatal(err)
	}
	if err := set(User, "dropped", true); err == nil {
		t.Fatal("a failed write reported success")
	}
	var users sync.WaitGroup
	stop := make(chan struct{})
	for i := range 4 {
		users.Go(func() {
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
					set(User, fmt.Sprintf("user-%d-%d", i, n), false)
				}
			}
		})
	}
	if err := set(Background, "background", false); err != nil {
		t.Fatal(err)
	}
	close(stop)
	users.Wait()
	var kept, dropped int
	d.read.QueryRow(`SELECT count(*) FILTER (WHERE key = 'kept'), count(*) FILTER (WHERE key = 'dropped') FROM settings`).Scan(&kept, &dropped)
	if kept != 1 || dropped != 0 {
		t.Fatalf("kept %d, dropped %d; want 1 and 0", kept, dropped)
	}
}
