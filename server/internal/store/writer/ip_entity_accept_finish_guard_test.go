package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_entity_accept",
		resultTypes: []string{
			"ipEntityAcceptCommandResult", "ipEntityAcceptResult",
			"ipEntityAcceptCommand", "ipEntityAcceptReceipt",
		},
		bypasses: []string{
			`func applyIPEntityAccept(ctx context.Context)(out ipEntityAcceptCommandResult,err error){return out,nil}`,
		},
	})
}
