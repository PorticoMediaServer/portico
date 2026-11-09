package writer

// IA21's walkers are same-turn releases: every one of them installs the shared
// finish rule before its first return, so it can never disclose a named key or
// projection after the caller's lifetime is gone.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_refresh_walk",
		resultTypes: []string{"iaRefreshWalkStep"},
		names: []string{
			"iaRefreshWalkCursor", "iaRefreshWalkLive", "iaRefreshWalkJournalChild", "iaRefreshWalkParent",
			"iaRefreshWalkNext", "iaRefreshWalkRestore",
			"iaRefreshNextStreamStep", "iaRefreshNextStreamRestore",
			"iaRefreshNextChapterStep", "iaRefreshNextChapterRestore",
		},
		bypasses: []string{
			`func iaRefreshWalkNext(ctx context.Context)(out iaRefreshWalkStep,err error){return out,nil}`,
			`func iaRefreshNextStreamStep(ctx context.Context)(out iaRefreshWalkStep,err error){return out,nil}`,
		},
	})
}
