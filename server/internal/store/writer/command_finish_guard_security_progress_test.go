package writer

// Keep progress selectors and bypass controls beside the owned domain.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "security_progress",
		resultTypes: []string{"ProgressResult", "ProgressSnapshot", "ProgressCommand", "wcProgressLookup"},
		names:       []string{"newWCProgressDriver", "composeWCProgress", "initializeWCProgressFixture", "checkWCProgressNativeDisclosure"},
		bypasses: []string{
			`func newWCProgressDriver(ctx context.Context)(out *WCProgressDriver,err error){return out,nil}`,
			`func progressBypass(ctx context.Context)(out ProgressResult,err error){return out,nil}`,
			`func snapshotBypass(ctx context.Context)(out ProgressSnapshot,err error){return out,nil}`,
			`func commandBypass(ctx context.Context)(out ProgressCommand,err error){return out,nil}`,
			`func revoke(ctx context.Context)(out commandOutput,err error){return out,nil}`,
			`func reportProgress(ctx context.Context)(out AckResult,err error){return out,nil}`,
		},
	})
}
