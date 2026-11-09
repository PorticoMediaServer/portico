package writer

// US PIN composition (532/533). Its dispatch completion deliberately carries no
// bare AckResult: the explicit pre-terminal finishCommand in runUSPINDispatch
// would otherwise be indistinguishable from a deferred release, and a deferred
// finish after transaction.commit/rollback would report a committed command as
// canceled. These selectors pin the helpers that must keep the shared release
// guard, plus real bypass bodies that the AST guard must reject exactly once.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "us_pin_dispatch",
		resultTypes: []string{"usPINPublicImage"},
		names:       []string{"executeUSPINInvocation", "captureUSPINProfileImage", "readUSPINQuota", "applyUSPIN"},
		bypasses: []string{
			`func dispatchBypass(ctx context.Context)(out usPINPublicImage,err error){return out,nil}`,
			`func captureBypass(ctx context.Context)(out usPINPublicImage,err error){return out,nil}`,
			`func readUSPINQuota(ctx context.Context)(value int64,err error){return 1,nil}`,
			`func applyUSPIN(ctx context.Context)(value int64,err error){return 1,nil}`,
		},
	})
}
