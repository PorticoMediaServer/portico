package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg-lc",
		resultTypes: []string{"mgLCProgress", "mgLCSaved", "mgLCReceiptDebt"},
		names:       []string{"mgLCTime", "observeMGLC", "acknowledgeMGLC", "recordMGLCReceiptDebt", "scheduleMGLCReceiptDebt"},
		bypasses: []string{
			`func arbitraryLCDebt(ctx context.Context)(out mgLCReceiptDebt,err error){return out,nil}`,
			`func recordMGLCReceiptDebt(ctx context.Context)(err error){return nil}`,
			`func scheduleMGLCReceiptDebt(ctx context.Context)(err error){return nil}`,
			`func arbitraryLCProgress(ctx context.Context)(out mgLCProgress,err error){return out,nil}`,
			`func arbitraryLCSaved(ctx context.Context)(out mgLCSaved,err error){return out,nil}`,
			`func mgLCTime(ctx context.Context)(out int64,err error){return 1,nil}`,
			`func observeMGLC(ctx context.Context)(out int64,err error){return 1,nil}`,
			`func acknowledgeMGLC(ctx context.Context)(out int64,err error){return 1,nil}`,
		},
	})
}
