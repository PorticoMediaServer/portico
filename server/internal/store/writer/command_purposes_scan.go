package writer

// SC scan inventory, lifecycle and directory purposes.
var scanCommandPurposes = commandPurposeRegistration{owner: "scan", purposes: []commandPurpose{
	{id: InventoryAccept, plan: true, memory: inventoryPartition, clock: clockUnderLease},
	{id: InventoryCreate, plan: true, memory: scanLifecyclePartition, clock: clockWithoutLease},
	{id: InventoryClaim, plan: true, memory: scanLifecyclePartition, clock: clockUnderLease},
	{id: InventoryDirectoryDiscover, plan: true, memory: directoryPartition, clock: clockUnderLease},
	{id: InventoryDirectoryEOF, plan: true, memory: directoryPartition, clock: clockUnderLease},
	{id: InventoryDirectoryRefuse, plan: true, memory: directoryPartition, clock: clockUnderLease},
	{id: InventoryTraversalComplete, plan: true, memory: directoryPartition, clock: clockUnderLease},
}}
