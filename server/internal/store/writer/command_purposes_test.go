package writer

import (
	"errors"
	"testing"
)

// The pre-registry gate expressions as of e0953113, frozen as the behaviour
// oracle. Live helpers are replaced by their then-exact sets, and only those
// legacy purposes are compared, so new domain purposes never edit this file.
var (
	frozenDirectory  = map[CommandID]bool{InventoryDirectoryDiscover: true, InventoryDirectoryEOF: true, InventoryDirectoryRefuse: true, InventoryTraversalComplete: true}
	frozenPublisher  = map[CommandID]bool{SearchFragmentStage: true, SearchHeadPublish: true}
	frozenEnrichment = map[CommandID]bool{EvidenceStage: true, CreditStage: true, EvidenceSeal: true, CreditSourceSeal: true}
	frozenEffective  = map[CommandID]bool{EffectiveStage: true, EnrichAccept: true}
)

func frozenLegacyPurposes() []CommandID {
	ids := []CommandID{InventoryAccept, InventoryCreate, InventoryClaim, SearchImageStage, BasicStage, BasicSeal, BasicAccept,
		ProgressAccept, SecurityAccept, ProgressReceiptReclaim}
	for _, set := range []map[CommandID]bool{frozenDirectory, frozenPublisher, frozenEnrichment, frozenEffective} {
		for id := range set {
			ids = append(ids, id)
		}
	}
	return ids
}
func legacyPlanPurpose(p CommandID) bool {
	return !(p != EffectiveStage && p != EnrichAccept && p != InventoryAccept && p != SearchImageStage && p != InventoryCreate && p != InventoryClaim && p != ProgressAccept && p != SecurityAccept && p != ProgressReceiptReclaim && !frozenDirectory[p] && !frozenPublisher[p] && !frozenEnrichment[p])
}

func legacyMemoryPurpose(p CommandID) bool {
	return !(p != InventoryAccept && p != SearchImageStage && p != InventoryCreate && p != InventoryClaim && p != BasicStage && p != BasicSeal && p != BasicAccept && p != ProgressAccept && p != SecurityAccept && p != ProgressReceiptReclaim && p != EffectiveStage && p != EnrichAccept && !frozenDirectory[p] && !frozenPublisher[p] && !frozenEnrichment[p])
}

// The legacy last-assignment-wins partition selection.
func legacyPartition(p CommandID) memoryPartition {
	b := inventoryPartition
	if frozenEnrichment[p] {
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
	if frozenDirectory[p] {
		b = directoryPartition
	}
	if frozenPublisher[p] {
		b = publisherPartition
	}
	if frozenEffective[p] {
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
		return frozenEnrichment[p]
	case basicPartition:
		return p == BasicStage || p == BasicSeal || p == BasicAccept
	case wcProgressPartition:
		return p == ProgressAccept
	case wcSecurityPartition:
		return p == SecurityAccept
	case wcReclaimPartition:
		return p == ProgressReceiptReclaim
	case directoryPartition:
		return frozenDirectory[p]
	case publisherPartition:
		return frozenPublisher[p]
	case effectiveSourcePartition:
		return frozenEffective[p]
	}
	return false
}

func legacyClock(p CommandID) commandClock {
	if p != InventoryAccept && p != SearchImageStage && p != InventoryCreate && p != InventoryClaim && p != BasicStage && p != BasicSeal && p != BasicAccept && p != ProgressAccept && p != SecurityAccept && p != ProgressReceiptReclaim && !frozenDirectory[p] && !frozenPublisher[p] {
		return noCommandClock
	}
	if p == InventoryCreate || p == ProgressAccept || p == SecurityAccept || p == ProgressReceiptReclaim {
		return clockWithoutLease
	}
	return clockUnderLease
}

func TestCommandPurposeRegistryMatchesLegacyGates(t *testing.T) {
	for _, id := range frozenLegacyPurposes() {
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
	// Every registry entry, legacy or new, is a closed command with builder-valid facts.
	for id, p := range closedCommandPurposes {
		if _, closed := closedCommandDescriptors[id]; !closed || p.id != id || p.memory >= memoryPartitionCount || p.clock > clockUnderLease {
			t.Fatal("registry entry escaped builder invariants", id)
		}
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
	// An undeclared command is refused by every shared gate. These IDs can never
	// be declared (not closed), so domain additions never edit this test.
	for _, id := range []CommandID{0, 0xFFFF} {
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
