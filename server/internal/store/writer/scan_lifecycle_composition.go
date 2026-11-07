package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert finite definition/use inventory. This neither registers statements nor
// supplies a numerical resource profile, source/birth/lease authority or enable.
func scanLifecycleCommandDefinitions() []stmt.Definition {
	defs := append(scanRunLifecycleDefinitions(), scanRunLifecycleHistoryDefinitions()...)
	defs = append(defs, scanBirthDefinitions()...)
	defs = append(defs, scanSourceDefinitions()...)
	defs = append(defs, inventoryDisclosureDefinitions()...)
	for _, def := range scanReceiptDefinitions() {
		switch def.ID {
		case scanReceiptLookup, scanReceiptAck, scanReceiptReclaimKey, scanReceiptReclaimHeader:
			defs = append(defs, def)
		}
	}
	return append(defs, scanLifecycleByteDefinitions()...)
}

func scanLifecycleMaxUses(purpose CommandID, id stmt.ID) int {
	if purpose != InventoryCreate && purpose != InventoryClaim {
		return 0
	}
	switch id {
	case scanLifecycleRun, scanLifecycleJob, scanLifecycleRoot:
		if purpose == InventoryClaim {
			return 5
		} // W exclusion +fresh vector +SC before/post +receipt post
		return 3 // W birth absence +SC post +receipt post
	case scanRunHistoryOwner, scanReceiptLookup, scanRunHistoryBody, scanReceiptReclaimKey,
		scanReceiptByteLengths, scanReceiptOwnerPrefix, scanReceiptIdentityPrefix:
		return 2 // original history +same-invocation ACK revalidation, conservative
	case scanRunByteLengths:
		return 3 // history revalidation twice, plus current-native claim exclusion
	case scanJobByteLengths, scanRootByteLengths:
		return 1 // one current snapshot/claim exclusion
	case scanBirthRun, scanBirthJob, scanBirthOperation, scanBirthActive,
		inventoryDisclosureJob, inventoryDisclosureLibrary, inventoryFreshLibrary,
		scanSourceLengths, scanSourcePayload, scanSourcePolicy, scanLibraryRootLengths, scanLibraryRoot,
		scanLifecycleActive, scanLifecycleInsertJob, scanLifecycleInsertRun, scanLifecycleInsertRoot, scanLifecycleInsertActive,
		scanLifecycleClaimNull, scanLifecycleClaimDue, scanLifecycleClaimJob, scanLifecycleReceiptInsert,
		scanReceiptAck, scanReceiptReclaimHeader:
		return 1
	default:
		return 0
	}
}
