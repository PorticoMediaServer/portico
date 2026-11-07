package writer

import (
	"math"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
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
	return append(defs, inventoryHistoryByteDefinitions()...)
}

// freezeCommandPlan is private composition, not caller budget admission or a
// qualification claim. Reserve every possible registered use, setup and
// terminal before any BEGIN. All domains share ONE registry and executor.
func freezeCommandPlan(w *executor, plan effectivePlan, defs []stmt.Definition, purpose CommandID) (effectivePlan, error) {
	if purpose != EffectiveStage && purpose != InventoryAccept && purpose != SearchImageStage && purpose != InventoryCreate && purpose != InventoryClaim && purpose != ProgressAccept && purpose != SecurityAccept && purpose != ProgressReceiptReclaim && !scanDirectoryPurpose(purpose) {
		return effectivePlan{}, invalid("command_plan_purpose")
	}
	if plan.inventoryEngine != (sqliteconn.EngineIdentity{}) && (purpose != InventoryAccept || len(defs) != len(inventoryDriverCommandDefinitions())) {
		return effectivePlan{}, invalid("inventory_engine_purpose")
	}
	if plan.inventoryPageSize != 0 && (purpose != InventoryAccept || len(defs) != len(inventoryDriverCommandDefinitions())) {
		return effectivePlan{}, invalid("inventory_page_size_purpose")
	}
	if plan.inventoryRecords != (inventoryRecordEnvelope{}) {
		if purpose != InventoryAccept || len(defs) != len(inventoryDriverCommandDefinitions()) {
			return effectivePlan{}, invalid("inventory_record_work_purpose")
		}
		found := false
		for _, d := range defs {
			if d.ID == scanInventoryLibraryRecordLengths {
				found = true
			}
		}
		if !found {
			return effectivePlan{}, invalid("inventory_record_work_purpose")
		}
	}
	if purpose != InventoryAccept && plan.inventoryMemory != (inventoryMemoryBudget{}) {
		return effectivePlan{}, invalid("memory_purpose")
	}
	if purpose != SearchImageStage && plan.searchImageMemory != (searchImageMemoryBudget{}) {
		return effectivePlan{}, invalid("memory_purpose")
	}
	if purpose != InventoryCreate && purpose != InventoryClaim && plan.scanLifecycleMemory != (commandMemoryBudget{}) {
		return effectivePlan{}, invalid("memory_purpose")
	}
	if purpose != BasicStage && purpose != BasicSeal && purpose != BasicAccept && plan.basicMemory != (commandMemoryBudget{}) {
		return effectivePlan{}, invalid("memory_purpose")
	}
	if err := validateMemoryPartition(plan.basicMemory, plan.need[governor.MemoryBytes], "basic_memory_partition"); err != nil {
		return effectivePlan{}, err
	}
	if err := validateInventoryMemory(plan.inventoryMemory, plan.need[governor.MemoryBytes]); err != nil {
		return effectivePlan{}, err
	}
	if err := validateSearchImageMemory(plan.searchImageMemory, plan.need[governor.MemoryBytes]); err != nil {
		return effectivePlan{}, err
	}
	if err := validateMemoryPartition(plan.scanLifecycleMemory, plan.need[governor.MemoryBytes], "scan_lifecycle_memory_partition"); err != nil {
		return effectivePlan{}, err
	}
	if purpose != ProgressAccept && plan.wcProgressMemory != (commandMemoryBudget{}) {
		return effectivePlan{}, invalid("memory_purpose")
	}
	if err := validateMemoryPartition(plan.wcProgressMemory, plan.need[governor.MemoryBytes], "wc_progress_memory"); err != nil {
		return effectivePlan{}, err
	}
	if purpose != SecurityAccept && plan.wcSecurityMemory != (commandMemoryBudget{}) {
		return effectivePlan{}, invalid("memory_purpose")
	}
	if err := validateMemoryPartition(plan.wcSecurityMemory, plan.need[governor.MemoryBytes], "wc_security_memory"); err != nil {
		return effectivePlan{}, err
	}
	if purpose != ProgressReceiptReclaim && plan.wcReclaimMemory != (commandMemoryBudget{}) {
		return effectivePlan{}, invalid("memory_purpose")
	}
	if err := validateMemoryPartition(plan.wcReclaimMemory, plan.need[governor.MemoryBytes], "wc_reclaim_memory"); err != nil {
		return effectivePlan{}, err
	}
	if !scanDirectoryPurpose(purpose) && plan.directoryMemory != (commandMemoryBudget{}) {
		return effectivePlan{}, invalid("memory_purpose")
	}
	if err := validateMemoryPartition(plan.directoryMemory, plan.need[governor.MemoryBytes], "directory_memory_partition"); err != nil {
		return effectivePlan{}, err
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
	remaining := plan.recipe.limit
	for _, work := range []Work{plan.recipe.setup, plan.recipe.terminal} {
		if work.Hold <= 0 || !fits(work, remaining) {
			return effectivePlan{}, ErrQuantum
		}
		remaining = subtract(remaining, work)
	}
	if _, err := reserveCommandSteps(remaining, plan.steps, defs, purpose); err != nil {
		return effectivePlan{}, err
	}
	plan.steps = append([]statementStep(nil), plan.steps...)
	plan.io = append([]governor.DeviceIO(nil), plan.io...)
	for i := range plan.io {
		plan.io[i].DeviceID = strings.Clone(plan.io[i].DeviceID)
	}
	return plan, nil
}
