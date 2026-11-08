package writer

// EN enrichment stage/seal purposes.
var enrichmentCommandPurposes = commandPurposeRegistration{owner: "enrichment", purposes: []commandPurpose{
	{id: EvidenceStage, plan: true, memory: enrichmentPartition},
	{id: CreditStage, plan: true, memory: enrichmentPartition},
	{id: EvidenceSeal, plan: true, memory: enrichmentPartition},
	{id: CreditSourceSeal, plan: true, memory: enrichmentPartition},
}}

// Effective BEGIN and final EnrichAccept share the source-proof partition.
var effectiveSourceCommandPurposes = commandPurposeRegistration{owner: "effective_source", purposes: []commandPurpose{
	{id: EffectiveStage, plan: true, memory: effectiveSourcePartition},
	{id: EnrichAccept, plan: true, memory: effectiveSourcePartition},
}}
