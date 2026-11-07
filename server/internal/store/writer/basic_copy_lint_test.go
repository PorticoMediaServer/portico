package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"testing"
)

func TestBasicCopyNewDefinitionsStayClosed(t *testing.T) {
	for _, defs := range [][]stmt.Definition{basicCopyMediaFactsDefinitions(), basicCopySubtitlesDefinitions(), basicCopyCleanupMediaDefinitions()} {
		if _, err := stmt.Describe(defs); err != nil {
			t.Fatal(err)
		}
		for _, d := range defs {
			if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
				t.Fatal(d.ID, issues)
			}
		}
	}
}
