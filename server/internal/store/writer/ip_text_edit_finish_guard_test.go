package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ip_text_edit",
		resultTypes: []string{"ipTextEditCommandResult", "ipTextEditCommand"},
		bypasses: []string{
			`func applyIPTextEdit(ctx context.Context)(out ipTextEditCommandResult,err error){return out,nil}`,
		},
	})
}
