package plan_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestSQLClosedDML(t *testing.T) {
	for _, sql := range []string{
		"INSERT INTO t (id,value) VALUES (?,?)",
		"INSERT INTO t (id,value) VALUES (?,?) ON CONFLICT(id) DO NOTHING RETURNING id,value",
		"INSERT INTO t (id,value) VALUES (?,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value,id=? RETURNING id",
		"UPDATE t SET value=? WHERE id=? RETURNING id,value",
		"UPDATE t INDEXED BY ix SET value=coalesce(value,0)+?,id=id-1 WHERE id BETWEEN ? AND ?",
		"UPDATE t SET value=replace(value,'(',')') WHERE id IN(1,2) OR value IS NULL",
		"DELETE FROM t WHERE id=?",
		"DELETE FROM t INDEXED BY ix WHERE id IN(?,?) AND value IS NOT NULL RETURNING id",
		"insert into [t] ([id],`value`) values (?,?) on conflict([id]) do update set `value`=excluded.`value` returning [id];",
	} {
		if issues := plan.LintSQL(sql); len(issues) != 0 {
			t.Errorf("inside grammar flagged: %s: %v", sql, issues)
		}
	}
	for _, sql := range []string{
		"REPLACE INTO t(id) VALUES(?)", "INSERT OR REPLACE INTO t(id) VALUES(?)", "INSERT INTO t DEFAULT VALUES",
		"INSERT INTO t(id) SELECT id FROM rhs", "INSERT INTO t(id) VALUES(1)", "INSERT INTO t(id) VALUES(?),(?)",
		"INSERT INTO t(id,value) VALUES(?)", "INSERT INTO t(id) VALUES(?,?)", "INSERT INTO t(id,ID) VALUES(?,?)",
		"INSERT INTO t(id) VALUES((SELECT id FROM rhs))", "INSERT INTO t(id) VALUES(coalesce(?,0))",
		"INSERT INTO t(id) VALUES(?) ON CONFLICT DO NOTHING", "INSERT INTO t(id) VALUES(?) ON CONFLICT((id)) DO NOTHING",
		"INSERT INTO t(id) VALUES(?) ON CONFLICT(id) WHERE id>0 DO NOTHING",
		"INSERT INTO t(id) VALUES(?) ON CONFLICT(id) DO UPDATE SET id=(SELECT id FROM rhs)",
		"INSERT INTO t(id) VALUES(?) ON CONFLICT(id) DO UPDATE SET id=excluded.id+1",
		"INSERT INTO t(id) VALUES(?) ON CONFLICT(id) DO UPDATE SET id=? WHERE id=?",
		"UPDATE t SET value=?", "UPDATE t SET (id,value)=(?,?) WHERE id=?", "UPDATE t SET value=(?) WHERE id=?",
		"UPDATE t SET value=? FROM rhs WHERE id=?", "UPDATE t SET value=(SELECT id FROM rhs) WHERE id=?",
		"UPDATE t SET value=CASE id WHEN 1 THEN ? END WHERE id=?", "UPDATE t SET value=unknown(?) WHERE id=?",
		"UPDATE t SET value=? WHERE id IN rhs", "UPDATE t SET value=? WHERE id LIKE (SELECT id FROM rhs)",
		"DELETE FROM t", "DELETE FROM t WHERE id=? LIMIT ?", "DELETE FROM t WHERE (id,value)>(?,?)",
		"DELETE FROM t WHERE id=(SELECT id FROM rhs)", "DELETE FROM t WHERE id=? RETURNING *",
		"UPDATE t SET value=? WHERE id=? RETURNING abs(id)", "DELETE FROM t WHERE id=? RETURNING id AS other",
		"DELETE FROM t WHERE id=?; INSERT INTO t(id) VALUES(?)",
	} {
		if len(plan.LintSQL(sql)) == 0 {
			t.Errorf("outside grammar allowed: %s", sql)
		}
	}
}

func TestPlanDMLNativeLookupsAndInsertAudit(t *testing.T) {
	conn, _, f, _ := fixture(t)
	ctx := context.Background()
	for _, sql := range []string{
		"CREATE TABLE dml_parent(id INTEGER PRIMARY KEY)",
		"CREATE TABLE dml_items(id INTEGER PRIMARY KEY, key TEXT NOT NULL UNIQUE, value INTEGER, parent_id INTEGER REFERENCES dml_parent(id))",
		"CREATE INDEX dml_parent_lookup ON dml_items(parent_id)",
		"INSERT INTO dml_parent VALUES(1)", "INSERT INTO dml_items VALUES(1,'retained',7,1)",
	} {
		if _, err := conn.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	makeDef := func(sql string, contract stmt.Plan) stmt.Definition {
		d := stmt.Definition{ID: "dml.test", SQL: sql, Owner: "test", CallSites: []string{"dml_test.go"}, Kind: stmt.Write, Plan: contract}
		for i := 0; i < strings.Count(sql, "?"); i++ {
			d.Params = append(d.Params, stmt.Param{Name: strings.Repeat("p", i+1), Type: stmt.Integer})
		}
		bindings := make([]any, len(d.Params))
		for i := range bindings {
			bindings[i] = int64(1)
		}
		d.Cohorts = []stmt.Cohort{{Name: "one", Bindings: bindings}}
		return d
	}
	capture := func(sql string, contract stmt.Plan) (plan.Golden, error) {
		r, err := stmt.Describe([]stmt.Definition{makeDef(sql, contract)})
		if err != nil {
			t.Fatal(err)
		}
		return plan.Capture(ctx, conn, r, f, indexPolicy(t))
	}
	for _, tc := range []struct {
		sql              string
		contract         stmt.Plan
		insert, conflict bool
	}{
		{"INSERT INTO dml_items(id,key,value,parent_id) VALUES(?,?,?,?) RETURNING id", stmt.Plan{Hot: true}, true, false},
		{"INSERT INTO dml_items(id,key,value,parent_id) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET value=? RETURNING id", stmt.Plan{Hot: true, PrimaryKeys: []string{"dml_items"}}, true, true},
		{"INSERT INTO dml_items(id,key,value,parent_id) VALUES(?,?,?,?) ON CONFLICT(key) DO NOTHING", stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_dml_items_1"}}, true, true},
		{"INSERT INTO dml_items(id,key,value,parent_id) VALUES(?,?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_dml_items_1"}}, true, true},
		{"UPDATE dml_items SET value=coalesce(value,0)+? WHERE id=? RETURNING id,value", stmt.Plan{Hot: true, PrimaryKeys: []string{"dml_items"}}, false, false},
		{"UPDATE dml_items INDEXED BY dml_parent_lookup SET value=? WHERE parent_id=? AND id>?", stmt.Plan{Hot: true, Indexes: []string{"dml_parent_lookup"}}, false, false},
		{"DELETE FROM dml_items WHERE id=? RETURNING id", stmt.Plan{PrimaryKeys: []string{"dml_items"}}, false, false},
		{"SELECT id FROM dml_items WHERE key=?", stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_dml_items_1"}}, false, false},
	} {
		g, err := capture(tc.sql, tc.contract)
		if err != nil {
			t.Fatalf("closed DML failed: %s: %v", tc.sql, err)
		}
		p := g.Plans[0]
		if (p.Insert != nil) != tc.insert || (g.ForeignKeys != nil) != tc.insert {
			t.Fatal("destination/FK evidence missing or misplaced")
		}
		if tc.insert {
			if (p.Insert.Conflict != nil) != tc.conflict || len(p.Insert.Indexes.Values) == 0 || len(p.Insert.Columns.Values) == 0 {
				t.Fatal("insert index/column/conflict evidence missing")
			}
		}
		data, _ := g.JSON()
		var roundtrip plan.Golden
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.UseNumber()
		if err := decoder.Decode(&roundtrip); err != nil {
			t.Fatal(err)
		}
		if err := roundtrip.Compare(data); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		sql      string
		contract stmt.Plan
	}{
		{"UPDATE dml_items SET value=? WHERE id=?", stmt.Plan{}},
		{"DELETE FROM dml_items WHERE value=?", stmt.Plan{Hot: true, PrimaryKeys: []string{"dml_items"}}},
		{"INSERT INTO dml_items(id,key) VALUES(?,?) ON CONFLICT(key) DO NOTHING", stmt.Plan{Hot: true}},
		{"INSERT INTO dml_items(id,key,parent_id) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING", stmt.Plan{Hot: true, Indexes: []string{"dml_parent_lookup"}}},
		{"INSERT INTO dml_items(id,key) VALUES(?,?) ON CONFLICT(key) DO NOTHING", stmt.Plan{Hot: true, PrimaryKeys: []string{"dml_items"}}},
		{"INSERT INTO dml_items(id,key) VALUES(?,?) ON CONFLICT(key) DO NOTHING", stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_dml_items_1; SELECT 1"}}},
		{"SELECT id FROM dml_items WHERE key=?", stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_dml_items_2"}}},
		{"SELECT id FROM dml_items INDEXED BY sqlite_autoindex_dml_items_1 WHERE key=?", stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_dml_items_1"}}},
		{`SELECT id FROM dml_items INDEXED BY "sqlite_autoindex_dml_items_1" WHERE key=?`, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_dml_items_1"}}},
		{"SELECT id FROM dml_items WHERE parent_id=?", stmt.Plan{Hot: true, Indexes: []string{"dml_parent_lookup"}}},
	} {
		if _, err := capture(tc.sql, tc.contract); err == nil {
			t.Fatalf("unproved lookup qualified: %s", tc.sql)
		}
	}
	// A lint exception cannot turn INSERT SELECT/CTE writes into read plans and
	// omit their destination/constraint audit, regardless of Kind or Hot.
	for _, sql := range []string{
		"INSERT INTO dml_items(id,key) SELECT id,id FROM dml_parent WHERE id=?",
		"WITH src AS (SELECT id FROM dml_parent WHERE id=?) INSERT INTO dml_items(id,key) SELECT id,id FROM src",
	} {
		d := makeDef(sql, stmt.Plan{PrimaryKeys: []string{"dml_parent"}})
		d.Kind = stmt.Read
		for _, issue := range plan.LintSQL(sql) {
			d.Plan.Waivers = append(d.Plan.Waivers, stmt.Waiver{StatementID: d.ID, Stage: "sql-lint", Form: issue.Form, Match: sql, Reason: "Synthetic regression; no destination audit permission"})
		}
		if _, err := stmt.Describe([]stmt.Definition{d}); err == nil {
			t.Fatal("SQL lint waiver accepted")
		}
		d.Plan.Waivers = nil
		r, err := stmt.Describe([]stmt.Definition{d})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plan.Capture(ctx, conn, r, f, indexPolicy(t)); err == nil {
			t.Fatal("unrecognized write qualified")
		}
	}
	if _, err := conn.ExecContext(ctx, "CREATE TEMP TABLE dml_items(id INTEGER PRIMARY KEY,key TEXT,parent_id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := capture("INSERT INTO dml_items(id,key,parent_id) VALUES(?,?,?)", stmt.Plan{Hot: true}); err == nil {
		t.Fatal("temp shadow substituted for audited main destination")
	}
	if _, err := conn.ExecContext(ctx, "DROP TABLE temp.dml_items"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "DROP INDEX dml_parent_lookup"); err != nil {
		t.Fatal(err)
	}
	if _, err := capture("INSERT INTO dml_items(id,key,parent_id) VALUES(?,?,?)", stmt.Plan{Hot: true}); err == nil || !strings.Contains(err.Error(), "SCAN") {
		t.Fatalf("missing FK child index qualified: %v", err)
	}
	var count, value int
	if err := conn.QueryRowContext(ctx, "SELECT count(*),value FROM dml_items").Scan(&count, &value); err != nil {
		t.Fatal(err)
	}
	if count != 1 || value != 7 {
		t.Fatal("Capture executed an accepting mutation")
	}
}
