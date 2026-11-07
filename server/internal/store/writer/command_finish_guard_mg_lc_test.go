package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg-lc",
		resultTypes: []string{"mgLCProgress", "mgLCSaved"},
		names:       []string{"mgLCTime", "observeMGLC", "acknowledgeMGLC"},
		bypasses: []string{
			`func arbitraryLCProgress(ctx context.Context)(out mgLCProgress,err error){return out,nil}`,
			`func arbitraryLCSaved(ctx context.Context)(out mgLCSaved,err error){return out,nil}`,
			`func mgLCTime(ctx context.Context)(out int64,err error){return 1,nil}`,
			`func observeMGLC(ctx context.Context)(out int64,err error){return 1,nil}`,
			`func acknowledgeMGLC(ctx context.Context)(out int64,err error){return 1,nil}`,
		},
	})
}
