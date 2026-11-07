package writer

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"testing"
	"time"
)

func basicHandlerTurn(t *testing.T, w *executor, kind CommandID, defs []stmt.Definition, registries ...*stmt.Registry) *commandTurn {
	t.Helper()
	var registry *stmt.Registry
	if len(registries) == 1 {
		registry = registries[0]
	} else if len(registries) == 0 {
		registry = nativeInventoryFixtureRegistry(t, w, defs, "basic_handlers_test.go", "basic_control_test.go", "basic_stage_test.go")
	} else {
		t.Fatal("one captured startup registry only")
	}
	recipe := turnRecipe{limit: Work{65536, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, 8 * time.Millisecond}}
	g, authority, wal, request := governedFixture(t, w.now, governedFixtureProfile{recipe, 8 << 20})
	request.Key.Domain, request.Key.ID = uint16(kind), "basic-run"
	request.Footprint = []governor.Scope{{Library: 7, Domain: uint16(kind)}}
	if err := wal.Publish(wal.Observation(), governor.WALState{PageSize: 4096}, w.now()); err != nil {
		t.Fatal(err)
	}
	decision := g.Try(request)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	custody, err := bindGovernedTurn(kind, recipe.limit, g, authority, wal, decision.Grant, request)
	if err != nil {
		t.Fatal(errors.Join(err, authority.Retired(decision.Grant)))
	}
	custody.basicMemory = commandMemoryBudget{4 << 20, 4 << 20}
	tx, err := w.begin(t.Context(), kind, recipe, custody)
	if err != nil {
		if errors.Is(err, ErrBusy) {
			err = errors.Join(err, custody.retired())
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.rollback(nil) })
	steps := make([]statementStep, 0, len(defs))
	for _, def := range defs {
		rows := uint64(16)

		steps = append(steps, statementStep{def.ID, Work{rows, 10000, 16, 65536, 25 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func basicHandlerCommit(t *testing.T, turn *commandTurn, purpose BasicPurpose, lease int64) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if err := checkBasicTerminal(t.Context(), turn, purpose, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}
