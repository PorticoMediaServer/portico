package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:  "effective_source",
		names: []string{"requireEffectiveSourceMemory"},
		bypasses: []string{
			`func requireEffectiveSourceMemory(ctx context.Context,required uint64)(err error){return nil}`,
		},
	})
}
