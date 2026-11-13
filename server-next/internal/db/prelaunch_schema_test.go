package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestAnOldTestFolderIsRefusedBeforeServing(t *testing.T) {
	ctx := context.Background()
	for _, change := range []string{"", `DROP TABLE track_embeddings`, `ALTER TABLE items RENAME COLUMN tagline TO older_name`} {
		path := filepath.Join(t.TempDir(), "main.db")
		d, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		d.Close()
		conn, err := open(path, "rw", 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if change != "" {
			if _, err := conn.Exec(change); err != nil {
				t.Fatal(err)
			}
		}
		conn.Close()
		d, err = Open(ctx, path)
		if d != nil {
			d.Close()
		}
		if change == "" && err != nil || change != "" && !errors.Is(err, ErrOldTestFolder) {
			t.Fatalf("after %q: %v", change, err)
		}
	}
}

func TestAnOlderFolderCanUseItsRegisteredUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	// The next schema genuinely has a table this version-1 folder does not hold.
	const added = `CREATE TABLE trial_startup (id INTEGER PRIMARY KEY)`
	before := schemaSQL
	schemaSQL += ";\n" + added
	t.Cleanup(func() { schemaSQL = before })
	useUpgrades(t, []upgrade{{what: "new startup table", sql: added}})
	d, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var version int
	var exists bool
	err = d.read.QueryRow(`SELECT max(version), EXISTS(SELECT 1 FROM sqlite_schema WHERE name = 'trial_startup') FROM schema_migrations`).Scan(&version, &exists)
	if err != nil || version != schemaVersion() || !exists {
		t.Fatalf("upgrade: version %d, new table %v, error %v", version, exists, err)
	}
}

func TestACurrentFolderFinishesItsRecordedBackgroundUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	const table = `CREATE TABLE trial_startup (id INTEGER PRIMARY KEY)`
	const index = `CREATE INDEX trial_startup_id ON trial_startup (id)`
	before := schemaSQL
	schemaSQL += ";\n" + table + ";\n" + index
	t.Cleanup(func() { schemaSQL = before })
	stopped := errors.New("test stopped the background step")
	stop := true
	useUpgrades(t, []upgrade{{what: "startup table and index", sql: table, beforeServing: true,
		background: func(tx *sql.Tx, after int64) (int64, bool, error) {
			if stop {
				return after, false, stopped
			}
			_, err := tx.Exec(index)
			return after, true, err
		}}})
	if _, err := Open(ctx, path); !errors.Is(err, stopped) {
		t.Fatalf("did not leave the registered background step unfinished: %v", err)
	}
	stop = false
	d, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var finished bool
	err = d.read.QueryRow(`SELECT background_after IS NULL AND EXISTS(SELECT 1 FROM sqlite_schema WHERE name = 'trial_startup_id')
		FROM schema_migrations WHERE version = ?`, schemaVersion()).Scan(&finished)
	if err != nil || !finished {
		t.Fatalf("background step finished: %v, error %v", finished, err)
	}
}
