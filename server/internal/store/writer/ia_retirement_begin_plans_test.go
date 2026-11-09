package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Group C is not embedded yet. plan.Capture refuses a live schema that differs
// from the embedded native layout, so no Capture golden is recorded here
// without faking one. These native EXPLAIN receipts are the same standard the
// merged IA entry, IA-06 and IA-07 families apply to their unembedded draft:
// every hot point read is one indexed SEARCH and no statement needs a temp
// B-tree.
func TestIARetirementBeginStatementPlans(t *testing.T) {
	w, _, _ := iaRetirementBeginFixture(t)
	defs := append(iaRetirementHistoryDefinitions(), iaRetirementBeginStatements()...)
	if len(defs) != 12 {
		t.Fatal("closed retirement BEGIN inventory changed", len(defs))
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(plan.LintSQL(d.SQL)) != 0 {
			t.Fatal("retirement BEGIN must retain closed hot grammar", d.ID, plan.LintSQL(d.SQL))
		}
		if len(d.Cohorts) == 0 {
			t.Fatal("statement lacks a native plan cohort", d.ID)
		}
		for _, cohort := range d.Cohorts {
			rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(d.ID, cohort.Name, err)
			}
			searched := false
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "SCAN ") && !strings.HasPrefix(detail, "SCAN CONSTANT") {
					rows.Close()
					t.Fatal(d.ID, cohort.Name, detail)
				}
				if strings.HasPrefix(detail, "SEARCH ") {
					searched = true
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			rows.Close()
			if d.Kind == stmt.Read && !searched {
				t.Fatal(d.ID, cohort.Name, "read has no indexed SEARCH")
			}
		}
	}
	t.Logf("%d hot retirement BEGIN statements on the group C draft; EXPLAIN-only, no Capture claim", len(defs))
}
