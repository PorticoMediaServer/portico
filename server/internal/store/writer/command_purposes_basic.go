package writer

// SC Basic purposes. All three install their closed profiles through freezeCommandPlan.
var basicCommandPurposes = commandPurposeRegistration{owner: "basic", purposes: []commandPurpose{
	{id: BasicStage, plan: true, memory: basicPartition, clock: clockUnderLease},
	{id: BasicSeal, plan: true, memory: basicPartition, clock: clockUnderLease},
	{id: BasicAccept, plan: true, memory: basicPartition, clock: clockUnderLease},
}}
