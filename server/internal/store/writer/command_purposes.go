package writer

import "fmt"

// Closed per-domain purpose facts for the shared plan, memory-partition and
// command-clock gates. Each domain declares its purposes in its own
// command_purposes_<domain>.go file; this table is built once at package init.
// There is no registration API and no runtime append/mutation path. A purpose
// absent here is refused by every gate below.
type memoryPartition uint8

const (
	noMemoryPartition memoryPartition = iota
	inventoryPartition
	searchImagePartition
	scanLifecyclePartition
	enrichmentPartition
	basicPartition
	wcProgressPartition
	wcSecurityPartition
	wcReclaimPartition
	directoryPartition
	publisherPartition
	effectiveSourcePartition
	memoryPartitionCount
)

// How commandTimeMS binds the lease argument for a purpose.
type commandClock uint8

const (
	noCommandClock    commandClock = iota
	clockWithoutLease              // leaseExpiresMS must be zero
	clockUnderLease                // leaseExpiresMS must be positive
)

type commandPurpose struct {
	id     CommandID
	plan   bool            // freezeCommandPlan may install a profile
	memory memoryPartition // requireCommandMemory partition; none refuses
	clock  commandClock    // commandTimeMS lease mode; none refuses
}

type commandPurposeRegistration struct {
	owner    string
	purposes []commandPurpose
}

var closedCommandPurposes = mustBuildCommandPurposes(
	scanCommandPurposes, basicCommandPurposes, searchImageCommandPurposes, searchPublisherCommandPurposes,
	wcCommandPurposes, enrichmentCommandPurposes, effectiveSourceCommandPurposes,
)

// Pure builder; only the package initializer above installs its result.
func buildCommandPurposes(registrations ...commandPurposeRegistration) (map[CommandID]commandPurpose, error) {
	out := map[CommandID]commandPurpose{}
	owners := map[string]bool{}
	for _, r := range registrations {
		if r.owner == "" || owners[r.owner] || len(r.purposes) == 0 {
			return nil, fmt.Errorf("writer: invalid command purpose registration %q", r.owner)
		}
		owners[r.owner] = true
		for _, p := range r.purposes {
			if _, ok := closedCommandDescriptors[p.id]; !ok {
				return nil, fmt.Errorf("writer: %s declares unknown command purpose %d", r.owner, p.id)
			}
			if _, dup := out[p.id]; dup {
				return nil, fmt.Errorf("writer: command purpose %d declared twice", p.id)
			}
			if p.memory >= memoryPartitionCount || p.clock > clockUnderLease || (!p.plan && p.memory == noMemoryPartition && p.clock == noCommandClock) {
				return nil, fmt.Errorf("writer: %s declares invalid facts for command purpose %d", r.owner, p.id)
			}
			out[p.id] = p
		}
	}
	return out, nil
}

func mustBuildCommandPurposes(registrations ...commandPurposeRegistration) map[CommandID]commandPurpose {
	out, err := buildCommandPurposes(registrations...)
	if err != nil {
		panic(err)
	}
	return out
}

func commandPurposeFacts(id CommandID) (commandPurpose, bool) {
	p, ok := closedCommandPurposes[id]
	return p, ok
}

func commandPlanPurpose(id CommandID) bool {
	p, ok := commandPurposeFacts(id)
	return ok && p.plan
}

// Only the declared partition may carry a nonzero plan/custody budget.
func purposeUsesPartition(id CommandID, partition memoryPartition) bool {
	p, ok := commandPurposeFacts(id)
	return ok && partition != noMemoryPartition && p.memory == partition
}
