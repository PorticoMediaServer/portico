package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Per-statement native EXPLAIN controls complement the complete canonical C019
// registry Captures in TestC019NativeWriterRegistries. Neither enables a caller.
func TestIAConfigureSourceStatementPlans(t *testing.T) {
	w, _, _ := iaConfigureSourceFixture(t)
	defs := append(iaConfigureSourceStatements(), wcAPIEventDefinitions()...)
	if len(iaConfigureSourceDefinitions()) != len(defs)+len(iaEntryDefinitions()) {
		t.Fatal("configure-source family inventory changed")
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(plan.LintSQL(d.SQL)) != 0 {
			t.Fatal("configure-source statements must retain closed hot grammar", d.ID)
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
	if got := len(iaConfigureSourceStatements()); got != 10 {
		t.Fatal("configure-source statement count changed", got)
	}
	t.Logf("%d hot configure-source statements on canonical C019; per-statement EXPLAIN controls", len(defs))
}
