package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_tv_set",
		resultTypes: []string{
			"ipTVSetCommandResult", "ipTVSetCommand",
			"ipTVSetResult", "ipTVSetHeader", "ipTVSetLastPage",
		},
		bypasses: []string{
			`func applyIPTVSetSeal(ctx context.Context)(out ipTVSetCommandResult,err error){return out,nil}`,
		},
	})
}
