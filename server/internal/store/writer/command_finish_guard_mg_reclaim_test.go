package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg-guide-reclaim",
		resultTypes: []string{"mgGuideNativeResult", "mgGuideReclaimState", "mgGuideReclaimResult", "mgGuideReclaimPage", "mgGuideCleanupCandidate"},
		names:       []string{"mgGuideReclaimTime"},
		bypasses: []string{
			`func mgDiscardGuideNative(ctx context.Context)(out mgGuideNativeResult,err error){return out,nil}`,
			`func mgReclaimGuideNative(ctx context.Context)(out mgGuideReclaimResult,err error){return out,nil}`,
			`func mgReadGuideReclaimNative(ctx context.Context)(out mgGuideReclaimState,err error){return out,nil}`,
			`func mgReadGuideReclaimKeys(ctx context.Context)(out mgGuideReclaimPage,err error){return out,nil}`,
			`func mgNextGuideCleanupNative(ctx context.Context)(out mgGuideCleanupCandidate,err error){return out,nil}`,
			`func mgGuideReclaimTime(ctx context.Context)(out int64,err error){return 1,nil}`,
		},
	})
}
