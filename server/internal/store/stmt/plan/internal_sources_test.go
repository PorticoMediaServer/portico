package plan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestPlanSQLiteInternalSourcesNeverQualify(t *testing.T) {
	conn, _, f, _ := fixture(t)
	ctx := context.Background()
	for _, sql := range []string{"CREATE TABLE au(id INTEGER PRIMARY KEY AUTOINCREMENT)", "INSERT INTO au DEFAULT VALUES", "CREATE TABLE rp(id INTEGER PRIMARY KEY)", "INSERT INTO rp VALUES(1)"} {
		if _, err := conn.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"sqlite_sequence", "SQLITE_SEQUENCE", "sQlItE_sEqUeNcE", "sqlite_schema", "sqlite_master", "sqlite_stat1", "sqlite_not_here"} {
		sqls := []string{"SELECT p.seq FROM " + name + " AS p WHERE p.rowid=1"}
		for _, join := range []string{"INNER", "LEFT", "CROSS"} {
			sqls = append(sqls, "SELECT p.id FROM rp AS p "+join+" JOIN "+name+" AS b ON b.rowid=p.id WHERE p.id=1")
		}
		for _, sql := range sqls {
			d := stmt.Definition{ID: "internal.source", SQL: sql, Kind: stmt.Read, Owner: "plan-test", CallSites: []string{"internal_sources_test.go"},
				Cohorts: []stmt.Cohort{{Name: "one", Bindings: []any{}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"p"}}}
			if strings.EqualFold(name, "sqlite_sequence") {
				if err := plan.CheckEQP(d, reviewRawEQP(t, conn, sql)); err != nil {
					t.Fatal("internal-table counterexample must have all-SEARCH EQP", err)
				}
			}
			if len(plan.LintSQL(sql)) == 0 || len(plan.LintSQLOn(ctx, conn, sql)) == 0 {
				t.Fatal("internal source passed lint", sql)
			}
			r, err := stmt.Describe([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Capture(ctx, conn, r, f, indexPolicy(t)); err == nil || !strings.Contains(err.Error(), "SQLite internal") {
				t.Fatalf("internal source certified: %s: %v", sql, err)
			}
		}
	}
	for _, name := range []string{"sqlite_sequence", "SQLITE_SEQUENCE", `"sQlItE_sEqUeNcE"`} {
		for _, sql := range []string{"INSERT INTO " + name + "(name,seq) VALUES(?,?)", "UPDATE " + name + " SET seq=? WHERE rowid=?", "DELETE FROM " + name + " WHERE rowid=?"} {
			if len(plan.LintSQL(sql)) == 0 || len(plan.LintSQLOn(ctx, conn, sql)) == 0 {
				t.Fatal("internal DML target passed lint", sql)
			}
			args := make([]any, strings.Count(sql, "?"))
			for i := range args {
				args[i] = int64(1)
			}
			d := reviewDefinition(sql, args, stmt.Plan{Hot: true, PrimaryKeys: []string{"sqlite_sequence"}})
			r, err := stmt.Describe([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Capture(ctx, conn, r, f, indexPolicy(t)); err == nil || !strings.Contains(err.Error(), "SQLite internal") {
				t.Fatalf("internal DML target certified: %s: %v", sql, err)
			}
		}
	}
	// Prefix restriction applies to physical sources, not an alias spelling.
	valid := "SELECT sqlite_sequence.id FROM au AS sqlite_sequence WHERE sqlite_sequence.id=1"
	if issues := plan.LintSQLOn(ctx, conn, valid); len(issues) != 0 {
		t.Fatal("ordinary source rejected for its alias", issues)
	}
}
