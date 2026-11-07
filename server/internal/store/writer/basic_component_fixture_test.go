package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"testing"
	"time"
)

// New multi-step component effects use synthetic per-step Work. Keep the
// merged stream fixture and its capture/source journal byte-identical; these
// virtual costs are neither production numbers nor wall-clock qualification.
func basicBoundedComponentTurn(t *testing.T, w *executor, defs []stmt.Definition) *commandTurn {
	t.Helper()
	turn := basicComponentTurn(t, w, defs)
	for id, step := range turn.steps {
		step.work.Hold = 25 * time.Microsecond
		turn.steps[id] = step
	}
	return turn
}
