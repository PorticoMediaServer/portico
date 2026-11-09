package writer

// US credential issue/revoke (521/522) releases its complete result through
// finishCommand before the physical terminal, because transaction.commit/rollback
// cancel the frame lifetime: a lifetime-based deferred finish afterwards would
// report a committed command as canceled. runUSCredentialDispatch therefore
// keeps its explicit pre-terminal lifetime release and adds the shared
// dispatcher's final deferred publication on the CALLER's context; the helpers
// whose defers replace a denial (storeUSCredentialResult, lookupUSCredential*,
// acknowledgeUSCredentialAfterAuthority) carry the same deferred-recheck shape
// the shared AST guard cannot express and are deliberately not selected.
// Every bypass below is a REAL unguarded release of this family's result type.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "us_credential_dispatch",
		resultTypes: []string{"usCredentialDispatchResult"},
		bypasses: []string{
			`func credentialDispatchBypass(ctx context.Context)(out usCredentialDispatchResult,err error){return out,nil}`,
			`func credentialCompletionBypass(ctx context.Context)(out usCredentialDispatchResult,err error){return out,nil}`,
		},
	})
}
