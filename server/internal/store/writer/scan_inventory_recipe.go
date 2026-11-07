package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

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
	return defs
}

// Whole driver BEGIN, including the outer original lookup, current-source
// verification and SC's receipt/result writes. Setup and terminal are separate.
func inventoryDriverMaxUses(id stmt.ID) int {
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
		scanSourceLengths, scanSourcePayload, scanSourcePolicy, scanLibraryRootLengths, scanLibraryRoot:
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

func freezeInventoryDriverPlan(w *executor, p effectivePlan) (effectivePlan, error) {
	p, err := freezeCommandPlan(w, p, inventoryDriverCommandDefinitions(), InventoryAccept)
	if err != nil {
		return effectivePlan{}, err
	}
	if p.inventoryMemory.preparationBytes < scanByteFixedWorkspace {
		return effectivePlan{}, &Refusal{Code: RefusalUnqualified}
	}
	remaining := subtract(subtract(p.recipe.limit, p.recipe.setup), p.recipe.terminal)
	if _, err := reserveInventoryDriverSteps(remaining, p.steps); err != nil {
		return effectivePlan{}, err
	}
	return p, nil
}
