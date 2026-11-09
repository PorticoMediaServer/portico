package writer

// US password replacement (520) releases its results through finishCommand
// before the physical terminal, because transaction.commit/rollback cancel the
// frame lifetime: a lifetime-based deferred finish afterwards would report a
// committed command as canceled. runUSPasswordDispatch therefore keeps its
// explicit pre-terminal lifetime release and adds the shared dispatcher's final
// deferred publication on the CALLER's context; the bound helpers below are the
// ones selected by name or result type.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "us_password_replace",
		resultTypes: []string{"usPasswordPostResult"},
		names:       []string{"executeUSPasswordInvocation", "readUSPasswordQuota", "readUSPasswordBinding", "readUSPasswordOwner", "readUSPasswordHeader", "readUSPasswordChildOctets", "readUSPasswordChild", "verifyUSPasswordPostState"},
		bypasses: []string{
			`func executeUSPasswordInvocation(ctx context.Context)(out usPasswordDispatchResult,err error){return out,nil}`,
			`func readUSPasswordQuota(ctx context.Context)(out usPasswordQuotaFacts,err error){return out,nil}`,
			`func passwordPostBypass(ctx context.Context)(out usPasswordPostResult,err error){return out,nil}`,
			`func readUSPasswordChild(ctx context.Context)(err error){return nil}`,
			`func verifyUSPasswordPostState(ctx context.Context)(err error){return nil}`,
		},
	})
}
