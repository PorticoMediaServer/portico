package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Group C is not embedded, so no Capture golden is recorded here. These are
// native EXPLAIN receipts on the exact draft fixture: every hot point is one
// indexed SEARCH, the keysets need no temporary B-tree, and the family is closed.
func TestIARetirementStepStatementPlans(t *testing.T) {
	w, _, _, _, _ := iaRetirementStepFixture(t)
	defs := iaRetirementStepStatements()
	if len(defs) != 36 {
		t.Fatal("closed retirement STEP inventory changed", len(defs))
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(plan.LintSQL(d.SQL)) != 0 {
			t.Fatal("retirement STEP must retain closed hot grammar", d.ID, plan.LintSQL(d.SQL))
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
	t.Logf("%d hot retirement STEP statements on the group C draft; EXPLAIN-only, no Capture claim", len(defs))
}
