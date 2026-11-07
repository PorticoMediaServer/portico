package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
)

// schemaComparison projects only a Capture explicitly bound to the embedded
// schema. Small/custom-schema goldens keep the original exact comparison.
// Neither JSON nor this comparison can mint a Registry receipt.
func schemaComparison(expected, actual []byte) ([]byte, []byte, error) {
	decode := func(b []byte) (any, error) {
		var v any
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		e := d.Decode(&v)
		return v, e
	}
	want, err := decode(expected)
	if err != nil {
		return nil, nil, err
	}
	got, err := decode(actual)
	if err != nil {
		return nil, nil, err
	}
	changed, err := projectSchemaPairs(want, got)
	if err != nil {
		return nil, nil, err
	}
	if !changed {
		return expected, actual, nil
	}
	a, err := json.Marshal(want)
	if err != nil {
		return nil, nil, err
	}
	b, err := json.Marshal(got)
	return a, b, err
}
func projectSchemaPairs(want, got any) (bool, error) {
	switch actual := got.(type) {
	case map[string]any:
		expected, ok := want.(map[string]any)
		if !ok {
			return false, nil
		}
		env, _ := actual["environment"].(map[string]any)
		_, golden := actual["manifest"]
		if !golden {
			_, golden = actual["Manifest"]
		}
		binding, qualified := env["schemaBinding"]
		if golden && !qualified && env["indexPolicySha256"] == embeddedPolicySHA() {
			return false, fmt.Errorf("plan: canonical Capture lost its schema binding")
		}
		if golden && qualified {
			ref, err := sharedSchemaEvidence()
			if err != nil {
				return false, err
			}
			if err = checkBinding(binding); err != nil {
				return false, err
			}
			oldEnv, fullEnvironment := expected["environment"].(map[string]any)
			if !fullEnvironment {
				if _, compact := expected["Manifest"]; !compact {
					if _, compact = expected["manifest"]; !compact {
						return false, fmt.Errorf("plan: reviewed golden lacks environment")
					}
				}
				// Legacy compact baselines never contained these environment
				// fields. Keep their original connection and fixture profile.
				oldEnv = map[string]any{}
			}
			var e Environment
			if err = decodeValue(env, &e); err != nil {
				return false, err
			}
			if err = validateDerivedEnvironment(e); err != nil {
				return false, err
			}
			plans, ok := actual["plans"].([]any)
			if !ok {
				plans, ok = actual["Plans"].([]any)
			}
			if !ok {
				return false, fmt.Errorf("plan: missing actual plans")
			}
			for _, p := range plans {
				if err = validateSchemaAudits(p, ref); err != nil {
					return false, err
				}
			}
			if f, ok := actual["foreignKeys"]; ok {
				if err = validateSharedFK(f, ref); err != nil {
					return false, err
				}
			}
			if fullEnvironment {
				var oldSchema Rows
				if err = decodeValue(oldEnv["schema"], &oldSchema); err != nil {
					return false, err
				}
				normalizeRecordWidths(expected, oldSchema)
				normalizeRecordWidths(actual, e.Schema)
			}
			// Full native evidence remains in the report. Only its checked schema
			// component is removed from the statement's comparison key.
			for _, e := range []map[string]any{oldEnv, env} {
				for _, k := range []string{"schemaBinding", "schema", "schemaSha256", "indexPolicySha256", "statistics", "statisticsSha256"} {
					delete(e, k)
				}
				if fixture, ok := e["fixture"].(map[string]any); ok {
					delete(fixture, "sha256")
					delete(fixture, "implementations")
				}
			}
			for _, g := range []map[string]any{expected, actual} {
				plans, ok := g["plans"].([]any)
				if !ok {
					plans, ok = g["Plans"].([]any)
				}
				if ok {
					for _, p := range plans {
						projectSchemaAudit(p)
					}
				}
				delete(g, "foreignKeys")
				for _, k := range []string{"Schema", "Policy", "Statistics", "schemaSha256", "policySha256", "statisticsSha256"} {
					delete(g, k)
				}
				for _, key := range []string{"Fixture", "fixture"} {
					if f, ok := g[key].(map[string]any); ok {
						delete(f, "sha256")
						delete(f, "implementations")
					}
				}
				if !fullEnvironment {
					delete(g, "environment")
				}
			}
			return true, nil
		}
		// Standalone complete FK audits use the same one schema baseline, including
		// their native nodes and SEARCH details. They are never simply ignored.
		if binding, ok := actual["schemaBinding"]; ok {
			if _, fk := actual["connection"]; fk {
				if err := checkBinding(binding); err != nil {
					return false, err
				}
				ref, err := sharedSchemaEvidence()
				if err != nil {
					return false, err
				}
				if err = validateSharedFK(actual, ref); err != nil {
					return false, err
				}
				for _, g := range []map[string]any{expected, actual} {
					delete(g, "schemaBinding")
					delete(g, "plans")
				}
				return true, nil
			}
		}
		changed := false
		for k, v := range actual {
			c, err := projectSchemaPairs(expected[k], v)
			if err != nil {
				return false, err
			}
			changed = changed || c
		}
		return changed, nil
	case []any:
		before, ok := want.([]any)
		if !ok || len(before) != len(actual) {
			return false, nil
		}
		changed := false
		for i := range actual {
			c, e := projectSchemaPairs(before[i], actual[i])
			if e != nil {
				return false, e
			}
			changed = changed || c
		}
		return changed, nil
	}
	return false, nil
}
func decodeValue(v, out any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(out)
}
func checkBinding(v any) error {
	var b SchemaBinding
	if e := decodeValue(v, &b); e != nil {
		return e
	}
	if b.Version != 1 || b.SQLSHA256 != schemaSQLSHA() {
		return fmt.Errorf("plan: actual Capture schema binding is stale or invalid")
	}
	return nil
}
func validateDerivedEnvironment(e Environment) error {
	ref, err := sharedSchemaEvidence()
	if err != nil {
		return err
	}
	if !jsonEqual(e.Engine, ref.Engine) {
		return fmt.Errorf("plan: native engine/sourceID differs from shared evidence")
	}
	options, _ := json.Marshal(e.CompileOptions)
	profile, err := identifyCompileProfile(options)
	if err != nil || profile.platform != runtime.GOOS+"/"+runtime.GOARCH {
		return fmt.Errorf("plan: unapproved native compile profile")
	}

	if err := checkEmbeddedCatalog(e.Schema); err != nil {
		return err
	}
	if e.SchemaSHA256 != digest(e.Schema) || e.StatisticsSHA256 != digest(e.Statistics) || e.IndexPolicySHA256 != embeddedPolicySHA() {
		return fmt.Errorf("plan: schema/policy/statistics fingerprint inconsistent with current evidence")
	}
	if len(e.Fixture.SHA256) != 64 || e.Fixture.Implementations == "" {
		return fmt.Errorf("plan: missing current fixture journal identity")
	}
	tables := map[string]bool{}
	indexes := map[string]string{}
	for _, r := range e.Schema.Values {
		if r[0] == "table" {
			tables[r[1].(string)] = true
		}
		if r[0] == "index" {
			indexes[r[1].(string)] = r[2].(string)
		}
	}
	if e.Fixture.Statistics == Unanalyzed && len(e.Statistics) != 0 || e.Fixture.Statistics == Analyzed && len(e.Statistics) == 0 {
		return fmt.Errorf("plan: statistics profile inconsistent")
	}
	for name, rows := range e.Statistics {
		columns := []string{"tbl", "idx", "stat"}
		if name == "sqlite_stat4" {
			columns = []string{"tbl", "idx", "neq", "nlt", "ndlt", "sample"}
		}
		if !slices.Equal(rows.Columns, columns) {
			return fmt.Errorf("plan: statistics columns changed")
		}

		if name != "sqlite_stat1" && name != "sqlite_stat4" || !tables[name] {
			return fmt.Errorf("plan: unknown statistics table %s", name)
		}
		for _, r := range rows.Values {
			if len(r) != len(columns) {
				return fmt.Errorf("plan: invalid statistics row")
			}
			table, ok := r[0].(string)
			if !ok || !tables[table] {
				return fmt.Errorf("plan: statistics refer to unknown table")
			}
			if r[1] != nil {
				idx, ok := r[1].(string)
				if !ok || indexes[idx] != table {
					// WITHOUT ROWID primary-key statistics name their table, not an index.
					if !ok || idx != table {
						return fmt.Errorf("plan: statistics refer to unknown index")
					}
				}
			}
		}
	}
	return nil
}
func validateSchemaAudits(v any, ref *SchemaEvidence) error {
	p, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("plan: invalid statement plan")
	}
	if raw, ok := p["insert"]; ok {
		var a InsertAudit
		if err := decodeValue(raw, &a); err != nil {
			return err
		}
		t, exists := ref.Tables[a.Table]
		if !exists || !jsonEqual(a.Columns, t.Columns) || !jsonEqual(a.Indexes, t.Indexes) || !jsonEqual(a.IndexColumns, t.IndexColumns) {
			return fmt.Errorf("plan: INSERT destination metadata differs from current shared schema evidence")
		}
	}
	if raw, ok := p["autoIndexes"]; ok {
		var audits []AutoIndexAudit
		if err := decodeValue(raw, &audits); err != nil {
			return err
		}
		for _, a := range audits {
			t, exists := ref.Tables[a.Binding.Table]
			var index []any
			for _, r := range t.Indexes.Values {
				if len(r) == 5 && r[1] == a.Binding.Name {
					index = r
				}
			}
			if !exists || index == nil || a.WithoutRowid != t.WithoutRowid || !jsonEqual(a.TableColumns, t.Columns) || !jsonEqual(a.IndexColumns, t.IndexColumns[a.Binding.Name]) || !jsonEqual(a.KeyColumns, t.IndexKeys[a.Binding.Name]) || !jsonEqual(a.Index, Rows{Columns: t.Indexes.Columns, Values: [][]any{index}}) {
				return fmt.Errorf("plan: forced-key metadata differs from current shared schema evidence")
			}
		}
	}
	return nil
}
func projectSchemaAudit(v any) {
	p, ok := v.(map[string]any)
	if !ok {
		return
	}
	if a, ok := p["insert"].(map[string]any); ok {
		for _, k := range []string{"columns", "indexes", "indexColumns"} {
			delete(a, k)
		}
	}
	if audits, ok := p["autoIndexes"].([]any); ok {
		for _, v := range audits {
			if a, ok := v.(map[string]any); ok {
				for _, k := range []string{"index", "keyColumns", "indexColumns", "tableColumns"} {
					delete(a, k)
				}
			}
		}
	}
}
func validateSharedFK(v any, ref *SchemaEvidence) error {
	var f ForeignKeyAudit
	if err := decodeValue(v, &f); err != nil {
		return err
	}
	if !jsonEqual(f.Plans, ref.ForeignKeys) {
		return fmt.Errorf("plan: FK enforcement differs from the current shared schema evidence; review/regenerate the schema baseline once")
	}
	return nil
}

// Only a table's full physical record width is schema-derived. Decoder
// prefixes, index KeyInfo, registers, flags and every other operand stay exact.
// Conservatively leave generated/virtual declarations on the exact VM path.
func normalizeRecordWidths(g map[string]any, catalog Rows) {
	widths := map[string]int64{}
	for _, r := range catalog.Values {
		if len(r) != 5 || r[0] != "table" {
			continue
		}
		ddl, ok := r[4].(string)
		if !ok {
			continue
		}
		tokens, err := lintTokens(ddl)
		if err != nil || len(tokens) < 4 || !equalSQL(tokens[0].text, "CREATE") || !equalSQL(tokens[1].text, "TABLE") {
			continue
		}
		depth, count := 0, int64(0)
		start := false
		safe := true
		for _, t := range tokens {
			if equalSQL(t.text, "GENERATED") {
				safe = false
				break
			}
			if t.text == "(" {
				depth++
				if depth == 1 {
					start = true
				}
				continue
			}
			if t.text == ")" {
				depth--
				if depth == 0 {
					break
				}
				continue
			}
			if depth == 1 && t.text == "," {
				start = true
				continue
			}
			if depth == 1 && start {
				start = false
				if t.kind != 'w' && t.kind != 'q' {
					safe = false
					break
				}
				if t.kind == 'q' {
					count++
					continue
				}
				switch nameKey(t.text) {
				case "constraint", "primary", "unique", "foreign", "check":
				default:
					count++
				}
			}
		}
		if safe && count > 0 {
			widths[identifier("main")+"."+identifier(r[1].(string))] = count
		}
	}
	plans, _ := g["plans"].([]any)
	for _, p := range plans {
		plan, _ := p.(map[string]any)
		vm, _ := plan["explain"].(map[string]any)
		rows, _ := vm["values"].([]any)
		for _, r := range rows {
			row, ok := r.([]any)
			if !ok || len(row) != 8 || row[1] != "OpenRead" {
				continue
			}
			object, ok := row[3].(string)
			if !ok {
				continue
			}
			n, ok := integerValue(row[5])
			if ok && widths[object] > 0 && n == widths[object] {
				row[5] = "<schema-full-record-width>"
			}
		}
	}
}
func equalSQL(a, b string) bool { return foldName(a) == foldName(b) }
