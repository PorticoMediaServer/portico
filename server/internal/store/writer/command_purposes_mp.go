package writer

// Only the reviewed first worker instance is opened. MP metadata for all other
// purposes remains inert; future domains add concrete cases, never callbacks.
var mpWorkerCommandPurposes = commandPurposeRegistration{owner: "mp-worker", purposes: []commandPurpose{
	{id: MPQueueRepeat, plan: true, memory: mpWorkerPartition, clock: clockWithoutLease},
	{id: MPStartPrepBegin, plan: true, memory: mpWorkerPartition, clock: clockWithoutLease},
	{id: MPStartPrepClaim, plan: true, memory: mpWorkerPartition, clock: clockWithoutLease},
	{id: MPStartPrepSeal, plan: true, memory: mpWorkerPartition, clock: clockWithoutLease},
	{id: MPStartPrepAccept, plan: true, memory: mpWorkerPartition, clock: clockWithoutLease},
	{id: MPStartPrepRetire, plan: true, memory: mpWorkerPartition, clock: clockWithoutLease},
	{id: MPStartPrepTimeout, plan: true, memory: mpWorkerPartition, clock: clockWithoutLease},
}}
