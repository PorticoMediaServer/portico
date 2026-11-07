package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestIARetirementReclaimClosedGrammar(t *testing.T) {
	defs := iaRetirementReceiptReclaimDefinitions()
	if len(defs) != 11 {
		t.Fatal("closed106 inventory changed", len(defs))
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) > 0 {
			t.Fatal(d.ID, issues)
		}
		if strings.Contains(d.SQL, "job_lease") || strings.Contains(d.SQL, "DELETE FROM catalog_removal_progress") {
			t.Fatal("GC acquired a lease/deleted its owner", d.ID)
		}
	}
}
