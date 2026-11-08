package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"strings"
	"testing"
)

// Adopted C019 journal fragment only: actual native SEARCH proof, no Capture
// golden or production recipe. Re-record on the composed C embed, once.
func TestIARefreshStatementPlans(t *testing.T) {
	w, _, _, _ := iaRefreshFixture(t)
	for _, d := range iaRefreshDefinitions() {
		if !strings.HasPrefix(string(d.ID), "ia.refresh.") {
			continue
		}
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 {
			t.Fatal(d.ID, "closed hot plan required")
		}
		if !strings.HasSuffix(string(d.ID), "bytes") && len(plan.LintSQL(d.SQL)) != 0 {
			t.Fatal(d.ID, plan.LintSQL(d.SQL))
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
			if d.Kind == stmt.Read && !searched {
				t.Fatal(d.ID, "missing SEARCH")
			}
		}
	}
}
