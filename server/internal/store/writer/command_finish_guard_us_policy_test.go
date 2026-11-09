package writer

// US policy dispatch releases its results through finishCommand before the
// physical terminal, because transaction.commit/rollback cancel the frame
// lifetime: a deferred finish afterwards would report a committed command as
// canceled. runUSPolicyDispatch therefore keeps an explicit pre-terminal
// release rather than a deferred one, and its bound helpers are selected here.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "us_policy_dispatch",
		resultTypes: []string{"usPolicyPublicImage"},
		names:       []string{"executeUSPolicyInvocation", "captureUSPolicyCallerSnapshot", "readUSPolicyQuota", "readUSPolicyFreshTarget"},
		bypasses: []string{
			`func quotaBypass(ctx context.Context)(out usPolicyQuotaFacts,err error){return out,nil}`,
			`func imageBypass(ctx context.Context)(out usPolicyPublicImage,err error){return out,nil}`,
			`func captureBypass(ctx context.Context)(err error){return nil}`,
		},
	})
}
