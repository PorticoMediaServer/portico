package writer

import (
	"errors"
	"testing"
)

// The purpose registry replaced the pre-registry gate expressions frozen at
// e0953113. This file holds every declaration to the shared gates' invariants
// instead of pinning per-purpose sets: a global purpose list was a hotspot that
// every new domain purpose had to edit (#1680). The registry is the only source
// of purpose facts.
func TestCommandPurposeRegistryInvariants(t *testing.T) {
	if len(closedCommandPurposes) == 0 {
		t.Fatal("no command purposes")
	}
	byPartition := map[memoryPartition][]CommandID{}
	for id, p := range closedCommandPurposes {
		// Every entry names a closed command and carries builder-valid facts.
		if _, closed := closedCommandDescriptors[id]; !closed || p.id != id || p.memory >= memoryPartitionCount || p.clock > clockUnderLease {
			t.Fatal("registry entry escaped builder invariants", id)
		}
		// A plan purpose must hold an enforceable fact: freezeCommandPlan installs
		// a profile and the purpose then gates a memory partition or a clock mode.
		if p.plan && p.memory == noMemoryPartition && p.clock == noCommandClock {
			t.Fatal("plan purpose holds neither a partition nor a clock", id)
		}
		// A purpose belongs to exactly one partition, and the partition gate must
		// report the declared one only (the per-field legacy predicates, #1680).
		claims := 0
		for part := noMemoryPartition + 1; part < memoryPartitionCount; part++ {
			if purposeUsesPartition(id, part) {
				claims++
			}
		}
		if claims > 1 || (claims == 1) != (p.memory != noMemoryPartition) {
			t.Fatal("partition gate disagrees with declared facts", id, claims, p.memory)
		}
		if p.memory != noMemoryPartition {
			byPartition[p.memory] = append(byPartition[p.memory], id)
		}
	}
	// A partition is one plan profile field: no declared partition may be dead.
	for part := noMemoryPartition + 1; part < memoryPartitionCount; part++ {
		if len(byPartition[part]) == 0 {
			t.Fatal("declared partition has no purpose", part)
		}
	}
	// Membership is total: every closed command is either registered or refused
	// by every shared gate. Only the registry can admit a purpose.
	for id := range closedCommandDescriptors {
		if _, declared := commandPurposeFacts(id); declared {
			continue
		}
		if commandPlanPurpose(id) {
			t.Fatal("undeclared purpose reached the plan gate", id)
		}
		for part := noMemoryPartition + 1; part < memoryPartitionCount; part++ {
			if purposeUsesPartition(id, part) {
				t.Fatal("undeclared purpose reached the partition gate", id, part)
			}
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
