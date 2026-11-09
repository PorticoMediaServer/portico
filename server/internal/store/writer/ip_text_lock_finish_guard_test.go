package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ip_text_lock",
		resultTypes: []string{"ipTextLockCommandResult", "ipTextLockCommand"},
		bypasses: []string{
			`func applyIPTextFieldLock(ctx context.Context)(out ipTextLockCommandResult,err error){return out,nil}`,
		},
	})
}
