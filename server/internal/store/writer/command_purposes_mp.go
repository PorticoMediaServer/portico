package writer

// Only the reviewed first worker instance is opened. MP metadata for all other
// purposes remains inert; future domains add concrete cases, never callbacks.
var mpWorkerCommandPurposes = commandPurposeRegistration{owner: "mp-worker", purposes: []commandPurpose{
	{id: MPQueueRepeat, plan: true, memory: mpWorkerPartition, clock: clockWithoutLease},
}}
