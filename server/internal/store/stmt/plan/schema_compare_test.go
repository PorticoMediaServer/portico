package plan

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

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
}
func TestPlanSchemaRecordWidthMappingIsExact(t *testing.T) {
	mk := func(ddl string, width int64) (map[string]any, Rows) {
		return map[string]any{"plans": []any{map[string]any{"explain": map[string]any{"values": []any{[]any{json.Number("0"), "OpenRead", json.Number("1"), `"main"."t"`, json.Number("0"), json.Number(strconv.FormatInt(width, 10)), json.Number("0"), nil}}}}}}, Rows{Columns: []string{"type", "name", "tbl_name", "rootpage", "sql"}, Values: [][]any{{"table", "t", "t", int64(2), ddl}}}
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
