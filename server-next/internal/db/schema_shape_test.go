package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// databaseFrom makes a database in memory from schema text, at a version.
func databaseFrom(t *testing.T, schema string, version int, more ...string) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetMaxOpenConns(1)
	if version > 0 {
		more = append([]string{fmt.Sprintf(`INSERT INTO schema_migrations (version, applied_at) VALUES (%d, 0)`, version)}, more...)
	}
	for _, statement := range append([]string{schema}, more...) {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return conn
}

// The check a backup must pass accepts a new database and refuses one that holds more, less
// or other than the schema: what was added could run or hide something once the copy is in
// use, and what is missing would fail the first request that needs it.
func TestTheSchemaCheckAcceptsANewDatabaseAndRefusesAChangedOne(t *testing.T) {
	ctx := context.Background()
	if err := CheckSchema(ctx, databaseFrom(t, schemaSQL, schemaVersion())); err != nil {
		t.Fatalf("a new database: %v", err)
	}
	for change, want := range map[string]string{
		`CREATE TRIGGER quiet AFTER INSERT ON users BEGIN DELETE FROM audit_log; END`:                             "holds trigger quiet",
		`CREATE VIEW everyone AS SELECT * FROM users`:                                                             "holds view everyone",
		`CREATE TABLE extra (id INTEGER PRIMARY KEY)`:                                                             "holds table extra",
		`CREATE INDEX extra ON items (tagline)`:                                                                   "holds index extra",
		`DROP INDEX items_by_title`:                                                                               "lacks index items_by_title",
		`DROP TABLE daily_totals`:                                                                                 "lacks table daily_totals",
		`ALTER TABLE items ADD COLUMN trial_extra TEXT`:                                                           "its table items is not as",
		`ALTER TABLE items RENAME COLUMN tagline TO motto`:                                                        "its table items is not as",
		`INSERT INTO schema_migrations (version, applied_at) VALUES (` + strconv.Itoa(schemaVersion()+1) + `, 0)`: "newer version",
		`DELETE FROM schema_migrations`:                                                                           "not a Portico database",
	} {
		err := CheckSchema(ctx, databaseFrom(t, schemaSQL, schemaVersion(), change))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("after %q the check says %v, want it to say %q", change, err, want)
		}
		if err != nil && strings.Contains(err.Error(), "fresh data folder") {
			t.Errorf("backup/restore schema check gave startup advice: %v", err)
		}
	}
}

// A backup made before an upgrade can still be restored after it: the next start upgrades
// it. It is still refused if it holds something that could run.
func TestTheSchemaCheckAcceptsAnOlderDatabase(t *testing.T) {
	ctx := context.Background()
	useUpgrades(t, trialUpgrades(10))
	if err := CheckSchema(ctx, databaseFrom(t, schemaSQL, firstVersion)); err != nil {
		t.Fatalf("a database from the first release: %v", err)
	}
	err := CheckSchema(ctx, databaseFrom(t, schemaSQL, firstVersion, `CREATE TRIGGER quiet AFTER INSERT ON users BEGIN DELETE FROM audit_log; END`))
	if err == nil || !strings.Contains(err.Error(), "holds trigger quiet") {
		t.Fatalf("a database from the first release with a trigger: %v", err)
	}
}

// A table that gained a column in an upgrade is the same table as one made new with that
// column last, though SQLite keeps different text for the two: the comma, a comment on what
// was the last line and the step's own spelling all end up different.
func TestATableWithAnAddedColumnHasTheShapeOfANewOne(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct{ old, fresh, add string }{
		"plain": {"CREATE TABLE things (\n  id INTEGER NOT NULL PRIMARY KEY,\n  name TEXT NOT NULL\n)",
			"CREATE TABLE things (\n  id INTEGER NOT NULL PRIMARY KEY,\n  name TEXT NOT NULL,\n  size INTEGER\n)", "size INTEGER"},
		"a comment on the last line": {"CREATE TABLE things (\n  id INTEGER NOT NULL PRIMARY KEY,\n  name TEXT NOT NULL -- what people call it\n)",
			"CREATE TABLE things (\n  id INTEGER NOT NULL PRIMARY KEY,\n  name TEXT NOT NULL, -- what people call it\n  size INTEGER\n)", "size INTEGER"},
		"a key after the columns": {"CREATE TABLE things (\n  id INTEGER NOT NULL,\n  name TEXT NOT NULL,\n  PRIMARY KEY (id)\n) STRICT",
			"CREATE TABLE things (\n  id INTEGER NOT NULL,\n  name TEXT NOT NULL,\n  size INTEGER,\n  PRIMARY KEY (id)\n) STRICT", "size INTEGER"},
		"another spelling in the step": {"CREATE TABLE things (\n  id INTEGER NOT NULL PRIMARY KEY,\n  name TEXT NOT NULL\n)",
			"CREATE TABLE things (\n  id INTEGER NOT NULL PRIMARY KEY,\n  name TEXT NOT NULL,\n  size INTEGER NOT NULL DEFAULT 0\n)", "size integer not null default 0"},
	} {
		upgraded, err := schemaShape(ctx, databaseFrom(t, c.old, 0, "ALTER TABLE things ADD COLUMN "+c.add))
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := schemaShape(ctx, databaseFrom(t, c.fresh, 0))
		if err != nil {
			t.Fatal(err)
		}
		if upgraded["table things"] != fresh["table things"] || fresh["table things"] == "" {
			t.Errorf("%s:\n  upgraded: %s\n  new:      %s", name, upgraded["table things"], fresh["table things"])
		}
	}
	// What must still tell two tables apart.
	base, _ := schemaShape(ctx, databaseFrom(t, `CREATE TABLE things (id INTEGER NOT NULL PRIMARY KEY, name TEXT NOT NULL, other INTEGER REFERENCES things(id))`, 0))
	for _, other := range []string{
		`CREATE TABLE things (id INTEGER NOT NULL PRIMARY KEY, name TEXT, other INTEGER REFERENCES things(id))`,
		`CREATE TABLE things (id INTEGER NOT NULL PRIMARY KEY, name BLOB NOT NULL, other INTEGER REFERENCES things(id))`,
		`CREATE TABLE things (id INTEGER NOT NULL PRIMARY KEY, name TEXT NOT NULL DEFAULT '', other INTEGER REFERENCES things(id))`,
		`CREATE TABLE things (id INTEGER NOT NULL PRIMARY KEY, name TEXT NOT NULL UNIQUE, other INTEGER REFERENCES things(id))`,
		`CREATE TABLE things (id INTEGER NOT NULL PRIMARY KEY, name TEXT NOT NULL, other INTEGER)`,
		`CREATE TABLE things (id INTEGER NOT NULL PRIMARY KEY, name TEXT NOT NULL, other INTEGER REFERENCES things(id) ON DELETE CASCADE)`,
		`CREATE TABLE things (id INTEGER NOT NULL PRIMARY KEY, other INTEGER REFERENCES things(id), name TEXT NOT NULL)`,
		`CREATE TABLE things (id INTEGER NOT NULL PRIMARY KEY, name TEXT NOT NULL, other INTEGER REFERENCES things(id)) STRICT`,
	} {
		shape, err := schemaShape(ctx, databaseFrom(t, other, 0))
		if err != nil {
			t.Fatal(err)
		}
		if shape["table things"] == base["table things"] {
			t.Errorf("%s reads the same as the table it differs from: %s", other, shape["table things"])
		}
	}
}

// Once there is a release, its schema is kept in testdata/released/<version>.sql, and this
// holds every later change to the schema files to having a step that makes the same change:
// a database from each release, upgraded, must be what a new database is. A schema file
// edited without a step fails here.
func TestAReleasedDatabaseUpgradesToTheShapeOfANewOne(t *testing.T) {
	released, _ := filepath.Glob("testdata/released/*.sql")
	if len(released) == 0 {
		t.Skip("nothing is released yet")
	}
	ctx := context.Background()
	for _, file := range released {
		version, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(file), ".sql"))
		if err != nil {
			t.Fatalf("%s is not named for its version", file)
		}
		schema, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "main.db")
		conn, err := open(path, "rwc", 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(string(schema) + fmt.Sprintf(`; INSERT INTO schema_migrations (version, applied_at) VALUES (%d, 0)`, version)); err != nil {
			t.Fatal(err)
		}
		conn.Close()
		d, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("release %d: %v", version, err)
		}
		<-d.upgradesDone
		if err := CheckSchema(ctx, d.read); err != nil {
			t.Errorf("a database from release %d, upgraded, is not what a new one is: %v", version, err)
		}
		d.Close()
	}
}
