package writer

import (
	"math"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Only these compiled Inventory history/ACK statements enter the shared
// runtime, plus one fresh-only eligibility read. Fresh effects and reclaim
// are not admitted by this profile.
func inventoryHistoryDefinitions() []stmt.Definition {
	defs := append(inventoryDisclosureDefinitions(), inventoryFreshLibraryDefinition())
	for _, def := range scanReceiptDefinitions() {
		switch def.ID {
		case scanReceiptOwner, scanReceiptLookup, scanReceiptResults, scanReceiptAck:
			defs = append(defs, def)
		}
	}
	return defs
}

// freezeCommandPlan is private composition, not caller budget admission or a
// qualification claim. Reserve every possible registered use, setup and
// terminal before any BEGIN. The two domains share ONE registry and executor.
func freezeCommandPlan(w *executor, plan effectivePlan, defs []stmt.Definition, purpose CommandID) (effectivePlan, error) {
	if purpose != EffectiveStage && purpose != InventoryAccept {
		return effectivePlan{}, invalid("command_plan_purpose")
	}
	// Each profile must itself carry a real matching registration. Another
	// profile's broad receipt must never compensate for a missing/foreign one.
	if _, err := stmt.New(defs, plan.registration); err != nil {
		return effectivePlan{}, err
	}
	if len(plan.steps) != len(defs) || plan.need[governor.MemoryBytes] <= 0 || plan.need[governor.CPUMilli] <= 0 ||
		plan.recipe.limit.Rows == 0 || plan.recipe.limit.Rows > math.MaxUint32 || plan.recipe.limit.Pages == 0 || plan.recipe.limit.Pages > math.MaxUint32 ||
		plan.recipe.limit.WALBytes > math.MaxInt64 || plan.need[governor.WALBytes] != int64(plan.recipe.limit.WALBytes) {
		return effectivePlan{}, invalid("command_plan")
	}
	if _, err := NewQuantum(plan.recipe.limit, plan.recipe.terminal, w.now()); err != nil {
		return effectivePlan{}, err
	}
	if plan.headerSync.Hold <= 0 || plan.headerSync.WALBytes < 32 || !fits(plan.headerSync, plan.recipe.terminal) || plan.need[governor.IOBytes] < 32 {
		return effectivePlan{}, invalid("command_header_sync_forecast")
	}
	allowed := make(map[stmt.ID]bool, len(defs))
	for _, def := range defs {
		allowed[def.ID] = true
	}
	remaining := plan.recipe.limit
	for _, work := range []Work{plan.recipe.setup, plan.recipe.terminal} {
		if work.Hold <= 0 || !fits(work, remaining) {
			return effectivePlan{}, ErrQuantum
		}
		remaining = subtract(remaining, work)
	}
	for _, step := range plan.steps {
		if !allowed[step.id] || step.work.Rows == 0 || step.work.VMOps == 0 || step.work.Hold <= 0 {
			return effectivePlan{}, invalid("command_step")
		}
		delete(allowed, step.id) // also rejects duplicates, not only missing IDs
		uses := 1
		switch step.id {
		case enrichmentEffectiveOwner:
			uses = 3 // current scope, historical resolution, fresh handler
		case enrichmentEffectiveReceipt:
			uses = 2 // historical resolution and fresh handler
		case scanReceiptOwner:
			uses = 2 // current native bridge and historical resolution
		}
		for range uses {
			if !fits(step.work, remaining) {
				return effectivePlan{}, ErrQuantum
			}
			remaining = subtract(remaining, step.work)
		}
	}
	if len(allowed) != 0 {
		return effectivePlan{}, invalid("missing_command_step")
	}
	plan.steps = append([]statementStep(nil), plan.steps...)
	plan.io = append([]governor.DeviceIO(nil), plan.io...)
	for i := range plan.io {
		plan.io[i].DeviceID = strings.Clone(plan.io[i].DeviceID)
	}
	return plan, nil
}
