package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

func TestNativeTotalChangesIncludesAllSQLiteEffects(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exec := func(q string) {
		t.Helper()
		if _, err := c.ExecContext(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	// Read the native value before the SQL oracle: a SQL step must not get a
	// chance to repair a stale per-statement cache before the comparison.
	check := func(stage string) int64 {
		t.Helper()
		var native, oracle int64
		if err := c.Raw(func(raw any) error {
			var known bool
			native, known = raw.(*conn).TotalChanges()
			if !known {
				t.Error("live native counter is unknown")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(context.Background(), `SELECT total_changes()`).Scan(&oracle); err != nil {
			t.Fatal(err)
		}
		if native != oracle {
			t.Fatalf("%s: native=%d SQL=%d", stage, native, oracle)
		}
		return native
	}
	exec(`PRAGMA foreign_keys=ON;
CREATE TABLE parents(id INTEGER PRIMARY KEY, value TEXT);
CREATE TABLE children(parent INTEGER REFERENCES parents(id) ON DELETE CASCADE ON UPDATE CASCADE);
CREATE TABLE audit(value TEXT);
CREATE TRIGGER inserted AFTER INSERT ON parents BEGIN INSERT INTO audit VALUES('insert'); END;
CREATE TRIGGER updated AFTER UPDATE ON parents BEGIN INSERT INTO audit VALUES('update'); END;
CREATE TRIGGER deleted AFTER DELETE ON parents BEGIN INSERT INTO audit VALUES('delete'); END;`)
	before := check("schema")
	exec(`INSERT INTO parents VALUES(1,'a')`)
	if delta := check("insert trigger") - before; delta != 2 {
		t.Fatalf("lost trigger effects: %d", delta)
	}
	exec(`INSERT INTO children VALUES(1)`)
	before = check("child")
	exec(`UPDATE parents SET id=2 WHERE id=1`)
	if delta := check("update cascade") - before; delta != 3 {
		t.Fatalf("lost cascade/trigger effects: %d", delta)
	}
	before = check("before delete")
	exec(`DELETE FROM parents WHERE id=2`)
	if delta := check("delete cascade") - before; delta != 3 {
		t.Fatalf("lost delete effects: %d", delta)
	}
	exec(`INSERT INTO parents VALUES(3,'first'); INSERT OR REPLACE INTO parents VALUES(3,'replacement')`)
	check("replace")
	exec(`BEGIN; INSERT INTO parents VALUES(4,'a'),(5,'b')`)
	before = check("uncommitted")
	exec(`ROLLBACK`)
	if after := check("rollback"); after != before {
		t.Fatalf("rollback reset cumulative count: %d -> %d", before, after)
	}
	before = check("before partial failure")
	if _, err := c.ExecContext(context.Background(), `INSERT OR FAIL INTO parents VALUES(6,'a'),(6,'duplicate'),(7,'unreached')`); err == nil {
		t.Fatal("expected constraint failure")
	}
	if delta := check("partial failure") - before; delta != 2 {
		t.Fatalf("lost successful prefix of failed statement: %d", delta)
	}
	rows, err := c.QueryContext(context.Background(), `INSERT INTO parents VALUES(8,'a'),(9,'b') RETURNING id`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("missing returning row", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	check("partially consumed returning")
	exec(`UPDATE parents SET value='no-match' WHERE id=-1`)
	check("no-op")
}

func TestNativeTotalChangesRefusesClosedConnection(t *testing.T) {
	opened, err := (&Driver{}).Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	c := opened.(*conn)
	if _, known := c.TotalChanges(); !known {
		t.Fatal("live counter unknown")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, known := c.TotalChanges(); known {
		t.Fatal("closed connection retained a valid counter")
	}
}
