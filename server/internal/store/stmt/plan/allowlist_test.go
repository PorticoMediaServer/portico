package plan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestPlanReviewAllowListAndNativeCounterexamples(t *testing.T) {
	conn, r, f, _ := fixture(t)
	base, _ := r.Lookup("sort.tail")
	original, _ := base.Definition()
	subquery := "SELECT key FROM items INDEXED BY item_sort WHERE library_id=1 AND " + predicate + " LIMIT 1"
	for name, condition := range map[string]string{
		"like":              "key LIKE (" + subquery + ")",
		"not-like":          "key NOT LIKE (" + subquery + ")",
		"glob":              "key GLOB (" + subquery + ")",
		"nested-like":       "key LIKE (((" + subquery + ")))",
		"postfix-not-null":  "(" + subquery + ") NOT NULL",
		"commented-postfix": "(((" + subquery + "))) NOT /*NULL?*/ NULL",
		"simple-case-base":  "CASE (" + subquery + ") WHEN key THEN 1 ELSE 0 END",
		"simple-case-when":  "CASE key WHEN (" + subquery + ") THEN 1 ELSE 0 END",
		"scalar-postfix":    "key NOT NULL",
		"scalar-case":       "CASE key WHEN 1 THEN 1 ELSE 0 END",
		"grouping":          "((key))>0",
		"row-value":         "(key,id)>(0,0)",
	} {
		t.Run(name, func(t *testing.T) {
			d := original
			d.SQL = "SELECT id,key FROM items INDEXED BY item_sort WHERE library_id=? AND kind=? AND " + condition + " AND key=? AND id>? AND " + predicate + " ORDER BY key,id LIMIT ?"
			if len(plan.LintSQL(d.SQL)) == 0 {
				t.Fatalf("outside-grammar predicate allowed: %s", condition)
			}
			registry, err := stmt.New([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Capture(context.Background(), conn, registry, f, indexPolicy(t)); err == nil {
				t.Fatalf("outside-grammar statement qualified: %s", condition)
			}
		})
	}
	for _, not := range []string{"", "NOT "} {
		t.Run("IN-table-"+not, func(t *testing.T) {
			d := original
			d.SQL = "WITH scoped AS (" + subquery + ") SELECT id,key FROM items INDEXED BY item_sort WHERE library_id=? AND kind=? AND key " + not + "IN scoped AND key=? AND id>? AND " + predicate + " ORDER BY key,id LIMIT ?"
			if len(plan.LintSQL(d.SQL)) == 0 {
				t.Fatal("IN table shorthand allowed")
			}
			registry, err := stmt.New([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Capture(context.Background(), conn, registry, f, indexPolicy(t)); err == nil {
				t.Fatal("IN table shorthand qualified")
			}
		})
	}
	for _, projection := range []string{"row_number() OVER (ORDER BY key,id)>0", "sum(key) OVER (PARTITION BY kind,library_id ORDER BY key)>0"} {
		d := original
		d.SQL = strings.Replace(d.SQL, "SELECT id,key", "SELECT id,key,"+projection, 1)
		if len(plan.LintSQL(d.SQL)) == 0 {
			t.Fatal("window clause allowed without waiver")
		}
		registry, err := stmt.New([]stmt.Definition{d})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plan.Capture(context.Background(), conn, registry, f, indexPolicy(t)); err == nil {
			t.Fatal("window clause qualified without waiver")
		}
	}
}

func TestSQLAllowListExplicitGrammar(t *testing.T) {
	for _, predicate := range []string{
		"key=? AND id>?", "key=1 OR id BETWEEN ? AND 10", "key IS NULL OR key IS NOT NULL",
		"key IN(?,?)", "kind IN(1,2,3,4,10)", "key IN('x','it''s (SELECT)')", "authored_refs>0",
		"abs(key)>?", "coalesce(key,0)=?", "like(?,key)=1", "glob(?,key)=1", "key LIKE ?", "key NOT GLOB other_key",
		"sort_key COLLATE NOCASE>?", "\"SELECT()\"='CASE OVER(,)'",
	} {
		sql := "SELECT id,key FROM items INDEXED BY item_sort WHERE " + predicate + " ORDER BY key,id LIMIT ?"
		if issues := plan.LintSQL(sql); len(issues) != 0 {
			t.Fatalf("allowed grammar flagged %q: %v", predicate, issues)
		}
	}
	for _, predicate := range []string{
		"(key)=?", "(key,id)>(?,?)", "key> (SELECT key FROM items)", "CASE key WHEN 1 THEN 0 END", "key NOT NULL", "key NOTNULL", "key ISNULL",
		"key IN scoped", "key NOT IN main.scoped", "key IN json_each(?)", "key IN(other_key)", "key IN(1.5)", "key IN()",
		"key LIKE (SELECT key FROM items)", "abs(key) LIKE ?", "'a' GLOB ?", "key GLOB lower(?)", "key MATCH (SELECT key FROM items)", "key REGEXP (SELECT key FROM items)",
		"sum(key) OVER(PARTITION BY kind,library_id)>0", "unknown(key)=?", "key+1>?", "key BETWEEN ? AND (SELECT key FROM items)",
		"key IS NOT DISTINCT FROM ?", "key=CURRENT_TIMESTAMP", "key='unterminated", "key=? /* unterminated", "key=? OFFSET 1", "key=?; SELECT 1",
	} {
		sql := "SELECT id,key FROM items WHERE " + predicate
		if len(plan.LintSQL(sql)) == 0 {
			t.Fatalf("outside grammar allowed: %s", sql)
		}
	}
}

func TestPlanNativeGuardAndStatementWaivers(t *testing.T) {
	d := stmt.Definition{ID: "waiver.test", SQL: "SELECT id FROM items WHERE key ISNULL", Plan: stmt.Plan{Hot: false}}
	row := func(detail string) plan.Rows {
		return plan.Rows{Columns: []string{"id", "parent", "unused", "detail"}, Values: [][]any{{int64(1), int64(0), int64(0), detail}}}
	}
	for _, detail := range []string{"SCALAR SUBQUERY 1", "CO-ROUTINE x", "MATERIALIZE x", "MULTI-INDEX OR", "USE TEMP B-TREE FOR ORDER BY", "SEARCH x USING AUTOMATIC COVERING INDEX (id=?)"} {
		if err := plan.CheckEQP(d, row(detail)); err == nil {
			t.Fatalf("SQLite guard accepted %s", detail)
		}
		d.Plan.Waivers = []stmt.Waiver{{StatementID: d.ID, Stage: "sql-lint", Form: "sql:outside-grammar", Match: d.SQL, Reason: "Test-only exact SQL waiver for postfix NULL syntax"}}
		if err := plan.CheckEQP(d, row(detail)); err == nil {
			t.Fatal("lint waiver bypassed SQLite guard")
		}
		form := detail
		switch {
		case strings.Contains(detail, "SUBQUERY"):
			form = "SUBQUERY"
		case strings.Contains(detail, "CO-ROUTINE"):
			form = "CO-ROUTINE"
		case strings.Contains(detail, "MATERIALIZE"):
			form = "MATERIALIZE"
		case strings.Contains(detail, "USE TEMP B-TREE"):
			form = "USE TEMP B-TREE"
		case strings.Contains(detail, "AUTOMATIC"):
			form = "AUTOMATIC INDEX"
		}
		d.Plan.Waivers = append(d.Plan.Waivers, stmt.Waiver{StatementID: d.ID, Stage: "eqp", Form: form, Match: detail, Reason: "Synthetic exact-detail waiver exercises the guard; no serving approval"})
		if err := plan.CheckEQP(d, row(detail)); err != nil {
			t.Fatal(err)
		}
		if err := plan.CheckEQP(d, row(detail+" changed")); err == nil {
			t.Fatal("EQP waiver matched changed detail")
		}
		d.Plan.Waivers = nil
	}
	d.Plan.Hot = true
	d.Plan.AllowedScans = []string{"items"}
	if err := plan.CheckEQP(d, row("SCAN items")); err == nil {
		t.Fatal("legacy metadata waived a hot scan without reason")
	}
	d.Plan.Hot = false
	d.Plan.Waivers = []stmt.Waiver{{StatementID: d.ID, Stage: "eqp", Form: "SUBQUERY", Match: "SCALAR SUBQUERY 1", Reason: "Synthetic EQP waiver only"}}
	if err := plan.Check(d, row("SEARCH items USING INTEGER PRIMARY KEY (rowid=?)")); err == nil {
		t.Fatal("EQP waiver bypassed SQL allow-list")
	}
	d.Plan.Waivers = []stmt.Waiver{{StatementID: d.ID, Stage: "sql-lint", Form: "sql:outside-grammar", Match: d.SQL, Reason: "Synthetic exact SQL waiver for postfix NULL syntax"}}
	if err := plan.Check(d, row("SEARCH items USING INTEGER PRIMARY KEY (rowid=?)")); err != nil {
		t.Fatal(err)
	}
	d.SQL += " AND id=?"
	if err := plan.Check(d, row("SEARCH items USING INTEGER PRIMARY KEY (rowid=?)")); err == nil {
		t.Fatal("lint waiver matched changed SQL")
	}
}

func TestPlanBaselineWaiverInventoryIsEmpty(t *testing.T) {
	_, r, _, _ := fixture(t)
	check := func(r *stmt.Registry) {
		for _, entry := range r.Manifest() {
			if len(entry.Plan.Waivers) != 0 {
				t.Fatalf("baseline statement %s needs explicit lead waiver approval: %+v", entry.ID, entry.Plan.Waivers)
			}
		}
	}
	check(r)
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	check(canonicalRegistry(t, policy))
}

func TestSQLAllowListDepthBoundary(t *testing.T) {
	scalar := "key"
	for depth := 1; depth <= 17; depth++ {
		scalar = "coalesce(" + scalar + ",0)"
		sql := "SELECT id FROM items WHERE " + scalar + ">?"
		issues := plan.LintSQL(sql)
		if depth <= 16 && len(issues) != 0 {
			t.Fatalf("known function depth %d flagged: %v", depth, issues)
		}
		if depth == 17 && len(issues) == 0 {
			t.Fatal("function nesting outside closed bound allowed")
		}
	}
}

func TestPlanSQLiteSubqueryGuardCannotBeWaivedByLint(t *testing.T) {
	conn, r, f, _ := fixture(t)
	statement, _ := r.Lookup("sort.tail")
	d, _ := statement.Definition()
	d.ID = "synthetic.native-guard"
	d.SQL = "SELECT id,key FROM items INDEXED BY item_sort WHERE library_id=? AND kind=? AND key > (SELECT key FROM items INDEXED BY item_sort WHERE library_id=1 AND " + predicate + " LIMIT 1) AND key=? AND id>? AND " + predicate + " ORDER BY key,id LIMIT ?"
	d.Plan.Waivers = []stmt.Waiver{
		{StatementID: d.ID, Stage: "sql-lint", Form: "sql:SELECT", Match: d.SQL, Reason: "Synthetic test-only outside-grammar waiver; no native permission"},
		{StatementID: d.ID, Stage: "sql-lint", Form: "sql:outside-grammar", Match: d.SQL, Reason: "Synthetic test-only subquery syntax; native guard must still refuse"},
	}
	registry, err := stmt.New([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Capture(context.Background(), conn, registry, f, indexPolicy(t))
	if err == nil || !strings.Contains(err.Error(), "forbidden plan SUBQUERY") {
		t.Fatalf("native SQLite guard was bypassed: %v", err)
	}
}

func TestSQLAllowListMalformedInputsFailClosed(t *testing.T) {
	for _, sql := range []string{
		"SELECT id FROM items WHERE key='unterminated",
		"SELECT id FROM items WHERE \"key=?",
		"SELECT id FROM items WHERE [key=?",
		"SELECT id FROM items WHERE `key=?",
		"SELECT id FROM items WHERE key=? /*",
		"SELECT id FROM items WHERE (key=?",
		"SELECT id FROM items WHERE key=?)",
		"SELECT id FROM items WHERE CASE key WHEN 1 THEN 0",
		"SELECT id FROM items WHERE key IN(?,?",
		"SELECT coalesce(key,0 FROM items WHERE id=?",
		"SELECT coalesce(key,0)) FROM items WHERE id=?",
		"SELECT DISTINCT,key FROM items", "SELECT CURRENT_TIME FROM items",
		"SELECT id FROM items WHERE key='nul\x00value'",
	} {
		if len(plan.LintSQL(sql)) == 0 {
			t.Fatalf("malformed/unknown SQL allowed: %q", sql)
		}
	}
}
