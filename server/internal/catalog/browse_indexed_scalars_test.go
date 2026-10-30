package catalog

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// The indexed year, decade and dateAdded forms must select exactly what the
// field expressions select, over missing (0, NULL, empty), boundary and
// out-of-domain values, for every operator (B7).
func TestIndexedScalarsMatchFieldExpressions(t *testing.T) {
	c := catalogtest.Open(t)
	c.Exec(`CREATE TEMP TABLE scalar_truth(id INTEGER PRIMARY KEY,year INTEGER NOT NULL,added_text TEXT)`)
	years := []int{0, -5, 1, 1799, 1800, 1809, 1989, 1990, 1995, 1999, 2000, 2009, 2190, 2199, 2200, 3000}
	added := []any{nil, "", "2026-09-01", "2026-08-31T23:59:59Z", "2026-09-01T10:00:00Z", "2026-09-01T10:00:00.5Z", "2026-09-01T10:00:00.25Z", "2026-09-02", "2030-01-01"}
	id := 0
	for _, year := range years {
		for _, date := range added {
			id++
			c.Exec(`INSERT INTO scalar_truth VALUES(?,?,?)`, id, year, date)
		}
	}
	fields := map[string]browseField{}
	for _, field := range browseFields {
		fields[field.ID] = field
	}
	numbers := []any{0.0, -5.0, -1.0, 1.0, 1799.0, 1800.0, 1990.0, 1995.0, 1999.5, 2000.0, 2195.0, 2199.0, 2200.0, 1e9}
	type check struct {
		field, operator string
		value           any
	}
	checks := []check{}
	for _, field := range []string{"year", "decade"} {
		for _, operator := range fields[field].Operators {
			for _, a := range numbers {
				switch operator {
				case "between":
					for _, b := range numbers {
						checks = append(checks, check{field, operator, []any{a, b}})
					}
				case "in", "not-in":
					checks = append(checks, check{field, operator, []any{a}}, check{field, operator, []any{a, 2000.0, 0.0}})
				default:
					checks = append(checks, check{field, operator, a})
				}
			}
		}
		// Unnormalized text keeps the field expression's own meaning.
		checks = append(checks, check{field, "equals", "1990"}, check{field, "in", []any{"1990", 2000.0}})
	}
	dates := []any{"2026-09-01", "2026-09-01T10:00:00Z", "2026-09-01T10:00:00.5Z", "2026-09-01T10:00:00.25Z", "2026-08-31T23:59:59Z", "2030-01-01"}
	for _, operator := range fields["dateAdded"].Operators {
		switch operator {
		case "within-last-days":
			for _, days := range []any{1.0, 2.0, 30.0} {
				checks = append(checks, check{"dateAdded", operator, days})
			}
		case "between":
			for _, a := range dates {
				for _, b := range dates {
					checks = append(checks, check{"dateAdded", operator, []any{a, b}})
				}
			}
		default:
			for _, a := range dates {
				checks = append(checks, check{"dateAdded", operator, a})
			}
		}
	}
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	selected := func(clause string, args []any) string {
		t.Helper()
		rows, err := c.DB.Query(`SELECT id FROM scalar_truth e WHERE `+clause+` ORDER BY id`, args...)
		if err != nil {
			t.Fatalf("%s: %v", clause, err)
		}
		defer rows.Close()
		ids := []string{}
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, fmt.Sprint(id))
		}
		return strings.Join(ids, ",")
	}
	rewritten := 0
	for _, check := range checks {
		field, node := fields[check.field], BrowseNode{Field: check.field, Operator: check.operator, Value: check.value}
		reference := &browseCompiler{now: now}
		want, err := reference.comparison(field, field.Entity, node.Operator, node.Value)
		if err != nil {
			t.Fatalf("%+v: %v", check, err)
		}
		indexed := &browseCompiler{now: now}
		got, ok, err := indexed.indexedScalar(field, node)
		if err != nil {
			t.Fatalf("%+v: %v", check, err)
		}
		if !ok {
			continue
		}
		rewritten++
		if a, b := selected(want, reference.args), selected(got, indexed.args); a != b {
			t.Errorf("%s %s %v: field expression selects [%s], indexed form %q selects [%s]", check.field, check.operator, check.value, a, got, b)
		}
	}
	if rewritten < len(checks)/2 {
		t.Fatalf("only %d of %d checks used an indexed form", rewritten, len(checks))
	}
}

// A positive year, decade or dateAdded bound reaches the index search itself,
// not a residual filter over the whole library/kind run (B7).
func TestIndexedScalarsSeekBrowseIndexes(t *testing.T) {
	c := catalogtest.Open(t)
	fields := map[string]browseField{}
	for _, field := range browseFields {
		fields[field.ID] = field
	}
	cases := []struct {
		node  BrowseNode
		index string
		seeks []string
	}{
		{BrowseNode{Field: "year", Operator: "equals", Value: 1999.0}, "catalog_browse_year", []string{"year=?"}},
		{BrowseNode{Field: "year", Operator: "in", Value: []any{1999.0, 2001.0}}, "catalog_browse_year", []string{"year=?"}},
		{BrowseNode{Field: "year", Operator: "between", Value: []any{1990.0, 1999.0}}, "catalog_browse_year", []string{"year>? AND year<?"}},
		{BrowseNode{Field: "year", Operator: "at-least", Value: 2030.0}, "catalog_browse_year", []string{"year>?"}},
		{BrowseNode{Field: "year", Operator: "less-than", Value: 1950.0}, "catalog_browse_year", []string{"year<?"}},
		{BrowseNode{Field: "decade", Operator: "equals", Value: 1990.0}, "catalog_browse_year", []string{"year>? AND year<?"}},
		{BrowseNode{Field: "decade", Operator: "in", Value: []any{1990.0, 2010.0}}, "catalog_browse_year", []string{"year=?"}},
		{BrowseNode{Field: "dateAdded", Operator: "at-least", Value: "2030-01-01"}, "catalog_browse_added", []string{"<expr>>?"}},
		{BrowseNode{Field: "dateAdded", Operator: "less-than", Value: "2000-01-01"}, "catalog_browse_added", []string{"<expr>>? AND <expr><?"}},
		{BrowseNode{Field: "dateAdded", Operator: "equals", Value: "2026-09-01"}, "catalog_browse_added", []string{"<expr>>? AND <expr><?"}},
	}
	for _, tc := range cases {
		compiler := &browseCompiler{now: time.Now()}
		clause, ok, err := compiler.indexedScalar(fields[tc.node.Field], tc.node)
		if err != nil || !ok {
			t.Fatalf("%+v: not rewritten: %v", tc.node, err)
		}
		rows, err := c.DB.Query(`EXPLAIN QUERY PLAN SELECT count(*) FROM catalog_browse_rows e WHERE e.library_id=? AND e.kind=? AND `+clause, append([]any{1, 1}, compiler.args...)...)
		if err != nil {
			t.Fatal(err)
		}
		details := []string{}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		rows.Close()
		plan := strings.Join(details, "\n")
		for _, seek := range tc.seeks {
			if !strings.Contains(plan, tc.index+" (library_id=? AND kind=? AND "+seek+")") {
				t.Errorf("%+v: plan does not seek %s on %s:\n%s", tc.node, seek, tc.index, plan)
			}
		}
	}
}
