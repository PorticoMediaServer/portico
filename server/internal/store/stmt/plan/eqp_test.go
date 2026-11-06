package plan_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestPlanOptimizedINNeedsNativeWaiver(t *testing.T) {
	conn, _, f, _ := fixture(t)
	ctx := context.Background()
	for _, sql := range []string{
		"CREATE TABLE rhs(id INTEGER PRIMARY KEY, key INTEGER)",
		"CREATE INDEX rhs_key ON rhs(key)",
		"CREATE TABLE onecol(id INTEGER PRIMARY KEY)",
	} {
		if _, err := conn.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, sql string
		kind      stmt.Kind
	}{
		{"rowid", "SELECT id FROM items WHERE id=? AND id IN (SELECT id FROM rhs)", stmt.Read},
		{"not-in", "SELECT id FROM items WHERE id=? AND id NOT IN (SELECT id FROM rhs)", stmt.Read},
		{"index", "SELECT id FROM items WHERE id=? AND key IN (SELECT key FROM rhs)", stmt.Read},
		{"table", "SELECT id FROM items WHERE id=? AND id IN onecol", stmt.Read},
		{"update", "UPDATE items SET key=0 WHERE id=? AND id IN (SELECT id FROM rhs)", stmt.Write},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := stmt.Definition{
				ID: stmt.ID("review.optimized-in." + tc.name), SQL: tc.sql,
				Owner: "test", CallSites: []string{"eqp_test.go"}, Kind: tc.kind,
				Params:  []stmt.Param{{Name: "id", Type: stmt.Integer}},
				Cohorts: []stmt.Cohort{{Name: "one", Bindings: []any{int64(1)}}},
				Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"items"}},
			}
			issues := plan.LintSQL(d.SQL)
			if len(issues) == 0 {
				t.Fatal("review form unexpectedly inside the SQL grammar")
			}
			eqp := reviewRawEQP(t, conn, d.SQL, int64(1))
			if err := plan.CheckEQP(d, eqp); err == nil || !strings.Contains(err.Error(), "forbidden plan FOR IN-OPERATOR") {
				t.Fatalf("optimized IN native guard failed: %v", err)
			}
			r, err := stmt.Describe([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Capture(ctx, conn, r, f, indexPolicy(t)); err == nil {
				t.Fatal("outside grammar qualified")
			}
		})
	}
	for _, tc := range []struct {
		sql      string
		bindings []any
	}{
		{"SELECT id FROM items WHERE id=? AND id IN(1,2)", []any{int64(1)}},
		{"SELECT id FROM items WHERE id=? AND id IN(?,?)", []any{int64(1), int64(1), int64(2)}},
	} {
		d := stmt.Definition{
			ID: "review.literal-in", SQL: tc.sql, Owner: "test", CallSites: []string{"eqp_test.go"}, Kind: stmt.Read,
			Cohorts: []stmt.Cohort{{Name: "one", Bindings: tc.bindings}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"items"}},
		}
		for _, name := range []string{"id", "first", "second"}[:len(tc.bindings)] {
			d.Params = append(d.Params, stmt.Param{Name: name, Type: stmt.Integer})
		}
		r, err := stmt.Describe([]stmt.Definition{d})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plan.Capture(ctx, conn, r, f, indexPolicy(t)); err != nil {
			t.Fatalf("literal/parameter IN list needs no waiver: %v", err)
		}
	}
}

func TestPlanNativeEQPClosedShapes(t *testing.T) {
	d := stmt.Definition{ID: "review.eqp-shape", SQL: "SELECT id FROM items WHERE id=?"}
	row := func(detail string) plan.Rows {
		return plan.Rows{Columns: []string{"id", "parent", "unused", "detail"}, Values: [][]any{{int64(1), int64(0), int64(0), detail}}}
	}
	for _, detail := range []string{
		"SEARCH items USING INDEX item_sort (library_id=? AND key>? AND id<?)",
		"SEARCH items USING COVERING INDEX item_sort (library_id=? AND key>=? AND key<=?)",
		"SEARCH items USING INTEGER PRIMARY KEY (rowid=?)",
		"SEARCH child USING COVERING INDEX child_parent (parent_id=?) LEFT-JOIN",
		"search items using primary key (id=?)",
	} {
		if err := plan.CheckEQP(d, row(detail)); err != nil {
			t.Fatalf("expected SEARCH shape rejected: %v", err)
		}
	}
	for detail, form := range map[string]string{
		"SEARCH child USING INDEX child_parent (parent_id=?) LEFT JOIN":          "UNRECOGNIZED EQP",
		"SEARCH child USING INDEX child_parent (parent_id=?) LEFT-JOIN trailing": "UNRECOGNIZED EQP",
		"USING ROWID SEARCH ON TABLE rhs FOR IN-OPERATOR":                        "FOR IN-OPERATOR",
		"USING INDEX rhs_key FOR IN-OPERATOR":                                    "FOR IN-OPERATOR",
		"LIST SUBQUERY 1":                                                        "SUBQUERY",
		"SCALAR SUBQUERY 1":                                                      "SUBQUERY",
		"CORRELATED 1":                                                           "CORRELATED",
		"BLOOM FILTER ON rhs (id=?)":                                             "BLOOM FILTER",
		"AUTOMATIC INDEX":                                                        "AUTOMATIC INDEX",
		"SEARCH rhs USING AUTOMATIC PARTIAL COVERING INDEX (id=?)":               "AUTOMATIC INDEX",
		"USE TEMP B-TREE FOR GROUP BY":                                           "USE TEMP B-TREE",
		"USE TEMP B-TREE FOR DISTINCT":                                           "USE TEMP B-TREE",
		"SCAN items USING COVERING INDEX item_sort":                              "SCAN",
		"SEARCH items USING INTEGER PRIMARY KEY (rowid=?) unexpected":            "UNRECOGNIZED EQP",
		"SEARCH items USING INDEX item_sort (ANY(kind) AND key=?)":               "UNRECOGNIZED EQP",
		"SEARCH items USING INDEX item_sort (id=?) (unknown operator)":           "UNRECOGNIZED EQP",
		"SEARCH items USING INDEX item_sort (unknown constraint)":                "UNRECOGNIZED EQP",
		"SEARCH items USING INDEX item_sort":                                     "UNRECOGNIZED EQP",
		"FUTURE EPHEMERAL OPERATOR rhs":                                          "UNRECOGNIZED EQP",
		"":                                                                       "UNRECOGNIZED EQP",
	} {
		t.Run(detail, func(t *testing.T) {
			d := d
			d.Plan.Waivers = []stmt.Waiver{{StatementID: d.ID, Stage: "sql-lint", Form: form, Match: d.SQL, Reason: "Synthetic wrong-stage permission"}}
			if err := plan.CheckEQP(d, row(detail)); err == nil {
				t.Fatal("unapproved native shape accepted with lint-only waiver")
			}
			// Empty details fail closed; registry waivers require nonempty Match.
			if detail == "" {
				return
			}
			d.Plan.Waivers = []stmt.Waiver{{StatementID: d.ID, Stage: "eqp", Form: form, Match: detail, Reason: "Synthetic exact native permission"}}
			if err := plan.CheckEQP(d, row(detail)); err != nil {
				t.Fatalf("exact native waiver rejected: %v", err)
			}
			if err := plan.CheckEQP(d, row(detail+" changed")); err == nil {
				t.Fatal("native waiver accepted a changed detail")
			}
		})
	}
}

// Compile only: this keeps the independent native guard exercised even when
// the closed grammar rejects a statement before Capture prepares it.
func reviewRawEQP(t *testing.T, conn *sql.Conn, sql string, args ...any) plan.Rows {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	result := plan.Rows{Columns: cols, Values: [][]any{}}
	for rows.Next() {
		var id, parent, unused int64
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		result.Values = append(result.Values, []any{id, parent, unused, detail})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
