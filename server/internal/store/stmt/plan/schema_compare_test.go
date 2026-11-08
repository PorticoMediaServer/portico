package plan

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/schematest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

var comparisonCatalog struct {
	once sync.Once
	rows Rows
	err  error
}

// Comparison controls keep historical statement bytes, but their actual side
// must use the current native catalog. Rebinding the old catalog to a new SQL
// hash would make every control fail before reaching the field it tests.
func currentComparisonCatalog(t *testing.T) Rows {
	t.Helper()
	comparisonCatalog.once.Do(func() {
		policy := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 4096, Synchronous: sqliteconn.Full}
		db, _, err := sqliteconn.Open(t.Context(), schematest.Clone(t), policy)
		if err != nil {
			comparisonCatalog.err = err
			return
		}
		defer db.Close()
		conn, err := db.Conn(t.Context())
		if err != nil {
			comparisonCatalog.err = err
			return
		}
		defer conn.Close()
		if _, err = sqliteconn.Assert(t.Context(), conn, policy); err != nil {
			comparisonCatalog.err = err
			return
		}
		comparisonCatalog.rows, comparisonCatalog.err = query(t.Context(), conn, "SELECT type,name,tbl_name,rootpage,sql FROM main.sqlite_schema ORDER BY type,name")
		if comparisonCatalog.err == nil {
			comparisonCatalog.err = checkEmbeddedCatalog(comparisonCatalog.rows)
		}
	})
	if comparisonCatalog.err != nil {
		t.Fatal(comparisonCatalog.err)
	}
	return comparisonCatalog.rows
}

func canonicalComparisonFixture(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("testdata/canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err = decodeValue(json.RawMessage(data), &v); err != nil {
		t.Fatal(err)
	}
	env := v["environment"].(map[string]any)
	catalog := currentComparisonCatalog(t)
	var current any
	if err = decodeValue(catalog, &current); err != nil {
		t.Fatal(err)
	}
	env["schema"] = current
	env["schemaSha256"] = digest(catalog)
	env["indexPolicySha256"] = embeddedPolicySHA()
	env["schemaBinding"] = map[string]any{"version": json.Number("1"), "sqlSha256": schemaSQLSHA()}
	return v
}
func cloneComparison(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	var out map[string]any
	if e := decodeValue(v, &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func comparisonBytes(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestPlanSchemaEnvironmentIsCheckedNotPinnedPerStatement(t *testing.T) {
	actual := canonicalComparisonFixture(t)
	expected := cloneComparison(t, actual)
	env := expected["environment"].(map[string]any)
	delete(env, "schemaBinding")
	env["schemaSha256"] = "old reviewed schema"
	env["indexPolicySha256"] = "old policy"
	env["statisticsSha256"] = "old statistics"
	env["fixture"].(map[string]any)["sha256"] = "old journal"
	env["fixture"].(map[string]any)["implementations"] = "old source description"
	// A historical index, root and empty statistics entry need no recapture when
	// the actual schema matches the single newly reviewed schema baseline.
	schemaRows := env["schema"].(map[string]any)
	rows := schemaRows["values"].([]any)
	schemaRows["values"] = rows[1:]
	if e := CompareJSON(comparisonBytes(t, expected), comparisonBytes(t, actual)); e != nil {
		t.Fatal(e)
	}
	wrapped := func(v any) []byte {
		return comparisonBytes(t, []any{map[string]any{"Profile": "unanalyzed", "Golden": v}})
	}
	if e := CompareJSON(wrapped(expected), wrapped(actual)); e != nil {
		t.Fatal(e)
	}
}
func TestPlanSchemaComparisonNeverHidesPlanOrEnvironmentCorruption(t *testing.T) {
	base := canonicalComparisonFixture(t)
	for name, mutate := range map[string]func(map[string]any){
		"opcode": func(g map[string]any) {
			g["plans"].([]any)[0].(map[string]any)["explain"].(map[string]any)["values"].([]any)[0].([]any)[1] = "UnknownOpcode"
		},
		"operand": func(g map[string]any) {
			g["plans"].([]any)[0].(map[string]any)["explain"].(map[string]any)["values"].([]any)[0].([]any)[3] = json.Number("999")
		},
		"search": func(g map[string]any) {
			g["plans"].([]any)[0].(map[string]any)["queryPlan"].(map[string]any)["values"].([]any)[0].([]any)[3] = "SCAN changed"
		},
		"manifest":       func(g map[string]any) { g["manifest"].([]any)[0].(map[string]any)["sql"] = "SELECT other FROM another" },
		"missingBinding": func(g map[string]any) { delete(g["environment"].(map[string]any), "schemaBinding") },
		"schemaDigest":   func(g map[string]any) { g["environment"].(map[string]any)["schemaSha256"] = "wrong" },
		"policyDigest":   func(g map[string]any) { g["environment"].(map[string]any)["indexPolicySha256"] = "wrong" },
		"schemaDDL": func(g map[string]any) {
			env := g["environment"].(map[string]any)
			s := env["schema"].(map[string]any)
			s["values"].([]any)[0].([]any)[4] = "changed"
			env["schemaSha256"] = digest(s)
		},
		"statisticsDigest": func(g map[string]any) { g["environment"].(map[string]any)["statisticsSha256"] = "wrong" },
		"engine": func(g map[string]any) {
			g["environment"].(map[string]any)["engine"].(map[string]any)["values"].([]any)[0].([]any)[0] = "unknown"
		},
		"connection": func(g map[string]any) {
			g["environment"].(map[string]any)["connection"].(map[string]any)["TempStore"] = json.Number("2")
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := cloneComparison(t, base)
			mutate(g)
			if e := CompareJSON(comparisonBytes(t, base), comparisonBytes(t, g)); e == nil {
				t.Fatal("changed plan/unchecked environment accepted")
			}
		})
	}
	// Equal JSON is NOT an exemption from actual schema consistency checks.
	bad := cloneComparison(t, base)
	bad["environment"].(map[string]any)["schemaSha256"] = "forged"
	if e := CompareJSON(comparisonBytes(t, bad), comparisonBytes(t, bad)); e == nil {
		t.Fatal("identical forged evidence accepted")
	}
}
func TestPlanSchemaAuditsUseOneCurrentReference(t *testing.T) {
	ref, e := sharedSchemaEvidence()
	if e != nil {
		t.Fatal(e)
	}
	base := canonicalComparisonFixture(t)
	table := "catalog_libraries"
	s := ref.Tables[table]
	var insert any
	if e = decodeValue(InsertAudit{Table: table, Columns: s.Columns, Indexes: s.Indexes, IndexColumns: s.IndexColumns}, &insert); e != nil {
		t.Fatal(e)
	}
	base["plans"].([]any)[0].(map[string]any)["insert"] = insert
	expected := cloneComparison(t, base)
	expected["plans"].([]any)[0].(map[string]any)["insert"].(map[string]any)["indexes"] = nil
	if e = CompareJSON(comparisonBytes(t, expected), comparisonBytes(t, base)); e != nil {
		t.Fatal(e)
	}
	bad := cloneComparison(t, base)
	bad["plans"].([]any)[0].(map[string]any)["insert"].(map[string]any)["columns"] = nil
	if e = CompareJSON(comparisonBytes(t, expected), comparisonBytes(t, bad)); e == nil {
		t.Fatal("forged current INSERT audit accepted")
	}
	current := ForeignKeyAudit{SchemaBinding: &SchemaBinding{1, schemaSQLSHA()}, Plans: ref.ForeignKeys}
	old := ForeignKeyAudit{Plans: ref.ForeignKeys[:1]}
	if e = CompareExactJSON(comparisonBytes(t, old), comparisonBytes(t, current)); e != nil {
		t.Fatal(e)
	}
	var f ForeignKeyAudit
	if e = decodeValue(current, &f); e != nil {
		t.Fatal(e)
	}
	f.Plans[0].Delete.Values[0][3] = "SCAN wrong"
	if e = CompareExactJSON(comparisonBytes(t, old), comparisonBytes(t, f)); e == nil {
		t.Fatal("changed FK enforcement accepted")
	}
	forged := comparisonBytes(t, f)
	if e = CompareExactJSON(forged, forged); e == nil {
		t.Fatal("identical forged FK enforcement bypassed shared validation")
	}
	if e = CompareJSON(forged, forged); e == nil {
		t.Fatal("identical forged FK enforcement accepted by ordinary comparison")
	}

}
func TestPlanSchemaRecordWidthMappingIsExact(t *testing.T) {
	mk := func(ddl string, width any) (map[string]any, Rows) {
		operand := width
		if n, ok := width.(int); ok {
			operand = json.Number(strconv.Itoa(n))
		}
		return map[string]any{"plans": []any{map[string]any{"explain": map[string]any{"values": []any{[]any{json.Number("0"), "OpenRead", json.Number("1"), `"main"."t"`, json.Number("0"), operand, json.Number("0"), nil}}}}}}, Rows{Columns: []string{"type", "name", "tbl_name", "rootpage", "sql"}, Values: [][]any{{"table", "t", "t", int64(2), ddl}}}
	}

	a, sa := mk("CREATE TABLE t(a INTEGER,b TEXT)", 2)
	b, sb := mk("CREATE TABLE t(a INTEGER,b TEXT,c BLOB)", 3)
	normalizeRecordWidths(a, b, sa, sb)
	if !jsonEqual(a, b) {
		t.Fatal("full native widths were not schema-derived")
	}
	original, so := mk("CREATE TABLE t(a INTEGER,b TEXT)", 2)
	wrong, sw := mk("CREATE TABLE t(a INTEGER,b TEXT,c BLOB)", 1)
	normalizeRecordWidths(original, wrong, so, sw)
	if jsonEqual(original, wrong) {
		t.Fatal("changed decoder prefix erased")
	}
	unchanged, su := mk("CREATE TABLE t(a INTEGER,b TEXT,c BLOB)", 2)
	normalizeRecordWidths(original, unchanged, so, su)
	if !jsonEqual(original, unchanged) {
		t.Fatal("unchanged decoder prefix rejected")
	}
	generated, sg := mk("CREATE TABLE t(a INTEGER,b TEXT,c TEXT GENERATED ALWAYS AS(a) VIRTUAL)", 3)
	normalizeRecordWidths(original, generated, so, sg)
	if jsonEqual(original, generated) {
		t.Fatal("unsupported generated layout mapped")
	}
	for _, ddl := range []string{"CREATE TABLE t(a INTEGER,b TEXT,c TEXT AS(a) STORED)", "CREATE TABLE t(a INTEGER,b TEXT,c TEXT AS(a) VIRTUAL)"} {
		old, oldSchema := mk("CREATE TABLE t(a INTEGER,b TEXT)", 2)
		short, newSchema := mk(ddl, 3)
		normalizeRecordWidths(old, short, oldSchema, newSchema)
		if jsonEqual(old, short) {
			t.Fatal("shorthand generated layout mapped", ddl)
		}
	}
	// This actual native P4 is retained by the canonical capture; its complete
	// native VM is independently default-checked on every canonical test run.
	g := canonicalComparisonFixture(t)
	var native any
	for _, p := range g["plans"].([]any) {
		for _, r := range p.(map[string]any)["explain"].(map[string]any)["values"].([]any) {
			row := r.([]any)
			if row[1] == "OpenRead" && row[3] == `"main"."catalog_assets"` {
				native = row[5]
				break
			}
		}
	}
	if native != "32" {
		t.Fatalf("native P4 fixture changed: %T %v", native, native)
	}
	ddl := func(n int) string {
		cols := make([]string, n)
		for i := range cols {
			cols[i] = fmt.Sprintf("c%d TEXT", i)
		}
		return "CREATE TABLE t(" + strings.Join(cols, ",") + ")"
	}
	old, oldSchema := mk(ddl(31), "31")
	next, newSchema := mk(ddl(32), native)
	normalizeRecordWidths(old, next, oldSchema, newSchema)
	if !jsonEqual(old, next) {
		t.Fatal("native decimal P4 widths refused")
	}
	for _, bad := range []string{"031", " 31", "+31", "-1", "31.0", "k(31,)", "9223372036854775808"} {
		if _, ok := recordWidthValue(bad); ok {
			t.Fatal("noncanonical P4 width admitted", bad)
		}
	}

}

func TestPlanSchemaCompactSnapshotsKeepTheirPlans(t *testing.T) {
	g := canonicalComparisonFixture(t)
	compact := map[string]any{"Fixture": g["environment"].(map[string]any)["fixture"], "Connection": g["environment"].(map[string]any)["connection"], "Schema": "old schema", "Policy": "old policy", "Statistics": "old stats", "Manifest": g["manifest"], "Plans": g["plans"]}
	actual := cloneComparison(t, compact)
	actual["environment"] = g["environment"]
	actual["Schema"] = "current schema"
	if e := CompareJSON(comparisonBytes(t, compact), comparisonBytes(t, actual)); e != nil {
		t.Fatal(e)
	}
	actual["Plans"].([]any)[0].(map[string]any)["queryPlan"].(map[string]any)["values"].([]any)[0].([]any)[3] = "SCAN wrong"
	if e := CompareJSON(comparisonBytes(t, compact), comparisonBytes(t, actual)); e == nil {
		t.Fatal("compact snapshot plan erased")
	}
}
