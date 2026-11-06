package plan_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func reviewDefinition(sql string, bindings []any, contract stmt.Plan) stmt.Definition {
	d := stmt.Definition{ID: "review.r2", SQL: sql, Owner: "test", CallSites: []string{"review_r2_test.go"}, Kind: stmt.Write, Plan: contract, Cohorts: []stmt.Cohort{{Name: "one", Bindings: bindings}}}
	for i, b := range bindings {
		kind := stmt.Integer
		switch b.(type) {
		case string:
			kind = stmt.Text
		case []byte:
			kind = stmt.Blob
		}
		d.Params = append(d.Params, stmt.Param{Name: strings.Repeat("p", i+1), Type: kind})
	}
	return d
}

func TestPlanRejectsEveryUserTrigger(t *testing.T) {
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		for _, body := range []string{"DELETE FROM review_side;", "UPDATE review_side SET value=(SELECT key FROM items LIMIT 1);", "INSERT INTO review_side SELECT id,key FROM items;"} {
			t.Run(event+body, func(t *testing.T) {
				conn, _, f, _ := fixture(t)
				ctx := context.Background()
				for _, sql := range []string{"CREATE TABLE review_items(id INTEGER PRIMARY KEY,value INTEGER)", "CREATE TABLE review_side(id INTEGER PRIMARY KEY,value INTEGER)", "CREATE TRIGGER review_trigger AFTER " + event + " ON review_items BEGIN " + body + " END"} {
					if _, err := conn.ExecContext(ctx, sql); err != nil {
						t.Fatal(err)
					}
				}
				sql := "INSERT INTO review_items(id,value) VALUES(?,?)"
				args := []any{int64(1), int64(2)}
				contract := stmt.Plan{Hot: true}
				if event == "UPDATE" {
					sql = "UPDATE review_items SET value=? WHERE id=?"
					contract.PrimaryKeys = []string{"review_items"}
				}
				if event == "DELETE" {
					sql = "DELETE FROM review_items WHERE id=?"
					args = args[:1]
					contract.PrimaryKeys = []string{"review_items"}
				}
				r, err := stmt.Describe([]stmt.Definition{reviewDefinition(sql, args, contract)})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := plan.Capture(ctx, conn, r, f, indexPolicy(t)); err == nil || !strings.Contains(err.Error(), "trigger forbidden") {
					t.Fatalf("trigger work qualified: %v", err)
				}
				if _, err := plan.ForeignKeys(ctx, conn, f.ConnectionPolicy); err == nil {
					t.Fatal("standalone FK audit accepted trigger")
				}
			})
		}
	}
	for _, scope := range []string{"main", "temp", "aux"} {
		t.Run("unreachable-"+scope, func(t *testing.T) {
			conn, r, f, _ := fixture(t)
			ctx := context.Background()
			if scope == "aux" {
				if _, err := conn.ExecContext(ctx, "ATTACH DATABASE ? AS aux", filepath.Join(t.TempDir(), "aux.db")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := conn.ExecContext(ctx, "CREATE TABLE "+scope+".review_side(id INTEGER PRIMARY KEY)"); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(ctx, "CREATE TRIGGER "+scope+".review_trigger AFTER INSERT ON review_side BEGIN DELETE FROM review_side; END"); err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Capture(ctx, conn, r, f, indexPolicy(t)); err == nil || !strings.Contains(err.Error(), "trigger forbidden") {
				t.Fatalf("unreachable %s trigger qualified: %v", scope, err)
			}
		})
	}
}

func TestPlanDMLSyntaxCannotBeWaived(t *testing.T) {
	for _, sql := range []string{
		"UPDATE items SET key=? WHERE id=? RETURNING key+1",
		"UPDATE items SET key=? WHERE id=? RETURNING abs(key)",
		"DELETE FROM items WHERE id=? RETURNING 1",
		"UPDATE items SET key=rhs.value FROM review_rhs AS rhs WHERE items.id=? AND rhs.id=?",
	} {
		args := make([]any, strings.Count(sql, "?"))
		for i := range args {
			args[i] = int64(1)
		}
		d := reviewDefinition(sql, args, stmt.Plan{PrimaryKeys: []string{"items"}})
		if _, err := stmt.Describe([]stmt.Definition{d}); err != nil {
			t.Fatalf("invalid regression definition before waiver: %v", err)
		}
		for _, issue := range plan.LintSQL(sql) {
			d.Plan.Waivers = append(d.Plan.Waivers, stmt.Waiver{StatementID: d.ID, Stage: "sql-lint", Form: issue.Form, Match: sql, Reason: "Synthetic rejected metadata"})
		}
		if len(d.Plan.Waivers) == 0 {
			t.Fatal("counterexample entered grammar")
		}
		if _, err := stmt.Describe([]stmt.Definition{d}); err == nil || !strings.Contains(err.Error(), "only exact EQP waivers") {
			t.Fatalf("DML lint waiver accepted or rejected for unrelated reason: %v", err)
		}
		eqp := plan.Rows{Columns: []string{"id", "parent", "notused", "detail"}, Values: [][]any{{int64(1), int64(0), int64(0), "SEARCH items USING INTEGER PRIMARY KEY (rowid=?)"}}}
		if err := plan.Check(d, eqp); err == nil {
			t.Fatal("direct Check honored lint waiver")
		}
	}
}

func TestPlanDMLIdentifiersResolveActualColumns(t *testing.T) {
	conn, _, f, _ := fixture(t)
	ctx := context.Background()
	for _, sql := range []string{`CREATE TABLE review_items(id INTEGER PRIMARY KEY,value INTEGER,"quoted col" TEXT)`, `CREATE TABLE review_without(id INTEGER PRIMARY KEY,value INTEGER) WITHOUT ROWID`} {
		if _, err := conn.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	capture := func(sql string, args []any, table string, insert bool) error {
		contract := stmt.Plan{Hot: true}
		if !insert {
			contract.PrimaryKeys = []string{table}
		}
		r, err := stmt.Describe([]stmt.Definition{reviewDefinition(sql, args, contract)})
		if err != nil {
			return err
		}
		_, err = plan.Capture(ctx, conn, r, f, indexPolicy(t))
		return err
	}
	for _, tc := range []struct {
		sql    string
		args   []any
		insert bool
	}{
		{`INSERT INTO review_items(id,value) VALUES(?,?) RETURNING "missing"`, []any{int64(1), int64(2)}, true},
		{`UPDATE review_items SET value=? WHERE id=? RETURNING "missing"`, []any{int64(1), int64(2)}, false},
		{`DELETE FROM review_items WHERE id=? RETURNING "missing"`, []any{int64(1)}, false},
		{`UPDATE review_items SET value="missing" WHERE id=?`, []any{int64(1)}, false},
		{`UPDATE review_items SET "missing"=? WHERE id=?`, []any{int64(1), int64(2)}, false},
		{`UPDATE review_items SET value=? WHERE id="missing"`, []any{int64(1)}, false},
		{`DELETE FROM review_items WHERE "missing"=?`, []any{int64(1)}, false},
		{`UPDATE review_items SET value=coalesce("missing",0) WHERE id=?`, []any{int64(1)}, false},
		{`UPDATE review_items SET value=other.value WHERE id=?`, []any{int64(1)}, false},
	} {
		if err := capture(tc.sql, tc.args, "review_items", tc.insert); err == nil || !strings.Contains(err.Error(), "column") {
			t.Fatalf("unknown DML identifier qualified: %s: %v", tc.sql, err)
		}
	}
	for _, sql := range []string{
		`UPDATE review_items SET "value"=? WHERE "id"=? RETURNING "quoted col",rowid`,
		`UPDATE review_items SET value=coalesce(review_items.value,0)+? WHERE main.review_items.id=? RETURNING id`,
	} {
		if err := capture(sql, []any{int64(1), int64(2)}, "review_items", false); err != nil {
			t.Fatalf("valid column rejected: %s: %v", sql, err)
		}
	}
	if err := capture(`INSERT INTO review_without(id,value) VALUES(?,?) RETURNING "rowid"`, []any{int64(1), int64(2)}, "review_without", true); err == nil {
		t.Fatal("WITHOUT ROWID accepted nonexistent rowid")
	}
}

func TestPlanConflictTargetMustResolveUniquely(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		conn, _, f, _ := fixture(t)
		ctx := context.Background()
		if _, err := conn.ExecContext(ctx, "CREATE TABLE review_conflict(code TEXT COLLATE NOCASE,value INTEGER)"); err != nil {
			t.Fatal(err)
		}
		indexes := []string{"CREATE UNIQUE INDEX review_nocase ON review_conflict(code COLLATE NOCASE)", "CREATE UNIQUE INDEX review_binary ON review_conflict(code COLLATE BINARY)"}
		if reverse {
			indexes[0], indexes[1] = indexes[1], indexes[0]
		}
		for _, sql := range indexes {
			if _, err := conn.ExecContext(ctx, sql); err != nil {
				t.Fatal(err)
			}
		}
		for _, index := range []string{"review_nocase", "review_binary"} {
			d := reviewDefinition("INSERT INTO review_conflict(code,value) VALUES(?,?) ON CONFLICT(code) DO NOTHING", []any{"A", int64(1)}, stmt.Plan{Hot: true, Indexes: []string{index}})
			r, err := stmt.Describe([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Capture(ctx, conn, r, f, indexPolicy(t)); err == nil || !strings.Contains(err.Error(), "matches=2") {
				t.Fatalf("ambiguous target certified: %v", err)
			}
		}
	}
	for _, ddl := range []string{
		"CREATE TABLE review_duplicate(code TEXT,value INTEGER);CREATE UNIQUE INDEX review_a ON review_duplicate(code);CREATE UNIQUE INDEX review_b ON review_duplicate(code)",
		"CREATE TABLE review_duplicate(code INTEGER PRIMARY KEY,value INTEGER);CREATE UNIQUE INDEX review_a ON review_duplicate(code)",
	} {
		conn, _, f, _ := fixture(t)
		ctx := context.Background()
		if _, err := conn.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
		d := reviewDefinition("INSERT INTO review_duplicate(code,value) VALUES(?,?) ON CONFLICT(code) DO NOTHING", []any{int64(1), int64(2)}, stmt.Plan{Hot: true, Indexes: []string{"review_a"}})
		r, err := stmt.Describe([]stmt.Definition{d})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plan.Capture(ctx, conn, r, f, indexPolicy(t)); err == nil || !strings.Contains(err.Error(), "matches=2") {
			t.Fatalf("duplicate UNIQUE/PK target certified: %v", err)
		}
	}
	for _, tc := range []struct {
		ddl, sql, index, pk string
		args                []any
	}{
		{"CREATE TABLE review_target(code TEXT COLLATE NOCASE,value INTEGER);CREATE UNIQUE INDEX review_key ON review_target(code COLLATE BINARY)", "INSERT INTO review_target(code,value) VALUES(?,?) ON CONFLICT(code) DO NOTHING", "review_key", "", []any{"A", int64(1)}},
		{"CREATE TABLE review_target(code TEXT,value INTEGER);CREATE UNIQUE INDEX review_key ON review_target(code COLLATE NOCASE)", "INSERT INTO review_target(code,value) VALUES(?,?) ON CONFLICT(code) DO NOTHING", "review_key", "", []any{"A", int64(1)}},
		{"CREATE TABLE review_target(a INTEGER,b INTEGER,value INTEGER);CREATE UNIQUE INDEX review_key ON review_target(a,b)", "INSERT INTO review_target(a,b,value) VALUES(?,?,?) ON CONFLICT(b,a) DO UPDATE SET value=excluded.value", "review_key", "", []any{int64(1), int64(2), int64(3)}},
		{"CREATE TABLE review_target(a INTEGER,b INTEGER,value INTEGER,PRIMARY KEY(a,b)) WITHOUT ROWID", "INSERT INTO review_target(a,b,value) VALUES(?,?,?) ON CONFLICT(b,a) DO NOTHING", "", "review_target", []any{int64(1), int64(2), int64(3)}},
		{"CREATE TABLE review_target(code TEXT,value INTEGER);CREATE UNIQUE INDEX review_key ON review_target(code);CREATE UNIQUE INDEX review_partial ON review_target(code) WHERE value>0", "INSERT INTO review_target(code,value) VALUES(?,?) ON CONFLICT(code) DO NOTHING", "review_key", "", []any{"A", int64(1)}},
		{"CREATE TABLE review_target(id INTEGER PRIMARY KEY,value INTEGER)", "INSERT INTO review_target(id,value) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET value=?", "", "review_target", []any{int64(1), int64(2), int64(3)}},
		{"CREATE TABLE review_target(code TEXT COLLATE NOCASE PRIMARY KEY,value INTEGER)", "INSERT INTO review_target(code,value) VALUES(?,?) ON CONFLICT(code) DO NOTHING", "sqlite_autoindex_review_target_1", "", []any{"A", int64(1)}},
	} {
		conn, _, f, _ := fixture(t)
		ctx := context.Background()
		if _, err := conn.ExecContext(ctx, tc.ddl); err != nil {
			t.Fatal(err)
		}
		contract := stmt.Plan{Hot: true}
		if tc.index != "" {
			contract.Indexes = []string{tc.index}
		} else {
			contract.PrimaryKeys = []string{tc.pk}
		}
		r, err := stmt.Describe([]stmt.Definition{reviewDefinition(tc.sql, tc.args, contract)})
		if err != nil {
			t.Fatal(err)
		}
		policy := indexPolicy(t)
		if strings.Contains(tc.ddl, "review_partial") {
			policy, err = plan.ReadIndexPolicy([]byte(`{"indexes":[{"index":"item_sort","predicate":"kind IN(1,2,3,4,10)","choice":"repeat_exact_predicate"},{"index":"review_partial","predicate":"value>0","choice":"repeat_exact_predicate"}]}`))
			if err != nil {
				t.Fatal(err)
			}
		}
		g, err := plan.Capture(ctx, conn, r, f, policy)
		if err != nil {
			t.Fatalf("unique target failed: %s: %v", tc.sql, err)
		}
		if g.Plans[0].Insert.Conflict == nil {
			t.Fatal("resolved target evidence missing")
		}
		if tc.index != "" && g.Plans[0].Insert.Conflict.Index != tc.index {
			t.Fatalf("certified wrong index: %s", g.Plans[0].Insert.Conflict.Index)
		}
		if strings.Contains(tc.ddl, "code COLLATE BINARY") && !strings.Contains(g.Plans[0].Insert.Conflict.SQL, "code COLLATE BINARY=?") || strings.Contains(tc.ddl, "code COLLATE NOCASE") && !strings.Contains(g.Plans[0].Insert.Conflict.SQL, "code COLLATE NOCASE=?") {
			t.Fatalf("probe lost conflict-index collation: %s", g.Plans[0].Insert.Conflict.SQL)
		}
	}
}
