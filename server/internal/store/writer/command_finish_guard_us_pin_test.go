package writer

// US PIN composition (532/533). runUSPINDispatch keeps its explicit
// pre-terminal lifetime release and adds the shared dispatcher's final deferred
// publication on the CALLER's context: a lifetime-based deferred finish after
// transaction.commit/rollback would report a committed command as canceled.
// These selectors pin the dispatch entry and the helpers that must keep the
// shared release guard, plus real bypass bodies that the AST guard must reject
// exactly once.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "us_pin_dispatch",
		resultTypes: []string{"usPINPublicImage"},
		names:       []string{"runUSPINDispatch", "executeUSPINInvocation", "captureUSPINProfileImage", "captureUSPINSelfSnapshot", "readUSPINQuota", "applyUSPIN"},
		bypasses: []string{
			// Release the command's own result in the body instead of through the
			// required deferred final publication: the guard must still refuse it.
			`func runUSPINDispatch(ctx context.Context)(out usPINOutcome,err error){out,err=finishCommand(ctx,ctx,out,err);return out,err}`,
			`func dispatchBypass(ctx context.Context)(out usPINPublicImage,err error){return out,nil}`,
			`func captureBypass(ctx context.Context)(out usPINPublicImage,err error){return out,nil}`,
			`func readUSPINQuota(ctx context.Context)(value int64,err error){return 1,nil}`,
			`func applyUSPIN(ctx context.Context)(value int64,err error){return 1,nil}`,
		},
	})
}
