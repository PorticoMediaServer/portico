package writer

// US policy dispatch releases its results through finishCommand before the
// physical terminal, because transaction.commit/rollback cancel the frame
// lifetime: a lifetime-based deferred finish afterwards would report a
// committed command as canceled. runUSPolicyDispatch therefore keeps its
// explicit pre-terminal lifetime release and adds the shared dispatcher's
// final deferred publication on the CALLER's context, and its bound helpers
// are selected here.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "us_policy_dispatch",
		resultTypes: []string{"usPolicyPublicImage"},
		names:       []string{"executeUSPolicyInvocation", "captureUSPolicyCallerSnapshot", "readUSPolicyQuota", "readUSPolicyFreshTarget"},
		bypasses: []string{
			// Release the command's own result after the effect instead of through a
			// pre-terminal deferred finish: the guard must still refuse it.
			`func executeUSPolicyInvocation(ctx context.Context)(out usPolicyPublicImage,err error){out,err=finishCommand(ctx,ctx,out,err);return out,err}`,
			// Defer the finish but discard both projections, so the named results
			// never receive the physical outcome.
			`func readUSPolicyQuota(ctx context.Context)(out usPolicyQuotaFacts,err error){defer func(){_,_=finishCommand(ctx,ctx,out,err)}();return out,nil}`,
			// Error-only scope check that keeps a payload instead of discarding the
			// empty struct the guard's error-only shape requires.
			`func captureUSPolicyCallerSnapshot(ctx context.Context)(err error){defer func(){_,err=finishCommand(ctx,ctx,struct{Authority string}{},err)}();return nil}`,
		},
	})
}
