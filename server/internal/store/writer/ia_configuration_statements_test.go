package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"testing"
)

func TestIAConfigurationClosedHistoryGrammar(t *testing.T) {
	defs := iaConfigurationHistoryDefinitions()
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) > 0 {
			t.Fatal(d.ID, issues)
		}
	}
}
