package db

import (
	"context"
	"database/sql"
	"testing"
)

// A trigger or a view given a name beginning sqlite_ by writing to the schema table is run by
// SQLite like any other, so a copy holding one is refused like any other. A copy with a table
// missing is refused too: it would open and then fail on the first request that needs it.
func TestACopyWithAHiddenTriggerOrAMissingTableIsRefused(t *testing.T) {
	ctx := context.Background()
	for name, change := range map[string][]string{
		"a trigger named like one of SQLite's own": {
			`PRAGMA writable_schema = ON`,
			`INSERT INTO sqlite_master (type, name, tbl_name, rootpage, sql) VALUES ('trigger', 'sqlite_evil', 'settings', 0,
				'CREATE TRIGGER sqlite_evil AFTER INSERT ON settings BEGIN UPDATE settings SET value = ''owned'' WHERE key = new.key; END')`,
		},
		"a view named like one of SQLite's own": {
			`PRAGMA writable_schema = ON`,
			`INSERT INTO sqlite_master (type, name, tbl_name, rootpage, sql) VALUES ('view', 'sqlite_v', 'sqlite_v', 0, 'CREATE VIEW sqlite_v AS SELECT 1')`,
		},
		"a table missing": {`DROP TABLE auth_limits`},
	} {
		path := t.TempDir() + "/main.db"
		d, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		d.Close()
		if err := CheckCopy(ctx, path); err != nil {
			t.Fatalf("a new database fails its own check: %v", err)
		}
		raw, _ := sql.Open("sqlite", "file:"+path)
		raw.SetMaxOpenConns(1)
		for _, q := range change {
			if _, err := raw.Exec(q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		raw.Close()
		if err := CheckCopy(ctx, path); err == nil {
			t.Errorf("a copy with %s passed its check", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
}
