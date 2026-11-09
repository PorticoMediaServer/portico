package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_policy",
		resultTypes: []string{
			"ipPolicyCommandResult", "ipRefreshCommandResult", "ipPolicyLookup", "ipRefreshLookup",
			"ipPolicyAuthorization", "ipPolicyCommand", "ipRefreshCommand",
		},
		prefixes: []string{"ipPolicy", "ipRefresh"},
		bypasses: []string{
			`func applyIPPolicySet(ctx context.Context)(out ipPolicyCommandResult,err error){return out,nil}`,
			`func applyIPRefreshBegin(ctx context.Context)(out ipRefreshCommandResult,err error){return out,nil}`,
			`func ipPolicyAuthorityRecheck(ctx context.Context)(err error){return nil}`,
		},
	})
}
