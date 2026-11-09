package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_entity_seal",
		resultTypes: []string{
			"ipEntitySealCommandResult", "ipEntitySealCommand",
			"ipEntitySealResult", "ipEntitySealBuild",
		},
		bypasses: []string{
			`func applyIPEntitySeal(ctx context.Context)(out ipEntitySealCommandResult,err error){return out,nil}`,
		},
	})
}
