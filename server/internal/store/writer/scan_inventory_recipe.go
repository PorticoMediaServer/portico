package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The file header reserves an unsigned byte per page. This is a conservative
// usable-size floor, not an observation that the database reserves zero bytes.
// The configured page size is read and asserted by the SAME physical writer
// connection before BEGIN, including every replacement; zero is unqualified.
func inventoryUsablePageFloor(pageSize int32) (uint64, error) {
	if pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 {
		return 0, &Refusal{Code: RefusalUnqualified}
	}
	return uint64(pageSize) - 255, nil
}

// The driver adds its current source/policy/logical-root verification to the
// existing complete history/effects set. This is inert: no runtime installs
// this plan until W's complete structural and physical profile is reviewed.
func inventoryDriverCommandDefinitions() []stmt.Definition {
	defs := inventoryCommandDefinitions()
	for _, def := range scanSourceDefinitions() {
		if def.ID != inventoryFreshLibrary { // already in the history provider
			defs = append(defs, def)
		}
	}
	for _, def := range scanRunLifecycleDefinitions() {
		if def.ID == scanLifecycleActive {
			defs = append(defs, def)
		}
	}
	for _, def := range scanBytePreflightDefinitions() {
		switch def.ID {
		case scanRunByteLengths, scanJobByteLengths, scanRootByteLengths:
			defs = append(defs, def)
		}
	}
	return append(defs, inventoryLibraryRecordDefinition())
}

// Whole driver BEGIN, including the outer original lookup, current-source
// verification and SC's receipt/result writes. Setup and terminal are separate.
func inventoryDriverMaxUses(id stmt.ID) int {
	if id == scanMemberByteLengths {
		return inventoryReceiptMaxMembers
	}
	if uses := inventoryFreshMaxUses(id); uses != 0 {
		return uses
	}
	switch id {
	case scanReceiptOwner:
		return 2 // native disclosure bridge and original history
	case inventoryFreshLibrary:
		return 2 // fresh eligibility and current source verification
	case inventoryDisclosureJob, inventoryDisclosureLibrary,
		scanReceiptLookup, scanReceiptResults, scanReceiptAck,
		scanReceiptByteLengths, scanMemberByteLengths, scanReceiptIdentityPrefix, scanMemberKeys,
		scanSourceLengths, scanSourcePayload, scanSourcePolicy, scanLibraryRootLengths, scanLibraryRoot, scanLifecycleActive, scanRunByteLengths, scanJobByteLengths, scanRootByteLengths, scanInventoryLibraryRecordLengths:
		return 1
	default:
		return 0
	}
}

// Preserve the existing history/effects reservations. Only the dedicated full
// driver plan reserves the second fresh-library use; existing plans do not
// acquire an extra charge or change admission behavior.
func reserveInventoryDriverSteps(remaining Work, steps []statementStep) (Work, error) {
	left, err := reserveCommandSteps(remaining, steps, inventoryDriverCommandDefinitions(), InventoryAccept)
	if err != nil {
		return Work{}, err
	}
	for _, step := range steps {
		if step.id == inventoryFreshLibrary {
			if !fits(step.work, left) {
				return Work{}, ErrQuantum
			}
			return subtract(left, step.work), nil
		}
	}
	return Work{}, invalid("inventory_driver_library_step")
}

// Population invariant: all records are written only by the new layer's
// bounded producers. Cutover starts a fresh database; this profile makes no
// legacy-population compatibility or adoption claim.
func freezeInventoryDriverPlan(w *executor, p effectivePlan) (effectivePlan, error) {
	if w == nil {
		return effectivePlan{}, ErrInvalid
	}
	// Reuse GV's already-bound physical checkpointer connection. The complete
	// fresh profile cannot assume that discarding W is not the last close
	// unless the actual writer service holds this same-file lifetime pin.
	keeper := w.keeper.Load()
	if keeper == nil {
		return effectivePlan{}, &Refusal{Code: RefusalUnqualified}
	}
	if err := keeper.Check(); err != nil {
		return effectivePlan{}, err
	}
	if w.config.Role != sqliteconn.Writer {
		return effectivePlan{}, &Refusal{Code: RefusalUnqualified}
	}
	if _, err := inventoryUsablePageFloor(w.config.WriterPageSize); err != nil {
		return effectivePlan{}, err
	}
	if p.inventoryEngine.Version == "" || p.inventoryEngine.SourceID == "" {
		return effectivePlan{}, &Refusal{Code: RefusalUnqualified}
	}
	p, err := freezeCommandPlan(w, p, inventoryDriverCommandDefinitions(), InventoryAccept)
	if err != nil {
		return effectivePlan{}, err
	}
	p.inventoryPageSize = w.config.WriterPageSize
	if p.inventoryMemory.preparationBytes < scanByteFixedWorkspace {
		return effectivePlan{}, &Refusal{Code: RefusalUnqualified}
	}
	if err := validateInventoryRecordEnvelope(p); err != nil {
		return effectivePlan{}, err
	}
	remaining := subtract(subtract(p.recipe.limit, p.recipe.setup), p.recipe.terminal)
	if _, err := reserveInventoryDriverSteps(remaining, p.steps); err != nil {
		return effectivePlan{}, err
	}
	return p, nil
}
