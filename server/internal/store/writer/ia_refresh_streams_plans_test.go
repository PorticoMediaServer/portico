package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"strings"
	"testing"
)

// Group C is a frozen candidate, so this is real native SEARCH proof against the
// adopted C019 draft fragment, not a Capture or EXPLAIN golden. Record once the
// embed lands.
func iaRefreshAssertWalkPlans(t *testing.T, defs []stmt.Definition) {
	t.Helper()
	w, _, _, _ := iaRefreshFixture(t)
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) == 0 {
			t.Fatal(d.ID, "closed hot plan required")
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
		for _, cohort := range d.Cohorts {
			rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(d.ID, err)
			}
			searched := false
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "SCAN ") && !strings.HasPrefix(detail, "SCAN CONSTANT") {
					rows.Close()
					t.Fatal(d.ID, detail)
				}
				searched = searched || strings.HasPrefix(detail, "SEARCH ")
			}
			if err = rows.Err(); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			rows.Close()
			if !searched {
				t.Fatal(d.ID, "missing SEARCH")
			}
		}
	}
}

func TestIARefreshStreamStatementPlans(t *testing.T) {
	iaRefreshAssertWalkPlans(t, iaRefreshStreamDefinitions())
}
