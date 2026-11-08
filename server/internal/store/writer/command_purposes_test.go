package writer

import (
	"errors"
	"testing"
)

// The pre-registry gate expressions, kept verbatim as the behaviour oracle.
func legacyPlanPurpose(p CommandID) bool {
	return !(p != EffectiveStage && p != EnrichAccept && p != InventoryAccept && p != SearchImageStage && p != InventoryCreate && p != InventoryClaim && p != ProgressAccept && p != SecurityAccept && p != ProgressReceiptReclaim && !scanDirectoryPurpose(p) && !searchPublisherPurpose(p) && !enrichmentPurpose(p))
}

func legacyMemoryPurpose(p CommandID) bool {
	return !(p != InventoryAccept && p != SearchImageStage && p != InventoryCreate && p != InventoryClaim && p != BasicStage && p != BasicSeal && p != BasicAccept && p != ProgressAccept && p != SecurityAccept && p != ProgressReceiptReclaim && p != EffectiveStage && p != EnrichAccept && !scanDirectoryPurpose(p) && !searchPublisherPurpose(p) && !enrichmentPurpose(p))
}

// The legacy last-assignment-wins partition selection.
func legacyPartition(p CommandID) memoryPartition {
	b := inventoryPartition
	if enrichmentPurpose(p) {
		b = enrichmentPartition
	}
	if p == SearchImageStage {
		b = searchImagePartition
	}
	if p == InventoryCreate || p == InventoryClaim {
		b = scanLifecyclePartition
	}
	if p == BasicStage || p == BasicSeal || p == BasicAccept {
		b = basicPartition
	}
	if p == ProgressAccept {
		b = wcProgressPartition
	}
	if p == ProgressReceiptReclaim {
		b = wcReclaimPartition
	}
	if p == SecurityAccept {
		b = wcSecurityPartition
	}
	if scanDirectoryPurpose(p) {
		b = directoryPartition
	}
	if searchPublisherPurpose(p) {
		b = publisherPartition
	}
	if effectiveSourceProofPurpose(p) {
		b = effectiveSourcePartition
	}
	return b
}

// The legacy freezeCommandPlan per-field purpose predicates.
func legacyPartitionAllowed(p CommandID, part memoryPartition) bool {
	switch part {
	case inventoryPartition:
		return p == InventoryAccept
	case searchImagePartition:
		return p == SearchImageStage
	case scanLifecyclePartition:
		return p == InventoryCreate || p == InventoryClaim
	case enrichmentPartition:
		return enrichmentPurpose(p)
	case basicPartition:
		return p == BasicStage || p == BasicSeal || p == BasicAccept
	case wcProgressPartition:
		return p == ProgressAccept
	case wcSecurityPartition:
		return p == SecurityAccept
	case wcReclaimPartition:
		return p == ProgressReceiptReclaim
	case directoryPartition:
		return scanDirectoryPurpose(p)
	case publisherPartition:
		return searchPublisherPurpose(p)
	case effectiveSourcePartition:
		return effectiveSourceProofPurpose(p)
	}
	return false
}

func legacyClock(p CommandID) commandClock {
	if p != InventoryAccept && p != SearchImageStage && p != InventoryCreate && p != InventoryClaim && p != BasicStage && p != BasicSeal && p != BasicAccept && p != ProgressAccept && p != SecurityAccept && p != ProgressReceiptReclaim && !scanDirectoryPurpose(p) && !searchPublisherPurpose(p) {
		return noCommandClock
	}
	if p == InventoryCreate || p == ProgressAccept || p == SecurityAccept || p == ProgressReceiptReclaim {
		return clockWithoutLease
	}
	return clockUnderLease
}

func TestCommandPurposeRegistryMatchesLegacyGates(t *testing.T) {
	for id := range closedCommandDescriptors {
		facts, ok := commandPurposeFacts(id)
		if commandPlanPurpose(id) != legacyPlanPurpose(id) {
			t.Fatal("plan gate changed", id)
		}
		wantMemory := noMemoryPartition
		if legacyMemoryPurpose(id) {
			wantMemory = legacyPartition(id)
		}
		if facts.memory != wantMemory {
			t.Fatal("memory partition changed", id, facts.memory, wantMemory)
		}
		if facts.clock != legacyClock(id) {
			t.Fatal("clock gate changed", id, facts.clock, legacyClock(id))
		}
		for part := noMemoryPartition + 1; part < memoryPartitionCount; part++ {
			if purposeUsesPartition(id, part) != legacyPartitionAllowed(id, part) {
				t.Fatal("plan partition purpose changed", id, part)
			}
		}
		if ok != (legacyPlanPurpose(id) || legacyMemoryPurpose(id) || legacyClock(id) != noCommandClock) {
			t.Fatal("registry membership changed", id)
		}
	}
	if len(closedCommandPurposes) == 0 || len(closedCommandPurposes) > len(closedCommandDescriptors) {
		t.Fatal("registry size", len(closedCommandPurposes))
	}
}

func TestCommandPurposeRegistryRefusesDuplicatesAndUnknowns(t *testing.T) {
	ok := commandPurpose{id: InventoryAccept, plan: true}
	for name, regs := range map[string][]commandPurposeRegistration{
		"duplicate_in_domain":    {{owner: "a", purposes: []commandPurpose{ok, ok}}},
		"duplicate_across":       {{owner: "a", purposes: []commandPurpose{ok}}, {owner: "b", purposes: []commandPurpose{ok}}},
		"duplicate_owner":        {{owner: "a", purposes: []commandPurpose{ok}}, {owner: "a", purposes: []commandPurpose{{id: InventoryClaim, plan: true}}}},
		"empty_owner":            {{purposes: []commandPurpose{ok}}},
		"empty_domain":           {{owner: "a"}},
		"unknown_command":        {{owner: "a", purposes: []commandPurpose{{id: 0xFFFF, plan: true}}}},
		"no_facts":               {{owner: "a", purposes: []commandPurpose{{id: InventoryAccept}}}},
		"partition_out_of_range": {{owner: "a", purposes: []commandPurpose{{id: InventoryAccept, memory: memoryPartitionCount}}}},
		"clock_out_of_range":     {{owner: "a", purposes: []commandPurpose{{id: InventoryAccept, clock: clockUnderLease + 1}}}},
	} {
		if _, err := buildCommandPurposes(regs...); err == nil {
			t.Fatal("invalid purpose registration accepted", name)
		}
	}
	// An undeclared closed command is refused by every shared gate.
	for _, id := range []CommandID{PeopleStage, SearchFragmentReclaim, 0, 0xFFFF} {
		if _, declared := commandPurposeFacts(id); declared || commandPlanPurpose(id) {
			t.Fatal("undeclared purpose admitted", id)
		}
		for part := noMemoryPartition; part <= memoryPartitionCount; part++ {
			if purposeUsesPartition(id, part) {
				t.Fatal("undeclared purpose owns a partition", id, part)
			}
		}
		var turn *commandTurn
		if err := turn.requireCommandMemory(t.Context(), id, 1, "x"); !errors.Is(err, ErrInvalid) {
			t.Fatal("undeclared purpose reached memory custody", id, err)
		}
		if _, err := turn.commandTimeMS(t.Context(), id, 1); !errors.Is(err, ErrInvalid) {
			t.Fatal("undeclared purpose reached command clock", id, err)
		}
	}
	if (&governedTurn{}).memoryPartitionOrInvalid(noMemoryPartition) == nil || (&governedTurn{}).memoryPartitionOrInvalid(memoryPartitionCount) == nil {
		t.Fatal("custody selected an undeclared partition")
	}
}

func (c *governedTurn) memoryPartitionOrInvalid(p memoryPartition) error {
	_, err := c.memoryPartition(p)
	return err
}
