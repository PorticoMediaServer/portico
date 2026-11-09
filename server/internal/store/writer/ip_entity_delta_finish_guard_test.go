package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_entity_delta",
		resultTypes: []string{
			"ipEntityDeltaCommandResult", "ipEntityDeltaResult",
			"ipEntityDeltaCommand", "ipEntityDeltaReceipt",
		},
		bypasses: []string{
			`func applyIPEntityDelta(ctx context.Context)(out ipEntityDeltaCommandResult,err error){return out,nil}`,
		},
	})
}
