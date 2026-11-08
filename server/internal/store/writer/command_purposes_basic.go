package writer

// SC Basic purposes. Their profiles are not installed through freezeCommandPlan.
var basicCommandPurposes = commandPurposeRegistration{owner: "basic", purposes: []commandPurpose{
	{id: BasicStage, memory: basicPartition, clock: clockUnderLease},
	{id: BasicSeal, memory: basicPartition, clock: clockUnderLease},
	{id: BasicAccept, memory: basicPartition, clock: clockUnderLease},
}}
