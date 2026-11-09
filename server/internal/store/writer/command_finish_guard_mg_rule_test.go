package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg_rule",
		resultTypes: []string{"mgRuleFacts", "mgRuleAcceptance"},
		bypasses: []string{
			`func saveMGRule(ctx context.Context)(out mgRuleFacts,err error){return out,nil}`,
			`func deleteMGRule(ctx context.Context)(out mgRuleFacts,err error){return out,nil}`,
			`func acceptMGRuleSave(ctx context.Context)(out mgRuleAcceptance,err error){return out,nil}`,
			`func acceptMGRuleDelete(ctx context.Context)(out mgRuleAcceptance,err error){return out,nil}`,
		},
	})
}
