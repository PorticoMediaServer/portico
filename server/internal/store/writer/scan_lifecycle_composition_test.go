package writer

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestScanLifecycleFiniteUseInventory(t *testing.T) {
	defs := scanLifecycleCommandDefinitions()
	if len(defs) != 36 {
		t.Fatal("finite lifecycle provider changed", len(defs))
	}
	seen := map[stmt.ID]bool{}
	for _, def := range defs {
		if seen[def.ID] {
			t.Fatal("duplicate provider", def.ID)
		}
		seen[def.ID] = true
		for _, purpose := range []CommandID{InventoryCreate, InventoryClaim} {
			if commandStatementMaxUses(purpose, def.ID) == 0 {
				t.Fatal("unreserved compiled use", purpose, def.ID)
			}
		}
	}
	// One W fresh-vector/absence read is IN ADDITION to SC's documented native
	// create2 / claim3 full snapshot readbacks, including receipt verification.
	for _, id := range []stmt.ID{scanLifecycleRun, scanLifecycleJob, scanLifecycleRoot} {
		if commandStatementMaxUses(InventoryCreate, id) != 3 || commandStatementMaxUses(InventoryClaim, id) != 5 {
			t.Fatal("underreserved W+SC snapshot", id)
		}
	}
	for _, purpose := range []CommandID{InventoryCreate, InventoryClaim} {
		for _, id := range []stmt.ID{scanRunHistoryOwner, scanRunHistoryBody, scanReceiptLookup, scanReceiptReclaimKey} {
			if commandStatementMaxUses(purpose, id) != 2 {
				t.Fatal("ACK original revalidation omitted", id)
			}
		}
		for _, foreign := range []stmt.ID{scanInventoryEntity, enrichmentEffectiveOwner, "scan.unknown"} {
			if commandStatementMaxUses(purpose, foreign) != 0 {
				t.Fatal("foreign use admitted", foreign)
			}
		}
	}
}
