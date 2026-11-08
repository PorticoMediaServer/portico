package writer

import (
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestN1DriverPlanRequiresBoundKeeper(t *testing.T) {
	w := &executor{}
	var refusal *Refusal
	if p, err := freezeInventoryDriverPlan(w, effectivePlan{}); !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || len(p.steps) != 0 {
		t.Fatal("fresh profile assumed a second native connection without its actual keeper", p, err)
	}
	// A constructed service value is not GV's bound native lifetime pin.
	w.keeper.Store(&governor.CheckpointWriterService{})
	if p, err := freezeInventoryDriverPlan(w, effectivePlan{}); !errors.Is(err, governor.ErrCheckpointKeeper) || len(p.steps) != 0 {
		t.Fatal("unbound service became a physical-close guarantee", p, err)
	}
}

// Structural composition only: synthetic unit charges are arithmetic controls,
// never numerical forecasts or permission to install an accepting profile.
func TestN1DriverCompleteDefinitionAndUseInventory(t *testing.T) {
	defs := inventoryDriverCommandDefinitions()
	if len(defs) != 64 {
		t.Fatal("driver definition inventory", len(defs))
	}
	seen, uses := make(map[stmt.ID]bool), 0
	for _, d := range defs {
		n := inventoryDriverMaxUses(d.ID)
		if seen[d.ID] || n <= 0 {
			t.Fatal("duplicate or unreserved driver statement", d.ID, n)
		}
		seen[d.ID], uses = true, uses+n
	}
	if uses != 76 {
		t.Fatal("complete all-branch driver reservations", uses)
	}
	for _, d := range append(inventoryCommandDefinitions(), scanSourceDefinitions()...) {
		if !seen[d.ID] {
			t.Fatal("missing effects/history/current-source point", d.ID)
		}
	}
	for _, id := range []stmt.ID{scanLifecycleRun, scanReceiptReclaimHeader, "unselected"} {
		if seen[id] || inventoryDriverMaxUses(id) != 0 {
			t.Fatal("unselected lifecycle/reclaim work entered driver profile", id)
		}
	}
	if commandStatementMaxUses(InventoryAccept, inventoryFreshLibrary) != 1 {
		t.Fatal("existing history plan's fresh-library reservation changed")
	}
}

func TestN1DriverReservesTheSecondFreshLibraryUse(t *testing.T) {
	unit := Work{1, 1, 1, 1, time.Microsecond}
	var steps []statementStep
	for _, d := range inventoryDriverCommandDefinitions() {
		steps = append(steps, statementStep{d.ID, unit})
	}
	exact := Work{76, 76, 76, 76, 76 * time.Microsecond}
	if left, err := reserveInventoryDriverSteps(exact, steps); err != nil || left != (Work{}) {
		t.Fatal("complete recipe did not reserve exactly", left, err)
	}
	short := Work{75, 75, 75, 75, 75 * time.Microsecond}
	if left, err := reserveInventoryDriverSteps(short, steps); !errors.Is(err, ErrQuantum) || left != (Work{}) {
		t.Fatal("source check borrowed an unreserved fresh-library use", left, err)
	}
}
