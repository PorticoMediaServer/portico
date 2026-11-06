package plan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestPlanMainBaseTableSourceResolution(t *testing.T) {
	conn, _, f, _ := fixture(t)
	ctx := context.Background()
	for _, sql := range []string{
		"CREATE TABLE rp(id INTEGER PRIMARY KEY)",
		"INSERT INTO rp VALUES(1)",
		"CREATE TABLE rc(id INTEGER PRIMARY KEY,parent_id INTEGER NOT NULL,flag INTEGER NOT NULL)",
		"CREATE INDEX rc_parent ON rc(parent_id)",
		"INSERT INTO rc VALUES(1,1,0)",
		"CREATE VIEW rv AS SELECT id,parent_id,flag FROM rc WHERE (flag+0)=0",
		"CREATE TEMP TABLE rt(id INTEGER PRIMARY KEY)",
		"INSERT INTO rt VALUES(1)",
		"CREATE VIRTUAL TABLE rfts USING fts5(body)",
		"ATTACH ':memory:' AS aux",
		"CREATE TABLE aux.only_aux(id INTEGER PRIMARY KEY)",
	} {
		if _, err := conn.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	definition := func(sql string) stmt.Definition {
		return stmt.Definition{ID: "source.test", SQL: sql, Owner: "plan-test", CallSites: []string{"sources_test.go"}, Kind: stmt.Read,
			Cohorts: []stmt.Cohort{{Name: "point", Bindings: []any{}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"p"}}}
	}
	reject := func(t *testing.T, sql string) {
		t.Helper()
		if issues := plan.LintSQLOn(ctx, conn, sql); len(issues) == 0 {
			t.Fatalf("live source lint accepted %s", sql)
		}
		r, err := stmt.Describe([]stmt.Definition{definition(sql)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plan.Capture(ctx, conn, r, f, indexPolicy(t)); err == nil || !strings.Contains(err.Error(), "unapproved SQL lint") {
			t.Fatalf("invalid source qualified: %s: %v", sql, err)
		}
	}
	for _, kind := range []string{"INNER", "LEFT", "CROSS"} {
		t.Run(kind, func(t *testing.T) {
			for _, tc := range []struct{ name, source, column string }{
				{"view", "rv", "parent_id"},
				{"temp", "rt", "id"},
				{"quoted_temp", `"temp".rt`, "id"},
				{"main_qualified", "main.rc", "parent_id"},
				{"quoted", `"rc"`, "parent_id"},
				{"virtual", "rfts", "rowid"},
				{"shadow", "rfts_data", "id"},
				{"missing", "not_here", "id"},
				{"attached_only", "only_aux", "id"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					sql := "SELECT p.id FROM rp AS p " + kind + " JOIN " + tc.source + " AS b ON b." + tc.column + "=p.id WHERE p.id=1"
					if tc.name == "view" || tc.name == "temp" {
						// SQLite flattens the view and uses PKs for TEMP. Its own
						// all-SEARCH plan cannot substitute for source validation.
						if err := plan.CheckEQP(definition(sql), reviewRawEQP(t, conn, sql)); err != nil {
							t.Fatalf("review bypass no longer exercises all-SEARCH: %v", err)
						}
					}
					reject(t, sql)
				})
			}
			reject(t, "SELECT rp.id FROM rp "+kind+" JOIN rc INDEXED BY rc_parent ON rc.parent_id=rp.id WHERE rp.id=1")
			// Valid ordinary MAIN sources still qualify, including explicit
			// NOT INDEXED when SQLite genuinely uses the rowid primary key.
			valid := "SELECT p.id FROM rp AS p " + kind + " JOIN rc AS b NOT INDEXED ON b.id=p.id WHERE p.id=1"
			if issues := plan.LintSQLOn(ctx, conn, valid); len(issues) != 0 {
				t.Fatal(issues)
			}
			r, err := stmt.Describe([]stmt.Definition{definition(valid)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Capture(ctx, conn, r, f, indexPolicy(t)); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, source := range []string{"rv", "rt", `"temp".rt`, "main.rc", `"rc"`, "rfts", "rfts_data", "not_here", "only_aux"} {
		reject(t, "SELECT p.id FROM "+source+" AS p WHERE p.id=1")
	}
	for _, sql := range []string{"SELECT id FROM rp WHERE id=1", "SELECT p.id FROM RP AS p WHERE p.id=1"} {
		if issues := plan.LintSQLOn(ctx, conn, sql); len(issues) != 0 {
			t.Fatal("ordinary MAIN source rejected", issues)
		}
	}
	// A TEMP object can shadow an otherwise valid MAIN base table. Case is
	// folded exactly as SQLite resolves its unquoted identifiers.
	if _, err := conn.ExecContext(ctx, "CREATE TEMP VIEW RC AS SELECT id,parent_id,flag FROM main.rc"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"INNER", "LEFT", "CROSS"} {
		reject(t, "SELECT p.id FROM rp AS p "+kind+" JOIN rc AS b ON b.parent_id=p.id WHERE p.id=1")
	}
	reject(t, "SELECT p.id FROM rc AS p WHERE p.id=1")
	if _, err := conn.ExecContext(ctx, "DROP VIEW temp.RC"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "CREATE TEMP TABLE rc(id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	reject(t, "SELECT p.id FROM rc AS p WHERE p.id=1")
	if len(plan.LintSQLOn(ctx, nil, "SELECT id FROM rp")) == 0 {
		t.Fatal("nil connection qualified sources")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if len(plan.LintSQLOn(canceled, conn, "SELECT id FROM rp")) == 0 {
		t.Fatal("canceled source lookup qualified")
	}
}
