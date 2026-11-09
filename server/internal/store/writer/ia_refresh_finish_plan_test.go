package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// IA23's finish/gc statements are compiled against the adopted C019 journal
// fixture only. Every one is a closed hot SEARCH (or a write with an exact
// primary-key predicate); no Capture golden or production recipe is minted from
// an unembedded schema.
func TestIARefreshFinishStatementPlans(t *testing.T) {
	w, _, _, _ := iaRefreshFinishFixture(t)
	for _, d := range iaRefreshFinishOwnDefinitions() {
		if !strings.HasPrefix(string(d.ID), "ia.refresh.finish.") && !strings.HasPrefix(string(d.ID), "ia.refresh.gc.") {
			t.Fatal("foreign statement in the IA23 registry", d.ID)
		}
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 {
			t.Fatal(d.ID, "closed hot plan required")
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
		if len(d.Cohorts) == 0 {
			t.Fatal(d.ID, "missing cohort bindings")
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
