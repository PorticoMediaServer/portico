package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "enrichment",
		resultTypes: []string{"EffectiveStageResult", "EffectiveStageBeginResult", "enrichmentCommandResult", "enrichmentSealHistory", "enrichmentTurnLookup"},
		names:       []string{"composeEnrichmentSealHistory", "discloseEnrichment", "acknowledgeEnrichment"},
	})
}
