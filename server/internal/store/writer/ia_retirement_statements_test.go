package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestIARetirementClosedStorageGrammar(t *testing.T) {
	defs := iaRetirementHistoryDefinitions()
	if len(defs) != 7 {
		t.Fatal("closed IA history inventory changed")
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Cohorts) != 2 || len(plan.LintSQL(d.SQL)) != 0 ||
			!strings.Contains(d.SQL, "original_operation_id=?") || !strings.Contains(d.SQL, "command_incarnation=?") {
			t.Fatal("not an original-owner point", d.ID)
		}
		if strings.Contains(d.SQL, "library_sources") || strings.Contains(d.SQL, "job_lease") || strings.Contains(d.SQL, "DELETE FROM catalog_removal_progress") {
			t.Fatal("history rebinding or owner deletion", d.ID)
		}
		if d.ID == iaRetirementAckCAS && (strings.Contains(d.SQL, "retained") || strings.Contains(d.SQL, "reclaim") || strings.Contains(d.SQL, "DELETE")) {
			t.Fatal("ACK includes a refund", d.ID)
		}
	}
}
