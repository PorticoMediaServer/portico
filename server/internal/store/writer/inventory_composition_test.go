package writer

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These controls use synthetic unit costs to verify composition arithmetic.
// They execute no SQL and qualify no native/physical Work or production plan.
func TestInventoryCommandFiniteComposition(t *testing.T) {
	defs := inventoryCommandDefinitions()
	if len(defs) != 54 {
		t.Fatalf("complete history+N1 set has %d definitions", len(defs))
	}
	seen, uses := make(map[stmt.ID]bool, len(defs)), 0
	for _, def := range defs {
		n := commandStatementMaxUses(InventoryAccept, def.ID)
		if seen[def.ID] || n == 0 {
			t.Fatalf("duplicate/unfunded %s: %d", def.ID, n)
		}
		seen[def.ID], uses = true, uses+n
	}
	if uses != 65 {
		t.Fatalf("all-branch reservations = %d, want 65", uses)
	}
	for _, id := range inventoryFreshIDs() {
		if !seen[id] {
			t.Fatalf("missing fresh effect %s", id)
		}
	}
	for _, def := range inventoryHistoryDefinitions() {
		if !seen[def.ID] {
			t.Fatalf("missing history/disclosure %s", def.ID)
		}
	}
	for _, id := range []stmt.ID{scanReceiptReclaimKey, scanReceiptReclaimResult, scanReceiptReclaimHeader, enrichmentEffectiveOwner, "scan.inventory_create"} {
		if seen[id] || commandStatementMaxUses(InventoryAccept, id) != 0 {
			t.Fatalf("unselected command/reclaim %s", id)
		}
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	// Metadata must not silently turn the production history-only constructor
	// into a fresh profile, or require fresh steps for existing history calls.
	if len(inventoryHistoryDefinitions()) != 11 {
		t.Fatal("history-only set changed")
	}
}

func TestInventoryCommandReservesBothBankUses(t *testing.T) {
	unit := Work{Rows: 1, VMOps: 1, Pages: 1, WALBytes: 1, Hold: time.Microsecond}
	for _, id := range []stmt.ID{searchInputBank, searchInputInsert, searchInputPending} {
		t.Run(string(id), func(t *testing.T) {
			defs := []stmt.Definition{{ID: id}}
			steps := []statementStep{{id: id, work: unit}}
			if got, err := reserveCommandSteps(unit, steps, defs, InventoryAccept); !errors.Is(err, ErrQuantum) || got != (Work{}) {
				t.Fatalf("single-bank budget admitted dual bank: %v %v", got, err)
			}
			twice := Work{Rows: 2, VMOps: 2, Pages: 2, WALBytes: 2, Hold: 2 * time.Microsecond}
			if got, err := reserveCommandSteps(twice, steps, defs, InventoryAccept); err != nil || got != (Work{}) {
				t.Fatalf("exact dual budget: %v %v", got, err)
			}
			// Repeated subtraction must reject rather than wrapping a product.
			steps[0].work.Rows = math.MaxUint64
			twice.Rows = math.MaxUint64
			if got, err := reserveCommandSteps(twice, steps, defs, InventoryAccept); !errors.Is(err, ErrQuantum) || got != (Work{}) {
				t.Fatalf("overflow-sized reservation: %v %v", got, err)
			}
		})
	}
}

func TestInventoryCommandCompleteBudgetHasNoPartialSuccess(t *testing.T) {
	defs := inventoryCommandDefinitions()
	steps := make([]statementStep, len(defs))
	for i, def := range defs {
		steps[i] = statementStep{id: def.ID, work: Work{Rows: 1, VMOps: 1, Pages: 1, WALBytes: 1, Hold: time.Microsecond}}
	}
	before := append([]statementStep(nil), steps...)
	limit := Work{Rows: 58, VMOps: 58, Pages: 58, WALBytes: 58, Hold: 58 * time.Microsecond}
	if got, err := reserveCommandSteps(limit, steps, defs, InventoryAccept); err != nil || got != (Work{}) {
		t.Fatalf("exact complete budget: %v %v", got, err)
	}
	for _, field := range []string{"rows", "vm", "pages", "wal", "hold"} {
		t.Run(field, func(t *testing.T) {
			short := limit
			switch field {
			case "rows":
				short.Rows--
			case "vm":
				short.VMOps--
			case "pages":
				short.Pages--
			case "wal":
				short.WALBytes--
			case "hold":
				short.Hold--
			}
			if got, err := reserveCommandSteps(short, steps, defs, InventoryAccept); !errors.Is(err, ErrQuantum) || got != (Work{}) {
				t.Fatalf("partial %s budget: %v %v", field, got, err)
			}
			if !reflect.DeepEqual(steps, before) {
				t.Fatal("failed reservation mutated the frozen input")
			}
		})
	}
}

func TestInventoryCommandMissingDuplicateAndForeignSteps(t *testing.T) {
	work := Work{Rows: 10, VMOps: 10, Pages: 10, WALBytes: 10, Hold: 10 * time.Microsecond}
	step := statementStep{id: scanFreshRun, work: Work{Rows: 1, VMOps: 1, Hold: time.Microsecond}}
	def := stmt.Definition{ID: step.id}
	for _, tc := range []struct {
		name  string
		steps []statementStep
		defs  []stmt.Definition
	}{
		{"missing", nil, []stmt.Definition{def}},
		{"duplicate_step", []statementStep{step, step}, []stmt.Definition{def}},
		{"duplicate_definition", []statementStep{step}, []stmt.Definition{def, def}},
		{"foreign", []statementStep{{id: scanReceiptReclaimHeader, work: step.work}}, []stmt.Definition{{ID: scanReceiptReclaimHeader}}},
		{"unregistered", []statementStep{step}, nil},
		{"zero_work", []statementStep{{id: step.id}}, []stmt.Definition{def}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := reserveCommandSteps(work, tc.steps, tc.defs, InventoryAccept); err == nil || got != (Work{}) {
				t.Fatalf("invalid composition: %v %v", got, err)
			}
		})
	}
}

func TestCommandCompositionPreservesHistoryUses(t *testing.T) {
	for _, def := range inventoryHistoryDefinitions() {
		want := 1
		if def.ID == scanReceiptOwner {
			want = 2
		}
		if def.ID == scanMemberByteLengths {
			want = inventoryReceiptMaxMembers
		}
		if got := commandStatementMaxUses(InventoryAccept, def.ID); got != want {
			t.Fatalf("existing Inventory %s: %d != %d", def.ID, got, want)
		}
	}
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	for _, def := range defs {
		want := 1
		switch def.ID {
		case enrichmentEffectiveOwner:
			want = 3
		case enrichmentEffectiveReceipt:
			want = 2
		}
		if got := commandStatementMaxUses(EffectiveStage, def.ID); got != want {
			t.Fatalf("existing Effective %s: %d != %d", def.ID, got, want)
		}
	}
}
