package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "enrichment",
		resultTypes: []string{"EffectiveStageResult", "EffectiveStageBeginResult"},
	})
}
