package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg-source-commands",
		resultTypes: []string{"mgSourceCommandResult", "mgSourceCommandOutcome"},
		names:       []string{"runMGSourceCommand", "replayMGSourceCommandResult", "acknowledgeMGSourceCommand"},
		bypasses: []string{
			`func runMGSourceCommand(ctx context.Context)(out mgSourceCommandOutcome,err error){return out,nil}`,
			`func acknowledgeMGSourceCommand(ctx context.Context)(ack int64,err error){return 0,nil}`,
		},
	})
}
