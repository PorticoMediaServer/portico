package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:  "effective_source",
		names: []string{"requireEffectiveSourceMemory", "checkEffectiveSourceControlPoints"},
		bypasses: []string{
			`func requireEffectiveSourceMemory(ctx context.Context,required uint64)(err error){return nil}`,
			`func checkEffectiveSourceControlPoints(ctx context.Context,turn *commandTurn,c *effectiveStageBeginCommand,e,s effectivePinnedSealReceipt)(err error){return nil}`,
		},
	})
}
