package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg-lc-control",
		resultTypes: []string{"mgLCControlSaved", "mgLCControlResult"},
		names:       []string{"mgLCControlTime"},
		bypasses: []string{
			`func arbitraryLCControlSaved(ctx context.Context)(out mgLCControlSaved,err error){return out,nil}`,
			`func arbitraryLCControlResult(ctx context.Context)(out mgLCControlResult,err error){return out,nil}`,
			`func mgLCControlTime(ctx context.Context)(value int64,err error){return 1,nil}`,
		},
	})
}
