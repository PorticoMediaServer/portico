package plan_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestSQLJoinClosedGrammar(t *testing.T) {
	allowed, flagged := 0, 0
	for _, kind := range []string{"INNER", "LEFT", "CROSS"} {
		for _, hint := range []string{"", " INDEXED BY child_lookup", " NOT INDEXED"} {
			for _, on := range []string{
				"b.id=a.id", "b.id=?", "b.id=1", "b.id=-1", "b.id=+1",
				"b.id=a.id AND b.library_id=? AND b.kind=1",
				`"b"."id"=main.a.id`,
			} {
				sql := "SELECT a.id,b.id FROM main.a AS a NOT INDEXED " + kind + " JOIN main.b AS b" + hint + " ON " + on + " WHERE a.id=? AND b.id IS NOT NULL ORDER BY a.id LIMIT ?"
				if issues := plan.LintSQL(sql); len(issues) != 0 {
					t.Fatalf("allowed join flagged: %v\n%s", issues, sql)
				}
				allowed++
			}
		}
	}
	for _, sql := range []string{
		"SELECT a.id FROM a INNER JOIN b ON b.id=a.id LEFT JOIN c AS c NOT INDEXED ON c.id=b.id WHERE a.id=?",
		"SELECT a.id FROM a AS a INDEXED BY ia CROSS JOIN b AS b INDEXED BY ib ON b.id=a.id CROSS JOIN c AS c ON c.id=b.id AND c.kind=1",
		"SELECT a.id FROM a AS a /* JOIN (SELECT) */ LEFT JOIN b AS b ON b.id=a.id AND b.kind=1 WHERE a.title='JOIN (SELECT CASE)'",
	} {
		if issues := plan.LintSQL(sql); len(issues) != 0 {
			t.Fatalf("allowed chain flagged: %v\n%s", issues, sql)
		}
		allowed++
	}

	for _, source := range []string{
		"a AS a,b AS b", "a AS a JOIN b AS b ON b.id=a.id",
		"a AS a NATURAL JOIN b AS b", "a AS a RIGHT JOIN b AS b ON b.id=a.id",
		"a AS a FULL JOIN b AS b ON b.id=a.id", "a AS a LEFT OUTER JOIN b AS b ON b.id=a.id",
		"a AS a INNER JOIN b AS b USING(id)", "a AS a CROSS JOIN b AS b",
		"a AS a INNER JOIN b AS b", "a AS a LEFT JOIN b AS b ON",
		"a a INNER JOIN b AS b ON b.id=a.id", "a AS a INNER JOIN b b ON b.id=a.id",
		"(a AS a INNER JOIN b AS b ON b.id=a.id)", "a AS a INNER JOIN (SELECT id FROM b) AS b ON b.id=a.id",
		"a AS a INNER JOIN json_each(?) AS b ON b.id=a.id",
		"a AS a INNER JOIN b AS b ON b.id=a.id INDEXED BY ib",
		"a AS a INNER JOIN b AS b INDEXED BY ib NOT INDEXED ON b.id=a.id",
		"a AS a INNER JOIN b AS b NOT INDEXED INDEXED BY ib ON b.id=a.id",
		"a AS a INNER JOIN b AS b INDEXED ON b.id=a.id",
	} {
		if len(plan.LintSQL("SELECT a.id FROM "+source+" WHERE a.id=?")) == 0 {
			t.Fatalf("outside join grammar accepted: %s", source)
		}
		flagged++
	}
	for _, condition := range []string{
		"b.id COLLATE NOCASE=a.id", "b.id=a.id COLLATE BINARY",
		"b.id>a.id", "b.id==a.id", "b.id!=a.id", "b.id=a.id OR b.kind=1",
		"b.id=a.id AND", "(b.id=a.id)", "b.id=(a.id)", "(b.id,b.kind)=(a.id,a.kind)",
		"b.id=(SELECT id FROM b)", "b.id=CASE a.id WHEN 1 THEN 1 END",
		"b.id IS NULL", "b.id IN(1,2)", "b.id BETWEEN 1 AND 2", "b.id='1'",
		"?=a.id", "1=a.id", "abs(b.id)=a.id", "b.id=abs(a.id)", "b.id=a.id+1",
		"b.id=1.5", "b.id=NULL", "b.id=a.id, c.id=a.id",
		"b.id LIKE a.id", "b.id=a.id WINDOW w AS()", "b.id=a.id UNION SELECT id FROM b",
	} {
		for _, kind := range []string{"INNER", "LEFT", "CROSS"} {
			sql := "SELECT a.id FROM a AS a " + kind + " JOIN b AS b ON " + condition
			if len(plan.LintSQL(sql)) == 0 {
				t.Fatalf("outside ON grammar accepted: %s", sql)
			}
			flagged++
		}
	}
	t.Logf("closed join grammar: %d positive / %d negative probes", allowed, flagged)
}

func TestPlanJoinGoldenAndNativeGuards(t *testing.T) {
	conn, _, f, _ := fixture(t)
	ctx := context.Background()
	seed := []string{
		"CREATE TABLE principals(id INTEGER PRIMARY KEY,library_id INTEGER NOT NULL)",
		"CREATE INDEX principal_library ON principals(library_id,id)",
		"INSERT INTO principals VALUES(11,1),(22,2)",
		"CREATE TABLE labels(item_id INTEGER PRIMARY KEY,value TEXT)",
		"INSERT INTO labels VALUES(2,'allowed')",
	}
	for _, sql := range seed {
		if _, err := conn.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256([]byte(f.SHA256 + "\n" + strings.Join(seed, "\n")))
	f.SHA256 = hex.EncodeToString(sum[:])
	var defs []stmt.Definition
	for _, kind := range []string{"INNER", "CROSS"} {
		defs = append(defs, stmt.Definition{
			ID: stmt.ID("join." + strings.ToLower(kind)), Owner: "plan-test", CallSites: []string{"join_test.go"}, Kind: stmt.Read,
			SQL:     "SELECT i.id,p.id,l.value FROM items AS i INDEXED BY item_sort " + kind + " JOIN principals AS p INDEXED BY principal_library ON p.library_id=i.library_id AND p.id=? LEFT JOIN labels AS l NOT INDEXED ON l.item_id=i.id WHERE i.library_id=? AND i.kind=? AND " + predicate + " AND i.key=? ORDER BY i.key,i.id LIMIT ?",
			Params:  []stmt.Param{{Name: "principal", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}, {Name: "kind", Type: stmt.Integer}, {Name: "key", Type: stmt.Integer}, {Name: "limit", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(11), int64(1), int64(1), int64(1080), int64(4)}}, {Name: "missing", Bindings: []any{int64(99), int64(1), int64(1), int64(1080), int64(4)}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"item_sort", "principal_library"}, PrimaryKeys: []string{"l"}},
		})
	}
	registry, err := stmt.New(defs)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(golden.Plans) != 4 {
		t.Fatal("missing joined cohort plans")
	}
	for _, captured := range golden.Plans {
		if len(captured.QueryPlan.Values) != 3 {
			t.Fatalf("%s: expected three indexed point/range accesses", captured.ID)
		}
		if captured.ID == "join.cross" {
			for at, table := range []string{"i", "p", "l"} {
				if !strings.HasPrefix(captured.QueryPlan.Values[at][3].(string), "SEARCH "+table+" USING ") {
					t.Fatalf("CROSS outer order changed: %v", captured.QueryPlan.Values)
				}
			}
		}
	}
	// Verify both the admitted predicates and LEFT's NULL extension natively.
	for _, d := range defs {
		rows, err := conn.QueryContext(ctx, d.SQL, d.Cohorts[0].Bindings...)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var id, principal int64
			var label any
			if err := rows.Scan(&id, &principal, &label); err != nil {
				t.Fatal(err)
			}
			count++
			if id != int64(count) || principal != 11 || count == 1 && label != nil || count == 2 && label != "allowed" {
				t.Fatalf("joined native result differs: %d %d %v", id, principal, label)
			}
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil || closeErr != nil || count != 2 {
			t.Fatalf("joined rows=%d err=%v close=%v", count, err, closeErr)
		}
		missing, err := conn.QueryContext(ctx, d.SQL, d.Cohorts[1].Bindings...)
		if err != nil {
			t.Fatal(err)
		}
		found := missing.Next()
		err = missing.Err()
		closeErr = missing.Close()
		if found || err != nil || closeErr != nil {
			t.Fatalf("missing principal produced a row: %v err=%v close=%v", found, err, closeErr)
		}
	}
	for name, sql := range map[string]string{
		"scan":      strings.Replace(defs[1].SQL, "i INDEXED BY item_sort", "i NOT INDEXED", 1),
		"temp_sort": strings.Replace(defs[1].SQL, "ORDER BY i.key,i.id", "ORDER BY l.value,i.id", 1),
	} {
		t.Run(name, func(t *testing.T) {
			d := defs[1]
			d.SQL = sql
			eqp := reviewRawEQP(t, conn, d.SQL, d.Cohorts[0].Bindings...)
			if err := plan.CheckEQP(d, eqp); err == nil || !strings.Contains(err.Error(), map[string]string{"scan": "SCAN", "temp_sort": "USE TEMP B-TREE"}[name]) {
				t.Fatalf("native %s guard failed: %v; %v", name, err, eqp.Values)
			}
		})
	}
	if _, err := conn.ExecContext(ctx, "DROP INDEX principal_library"); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t)); err == nil {
		t.Fatal("missing forced join index qualified")
	}
	if t.Failed() {
		t.FailNow()
	}
	path := filepath.Join("testdata", "joins.json")
	if *record {
		data, err := golden.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := golden.Compare(expected); err != nil {
		t.Fatal(err)
	}
}
