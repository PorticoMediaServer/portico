package writer

// Security/progress currently uses Basic's shared commandOutput/AckResult.
// Keep its domain-specific selectors and bypass controls local as it grows.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "security_progress",
		bypasses: []string{
			`func revoke(ctx context.Context)(out commandOutput,err error){return out,nil}`,
			`func reportProgress(ctx context.Context)(out AckResult,err error){return out,nil}`,
		},
	})
}
