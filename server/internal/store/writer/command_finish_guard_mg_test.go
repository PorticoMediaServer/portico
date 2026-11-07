package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg",
		resultTypes: []string{"mgSourceFacts", "mgSourceResult"},
		names:       []string{"mgSourceTime"},
		bypasses: []string{
			`func mgReadSource(ctx context.Context)(out mgSourceFacts,err error){return out,nil}`,
			`func mgConfigureSource(ctx context.Context)(out mgSourceResult,err error){return out,nil}`,
			`func mgSourceTime(ctx context.Context)(value int64,err error){return 1,nil}`,
		},
	})
}
