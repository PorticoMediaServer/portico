package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ia_refresh", resultTypes: []string{"iaRefreshEntry", "iaRefreshImage", "iaRefreshOutcome"},
		names:    []string{"checkIARefreshCurrent", "changeIARefreshRow", "chargeIARefresh", "iaRefreshExec", "iaRefreshNoRows"},
		bypasses: []string{`func runIARefresh(ctx context.Context)(out iaRefreshOutcome,err error){return out,nil}`, `func enterIARefresh(ctx context.Context)(out *iaRefreshEntry,err error){return out,nil}`},
	})
}
