package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// inventoryCommandDefinitions is the finite union of W's history/disclosure/
// ACK/fresh-library points and SC's complete NEW ordinary N1 effects. It is an
// inert definition provider, not registration, a profile or fresh enable. The
// runtime still installs only inventoryHistoryDefinitions until trusted ingress
// and the complete qualified resource/physical plan are composed separately.
func inventoryCommandDefinitions() []stmt.Definition {
	return append(inventoryHistoryDefinitions(), scanInventoryFreshDefinitions()...)
}

// commandStatementMaxUses is called only after freezeCommandPlan has checked
// the closed purpose and exact Capture receipt. Inventory effects keep their
// SC-owned use inventory; W adds its outer history work. There is no worker
// override and no numerical Work forecast inferred from a definition.
func commandStatementMaxUses(purpose CommandID, id stmt.ID) int {
	if purpose == BasicStage || purpose == BasicSeal {
		return basicCommandMaxUses(purpose, id)
	}
	if scanDirectoryTerminalPurpose(purpose) {
		return directoryTerminalStatementMaxUses(id)
	}
	if purpose == ProgressReceiptReclaim {
		return wcReclaimStatementMaxUses(id)
	}
	if purpose == SecurityAccept {
		return wcSecurityStatementMaxUses(id)
	}
	if purpose == ProgressAccept {
		return wcProgressStatementMaxUses(id)
	}
	if purpose == InventoryDirectoryDiscover || purpose == InventoryDirectoryEOF {
		return directoryStatementMaxUses(id)
	}
	if searchPublisherPurpose(purpose) {
		return searchPublisherMaxUses(purpose, id)
	}
	if purpose == InventoryCreate || purpose == InventoryClaim {
		return scanLifecycleMaxUses(purpose, id)
	}

	if purpose == InventoryAccept {
		switch id {
		case scanInventoryLibraryRecordLengths, scanRunByteLengths, scanJobByteLengths, scanRootByteLengths, scanReceiptByteLengths, scanMemberByteLengths, scanReceiptIdentityPrefix, scanMemberKeys,
			scanLifecycleRun, scanLifecycleJob, scanLifecycleRoot, scanLifecycleActive, scanSourceLengths, scanSourcePayload, scanSourcePolicy, scanLibraryRootLengths, scanLibraryRoot:
			return 1
		}
		if uses := inventoryFreshMaxUses(id); uses != 0 {
			return uses
		}
		switch id {
		case scanReceiptOwner:
			return 2 // native disclosure bridge and historical lookup
		case inventoryDisclosureJob, inventoryDisclosureLibrary, inventoryFreshLibrary,
			scanReceiptLookup, scanReceiptResults, scanReceiptAck:
			return 1
		default:
			return 0 // no reclaim, lifecycle, Basic or unrelated domain statements
		}
	}
	// Preserve the existing non-Inventory use reservations. Their compiled
	// definition sets and purpose checks remain freezeCommandPlan-owned.
	switch id {
	case enrichmentEffectiveOwner:
		return 3 // current scope, historical resolution, fresh handler
	case enrichmentEffectiveReceipt:
		return 2 // historical resolution and fresh handler
	case scanReceiptOwner:
		return 2 // preserve the existing reservation for this compiled ID
	default:
		return 1
	}
}

// reserveCommandSteps checks pure forecast arithmetic AFTER setup and terminal
// have been reserved. It neither creates a Quantum nor admits work. On any
// error it returns zero, never a usable prefix or a partially frozen plan.
func reserveCommandSteps(remaining Work, steps []statementStep, defs []stmt.Definition, purpose CommandID, bounds ...basicAcceptBounds) (Work, error) {
	if (purpose == BasicAccept && (len(bounds) != 1 || !bounds[0].valid())) || (purpose != BasicAccept && len(bounds) != 0) {
		return Work{}, invalid("basic_accept_forecast_bounds")
	}
	allowed := make(map[stmt.ID]bool, len(defs))
	for _, def := range defs {
		if allowed[def.ID] {
			return Work{}, invalid("command_step")
		}
		allowed[def.ID] = true
	}
	for _, step := range steps {
		uses := commandStatementMaxUses(purpose, step.id)
		if purpose == BasicAccept {
			uses = basicFinalMaxUses(bounds[0], step.id)
		}
		if !allowed[step.id] || uses == 0 || step.work.Rows == 0 || step.work.VMOps == 0 || step.work.Hold <= 0 {
			return Work{}, invalid("command_step")
		}
		delete(allowed, step.id)
		for range uses {
			if !fits(step.work, remaining) {
				return Work{}, ErrQuantum
			}
			remaining = subtract(remaining, step.work)
		}
	}
	if len(allowed) != 0 {
		return Work{}, invalid("missing_command_step")
	}
	return remaining, nil
}
